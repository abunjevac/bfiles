package config

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Change sets one setting. Key is "name" for top-level settings or "table.name"
// (e.g. "panels.sort", "bookmarks.work"). A nil Value removes the setting.
type Change struct {
	Key   string
	Value any
}

var bareKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// UpdateFile applies changes to a TOML file in place, editing only the affected lines
// so comments and layout survive. The result must still parse as a valid config,
// otherwise nothing is written.
func UpdateFile(path string, changes []Change) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read-config: %w", err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")

	for _, c := range changes {
		lines, err = apply(lines, c)
		if err != nil {
			return err
		}
	}

	out := strings.Join(lines, "\n") + "\n"

	if _, err := Parse(out); err != nil {
		return fmt.Errorf("refusing to save an invalid config: %w", err)
	}

	return writeFileAtomic(path, []byte(out), configMode)
}

func apply(lines []string, c Change) ([]string, error) {
	table, key := "", c.Key

	if t, k, ok := strings.Cut(c.Key, "."); ok {
		table, key = t, k
	}

	newLine, err := settingLine(key, c)
	if err != nil {
		return nil, err
	}

	start, end, found := sectionBounds(lines, table)

	if !found {
		if c.Value == nil {
			return lines, nil
		}

		return append(lines, "", "["+table+"]", newLine), nil
	}

	if i, ok := findKey(lines, start, end, key); ok {
		last := valueEnd(lines, i)

		if c.Value == nil {
			return append(lines[:i], lines[last+1:]...), nil
		}

		return splice(lines, i, last+1, newLine), nil
	}

	if c.Value == nil {
		return lines, nil
	}

	// insert after the last setting of the section, before trailing blank lines
	// and the comment block that introduces the next header
	at := end

	for at > start && isBlankOrComment(lines[at-1]) {
		at--
	}

	return splice(lines, at, at, newLine), nil
}

func isBlankOrComment(l string) bool {
	t := strings.TrimSpace(l)

	return t == "" || strings.HasPrefix(t, "#")
}

// settingLine renders the "key = value" line for a change; it is empty when
// the change removes the key.
func settingLine(key string, c Change) (string, error) {
	if c.Value == nil {
		return "", nil
	}

	keyText := key
	if !bareKeyRe.MatchString(key) {
		keyText = fmt.Sprintf("%q", key)
	}

	v, err := encodeValue(c.Value)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", c.Key, err)
	}

	return keyText + " = " + v, nil
}

// findKey returns the line within [start, end) that sets key.
func findKey(lines []string, start, end int, key string) (int, bool) {
	for i := start; i < end; i++ {
		k, _, ok := lineKey(lines[i])
		if !ok {
			continue
		}

		if k == key {
			return i, true
		}

		i = valueEnd(lines, i)
	}

	return 0, false
}

// splice returns a copy of lines with lines[from:to] replaced by line.
func splice(lines []string, from, to int, line string) []string {
	out := append([]string{}, lines[:from]...)
	out = append(out, line)

	return append(out, lines[to:]...)
}

func encodeValue(v any) (string, error) {
	var buf bytes.Buffer

	if err := toml.NewEncoder(&buf).Encode(map[string]any{"v": v}); err != nil {
		return "", err
	}

	s := strings.TrimSpace(buf.String())

	return strings.TrimPrefix(s, "v = "), nil
}

// sectionBounds returns the line range [start, end) of a table's body; table ""
// is the top level before the first header. Value continuation lines are skipped,
// so a nested array or multi-line string is never taken for a header.
func sectionBounds(lines []string, table string) (int, int, bool) {
	start := -1

	if table == "" {
		start = 0
	}

	for i := 0; i < len(lines); i++ {
		if _, _, ok := lineKey(lines[i]); ok {
			i = valueEnd(lines, i)

			continue
		}

		name, isHeader := headerName(lines[i])
		if !isHeader {
			continue
		}

		if start >= 0 {
			return start, i, true
		}

		if name == table {
			start = i + 1
		}
	}

	if start >= 0 {
		return start, len(lines), true
	}

	return 0, 0, false
}

// headerName returns the name of a "[table]" header line, tolerating spaces inside
// the brackets, a quoted name and a trailing comment. Array-table headers ("[[x]]")
// are headers too, but their name never matches a plain table.
func headerName(l string) (string, bool) {
	t := strings.TrimSpace(l)

	if !strings.HasPrefix(t, "[") {
		return "", false
	}

	if strings.HasPrefix(t, "[[") {
		return "[" + t, true
	}

	name, _, ok := strings.Cut(t[1:], "]")
	if !ok {
		return "", false
	}

	name = strings.TrimSpace(name)

	if k, n, ok := parseKey(name); ok && n == len(name) {
		name = k
	}

	return name, true
}

// lineKey extracts the key of a "key = value" line and the index where the value
// starts. Quoted keys are unescaped, so a key containing '=' or '\' is found.
func lineKey(l string) (string, int, bool) {
	t := strings.TrimLeft(l, " \t")

	if t == "" || t[0] == '#' || t[0] == '[' {
		return "", 0, false
	}

	k, n, ok := parseKey(t)
	if !ok {
		return "", 0, false
	}

	rest := strings.TrimLeft(t[n:], " \t")
	if !strings.HasPrefix(rest, "=") {
		return "", 0, false
	}

	return k, len(l) - len(rest) + 1, true
}

// parseKey reads a bare, basic-quoted or literal-quoted key at the start of s and
// returns it with the number of bytes consumed.
func parseKey(s string) (string, int, bool) {
	if s == "" {
		return "", 0, false
	}

	switch s[0] {
	case '"':
		end := closingQuote(s, 0)
		if end >= len(s) {
			return "", 0, false
		}

		k, err := strconv.Unquote(s[:end+1])
		if err != nil {
			return "", 0, false
		}

		return k, end + 1, true
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", 0, false
		}

		return s[1 : end+1], end + 2, true
	}

	n := strings.IndexAny(s, " \t=")
	if n < 0 {
		n = len(s)
	}

	return s[:n], n, n > 0
}

// valueEnd returns the last line of a value whose key is on line i, following
// multi-line arrays and multi-line strings until they close.
func valueEnd(lines []string, i int) int {
	_, at, ok := lineKey(lines[i])
	if !ok {
		return i
	}

	var v valueScan

	for j := i; j < len(lines); j++ {
		l := lines[j]

		if j == i {
			l = l[at:]
		}

		v.scan(l)

		if v.depth <= 0 && v.multi == 0 {
			return j
		}
	}

	return len(lines) - 1
}

// valueScan tracks bracket depth and an open multi-line string across lines.
type valueScan struct {
	depth int
	// multi is the quote character of an open """ or ''' string, or 0.
	multi byte
}

// scan updates the state with one line, ignoring brackets in strings and comments.
func (v *valueScan) scan(s string) {
	for i := 0; i < len(s); i++ {
		if v.multi != 0 {
			i = v.scanMulti(s, i)

			continue
		}

		switch s[i] {
		case '"', '\'':
			if strings.HasPrefix(s[i:], strings.Repeat(s[i:i+1], 3)) {
				v.multi = s[i]
				i += 2

				continue
			}

			i = closingQuote(s, i)
		case '#':
			return
		case '[':
			v.depth++
		case ']':
			v.depth--
		}
	}
}

// scanMulti handles s[i] inside a multi-line string and returns the index of the
// last byte it consumed.
func (v *valueScan) scanMulti(s string, i int) int {
	if s[i] == '\\' && v.multi == '"' {
		return i + 1
	}

	if !strings.HasPrefix(s[i:], strings.Repeat(string(v.multi), 3)) {
		return i
	}

	// up to two extra quotes may close the string as part of its content
	end := i + 3
	for end < len(s) && end < i+5 && s[end] == v.multi {
		end++
	}

	v.multi = 0

	return end - 1
}

// closingQuote returns the index of the quote closing the string opened at i,
// or len(s) when it is unterminated; only basic strings honour escapes.
func closingQuote(s string, i int) int {
	quote := s[i]

	for j := i + 1; j < len(s); j++ {
		switch {
		case s[j] == '\\' && quote == '"':
			j++
		case s[j] == quote:
			return j
		}
	}

	return len(s)
}
