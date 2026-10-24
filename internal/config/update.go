package config

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
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

	return writeFileAtomic(path, []byte(out))
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
	at := end

	for at > start && strings.TrimSpace(lines[at-1]) == "" {
		at--
	}

	return splice(lines, at, at, newLine), nil
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
		if k, ok := lineKey(lines[i]); ok && k == key {
			return i, true
		}
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
// is the top level before the first header.
func sectionBounds(lines []string, table string) (int, int, bool) {
	start := -1

	if table == "" {
		start = 0
	}

	for i, l := range lines {
		t := strings.TrimSpace(l)

		if !strings.HasPrefix(t, "[") {
			continue
		}

		if start >= 0 {
			return start, i, true
		}

		if t == "["+table+"]" {
			start = i + 1
		}
	}

	if start >= 0 {
		return start, len(lines), true
	}

	return 0, 0, false
}

// lineKey extracts the key of a "key = value" line.
func lineKey(l string) (string, bool) {
	t := strings.TrimSpace(l)

	if t == "" || strings.HasPrefix(t, "#") {
		return "", false
	}

	k, _, ok := strings.Cut(t, "=")
	if !ok {
		return "", false
	}

	k = strings.TrimSpace(k)

	if len(k) >= 2 && (k[0] == '"' || k[0] == '\'') && k[len(k)-1] == k[0] {
		k = k[1 : len(k)-1]
	}

	return k, true
}

// valueEnd returns the last line of a value starting at line i, following
// multi-line arrays until their brackets balance.
func valueEnd(lines []string, i int) int {
	depth := 0

	for j := i; j < len(lines); j++ {
		l := lines[j]

		if j == i {
			_, l, _ = strings.Cut(l, "=")
		}

		depth += bracketDelta(l)

		if depth <= 0 {
			return j
		}
	}

	return len(lines) - 1
}

// bracketDelta counts [ minus ] outside strings and comments.
func bracketDelta(s string) int {
	d := 0

	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\'':
			i = closingQuote(s, i)
		case '#':
			return d
		case '[':
			d++
		case ']':
			d--
		}
	}

	return d
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
