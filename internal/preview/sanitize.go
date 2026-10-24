package preview

import (
	"strconv"
	"strings"
)

// sanitize keeps printable text and SGR colour sequences and drops every other
// control or escape sequence, so file contents and tool output cannot move the
// cursor, change the title or otherwise corrupt the screen. With noBg, background
// colours are removed so previews blend with the theme.
func sanitize(s string, noBg bool) string {
	var sb strings.Builder

	sb.Grow(len(s))

	for i := 0; i < len(s); i++ {
		c := s[i]

		switch {
		case c == 0x1b:
			i = escape(&sb, s, i, noBg)
		case c == '\t':
			sb.WriteString("    ")
		case c < 0x20 || c == 0x7f:
			// drop other control characters
		default:
			sb.WriteByte(c)
		}
	}

	return sb.String()
}

// escape handles an escape sequence starting at s[i] and returns the index of its
// last byte.
func escape(sb *strings.Builder, s string, i int, noBg bool) int {
	if i+1 >= len(s) {
		return i
	}

	switch s[i+1] {
	case '[':
		return csi(sb, s, i, noBg)
	case ']', 'P', '_', '^':
		return stringSequenceEnd(s, i)
	default:
		return i + 1
	}
}

// csi keeps an SGR sequence (filtered) and drops any other control sequence; it
// returns the index of the final byte.
func csi(sb *strings.Builder, s string, i int, noBg bool) int {
	j := i + 2

	for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
		j++
	}

	if j >= len(s) {
		return len(s) - 1
	}

	if s[j] == 'm' {
		if params := filterSGR(s[i+2:j], noBg); params != "" || i+2 == j {
			sb.WriteString("\x1b[" + params + "m")
		}
	}

	return j
}

// stringSequenceEnd returns the index of the terminator of a string sequence,
// which ends with BEL or ST (ESC \).
func stringSequenceEnd(s string, i int) int {
	for j := i + 2; j < len(s); j++ {
		if s[j] == 0x07 {
			return j
		}

		if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
			return j + 1
		}
	}

	return len(s) - 1
}

func filterSGR(params string, noBg bool) string {
	if !noBg || params == "" {
		return params
	}

	parts := strings.Split(params, ";")
	out := make([]string, 0, len(parts))

	for k := 0; k < len(parts); k++ {
		n, _ := strconv.Atoi(parts[k])

		switch {
		case n == 48:
			// skip 48;5;n or 48;2;r;g;b
			k += colourArgs(parts, k, false)
		case n == 38:
			extra := colourArgs(parts, k, true)

			out = append(out, parts[k:k+1+extra]...)
			k += extra
		case isBackground(n):
		default:
			out = append(out, parts[k])
		}
	}

	return strings.Join(out, ";")
}

// colourArgs returns how many parameters follow an extended colour introducer
// at parts[k] (5;n or 2;r;g;b). With complete, a truncated colour counts as none.
func colourArgs(parts []string, k int, complete bool) int {
	if k+1 >= len(parts) {
		return 0
	}

	var n int

	switch parts[k+1] {
	case "5":
		n = 2
	case "2":
		n = 4
	default:
		return 0
	}

	if complete && k+n >= len(parts) {
		return 0
	}

	return n
}

// isBackground reports whether an SGR code sets a basic or bright background.
func isBackground(n int) bool {
	return (n >= 40 && n <= 47) || n == 49 || (n >= 100 && n <= 107)
}
