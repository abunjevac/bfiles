package preview

import (
	"bytes"
	"path"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// tabWidth expands tabs so column math in the UI stays simple.
const tabWidth = 4

// maxHighlightBytes skips highlighting for large inputs, where lexing is slow.
const maxHighlightBytes = 512 << 10

// StyleFor picks a chroma style matching a bfiles theme.
func StyleFor(theme string, dark bool) string {
	switch theme {
	case "tokyonight":
		return "tokyonight-night"
	case "catppuccin-mocha", "catppuccin-latte", "nord", "dracula", "rose-pine", "solarized-light":
		return theme
	case "gruvbox-dark":
		return "gruvbox"
	case "kanagawa":
		return "kanagawa-wave"
	case "ayu-dark":
		return "github-dark"
	}

	if dark {
		return "onedark"
	}

	return "github"
}

func textPreview(data []byte, truncated bool, req Request) Result {
	text := string(data)
	lines := splitLines(text, req.MaxLines)

	if len(lines) < strings.Count(text, "\n") {
		truncated = true
	}

	text = strings.Join(lines, "\n")
	highlighted := highlight(req.Entry.Name, text, req.Style)

	if highlighted != nil {
		lines = highlighted
	} else {
		for i, l := range lines {
			lines[i] = sanitize(l, true)
		}
	}

	return Result{Kind: KindText, Title: lexerName(req.Entry.Name, text), Lines: lines, Truncated: truncated}
}

func splitLines(text string, limit int) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	if len(lines) > limit {
		lines = lines[:limit]
	}

	for i, l := range lines {
		lines[i] = expandTabs(l)
	}

	return lines
}

func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}

	var (
		sb  strings.Builder
		col int
	)

	for _, r := range s {
		if r == '\t' {
			n := tabWidth - col%tabWidth
			sb.WriteString(strings.Repeat(" ", n))
			col += n

			continue
		}

		sb.WriteRune(r)

		col++
	}

	return sb.String()
}

func lexerFor(name, text string) chroma.Lexer {
	l := lexers.Match(path.Base(name))

	if l == nil && len(text) > 0 {
		l = lexers.Analyse(text)
	}

	return l
}

func lexerName(name, text string) string {
	if l := lexerFor(name, text); l != nil {
		return l.Config().Name
	}

	return "text"
}

// highlight returns colourised lines, or nil when no lexer applies.
func highlight(name, text, style string) []string {
	if len(text) > maxHighlightBytes {
		return nil
	}

	lexer := lexerFor(name, text)
	if lexer == nil {
		return nil
	}

	lexer = chroma.Coalesce(lexer)

	st := styles.Get(style)
	if st == nil {
		st = styles.Fallback
	}

	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		return nil
	}

	var buf bytes.Buffer

	if err := formatters.TTY16m.Format(&buf, st, it); err != nil {
		return nil
	}

	out := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")

	// the formatter carries colours across lines; reset each line so the UI can
	// slice and pad them independently
	for i, l := range out {
		out[i] = sanitize(l, true) + "\x1b[0m"
	}

	return out
}
