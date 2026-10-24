package ui

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// humanSize renders a byte count compactly: 999, 1.2 K, 34 M, 1.5 G.
func humanSize(n int64) string {
	const unit = 1024

	if n < unit {
		return fmt.Sprintf("%d", n)
	}

	div, exp := int64(unit), 0

	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}

	v := float64(n) / float64(div)
	suffix := string("KMGTPE"[exp])

	if v < 10 {
		return fmt.Sprintf("%.1f %s", v, suffix)
	}

	return fmt.Sprintf("%.0f %s", v, suffix)
}

// groupDigits renders n with thin-space thousands separators: 1 234 567.
func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")

	var sb strings.Builder

	if neg {
		sb.WriteByte('-')
	}

	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			sb.WriteString("\u2009")
		}

		sb.WriteRune(c)
	}

	return sb.String()
}

// shortTime renders a modification time like ls: time of day for this year, else year.
func shortTime(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}

	if t.Year() == now.Year() {
		return t.Format("Jan _2 15:04")
	}

	return t.Format("Jan _2  2006")
}

// permString renders mode bits like ls -l.
func permString(m fs.FileMode) string {
	return m.String()
}

// fit pads or truncates s (which may contain ANSI) to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}

	sw := ansi.StringWidth(s)

	switch {
	case sw > w:
		return ansi.Truncate(s, w, "…")
	case sw < w:
		return s + strings.Repeat(" ", w-sw)
	}

	return s
}

// fitLeft truncates from the left, keeping the end of s visible (paths).
func fitLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}

	sw := ansi.StringWidth(s)

	if sw <= w {
		return s + strings.Repeat(" ", w-sw)
	}

	return ansi.TruncateLeft(s, sw-w+1, "…")
}

// padRight right-aligns s in w cells.
func padRight(s string, w int) string {
	sw := ansi.StringWidth(s)

	if sw >= w {
		return ansi.Truncate(s, w, "")
	}

	return strings.Repeat(" ", w-sw) + s
}

// progressBar renders a bar of w cells for done/total.
func progressBar(done, total int64, w int) string {
	if w <= 0 {
		return ""
	}

	filled := 0

	if total > 0 {
		filled = int(float64(w) * float64(min(done, total)) / float64(total))
	}

	return strings.Repeat("█", filled) + strings.Repeat("░", w-filled)
}

// duration renders an ETA compactly.
func duration(d time.Duration) string {
	d = d.Round(time.Second)

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
