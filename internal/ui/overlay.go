package ui

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// placement says where an overlay is drawn.
type placement int

const (
	placeCenter placement = iota
	placeTop
	placeFull
)

// overlay is a modal layer on top of the panels. The top-most overlay receives all
// key and paste messages, and any message overlays opt into.
type overlay interface {
	update(m *Model, msg tea.Msg) (done bool, cmd tea.Cmd)
	view(m *Model, w, h int) string
	place() placement
}

func (m *Model) push(o overlay) {
	m.overlays = append(m.overlays, o)
}

func (m *Model) pop() {
	if n := len(m.overlays); n > 0 {
		m.overlays = m.overlays[:n-1]
	}
}

// isOpen reports whether o is still on the overlay stack.
func (m *Model) isOpen(o overlay) bool {
	return slices.Contains(m.overlays, o)
}

func (m *Model) top() overlay {
	if n := len(m.overlays); n > 0 {
		return m.overlays[n-1]
	}

	return nil
}

// level is a toast severity.
type level int

const (
	levelInfo level = iota
	levelWarn
	levelError
)

type toast struct {
	text  string
	level level
	until time.Time
}

const toastTTL = 4 * time.Second

func (m *Model) toast(text string, lvl level) tea.Cmd {
	ttl := toastTTL

	if lvl == levelError {
		ttl *= 2
	}

	// identical consecutive messages refresh instead of stacking
	if n := len(m.toasts); n > 0 && m.toasts[n-1].text == text {
		m.toasts[n-1].until = timeNow().Add(ttl)
	} else {
		m.toasts = append(m.toasts, toast{text: text, level: lvl, until: timeNow().Add(ttl)})
	}

	if len(m.toasts) > 5 {
		m.toasts = m.toasts[len(m.toasts)-5:]
	}

	return tea.Tick(ttl, func(time.Time) tea.Msg { return toastExpireMsg{} })
}

func (m *Model) expireToasts() {
	now := timeNow()
	kept := m.toasts[:0]

	for _, t := range m.toasts {
		if t.until.After(now) {
			kept = append(kept, t)
		}
	}

	m.toasts = kept
}

// timeNow is a seam for tests.
var timeNow = time.Now //nolint:gochecknoglobals

// boxWidth chooses a dialog width for the screen.
func boxWidth(screen, want int) int {
	return max(min(want, screen-4), 20)
}

// renderBox draws a titled card. Lines are padded to the inner width with the
// overlay background so the card is opaque.
func (m *Model) renderBox(title string, lines []string, w int) string {
	return m.renderBoxWith(m.st.box, title, lines, w)
}

// renderBoxWith is renderBox with a custom frame style (e.g. a highlighted border).
func (m *Model) renderBoxWith(frame lipgloss.Style, title string, lines []string, w int) string {
	inner := w - 4
	st := m.st

	out := make([]string, 0, len(lines)+1)

	if title != "" {
		out = append(out, st.boxTitle.Render(fit(title, inner)))
	}

	for _, l := range lines {
		out = append(out, padBox(l, inner, st.boxText))
	}

	return frame.Width(w).Render(strings.Join(out, "\n"))
}

// padBox fits a pre-styled line to w cells, filling the rest with the box style.
func padBox(l string, w int, fill lipgloss.Style) string {
	lw := ansi.StringWidth(l)

	if lw > w {
		return ansi.Truncate(l, w, "…")
	}

	return l + fill.Render(strings.Repeat(" ", w-lw))
}

// keyHint renders "key label" pairs for dialog footers.
func (m *Model) keyHint(pairs ...string) string {
	var parts []string

	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, m.st.boxKey.Render(pairs[i])+m.st.boxMuted.Render(" "+pairs[i+1]))
	}

	return strings.Join(parts, m.st.boxMuted.Render("  "))
}
