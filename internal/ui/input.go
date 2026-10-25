package ui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// textInput is a single-line editor used by dialogs and the panel filter.
type textInput struct {
	value []rune
	pos   int
	mask  bool
	// selected marks the whole value as selected: typing replaces it, moving the
	// cursor keeps it.
	selected bool
}

func newInput(v string) textInput {
	r := []rune(v)

	return textInput{value: r, pos: len(r)}
}

// newSelectedInput starts with the whole value selected, so typing replaces it.
func newSelectedInput(v string) textInput {
	t := newInput(v)
	t.selected = len(t.value) > 0

	return t
}

func (t *textInput) String() string { return string(t.value) }

// collapseSelection ends a whole-value selection before an editing key: keys that
// type or delete replace the value, cursor keys keep it.
func (t *textInput) collapseSelection(key string, text bool) {
	if !t.selected {
		return
	}

	t.selected = false

	switch {
	case text, key == keyBackspace, key == keyDelete, key == "ctrl+h", key == keyCtrlD:
		t.value = nil
		t.pos = 0
	case key == keyLeft, key == keyHome, key == keyCtrlA, key == keyCtrlB:
		t.pos = 0
	}
}

// update applies an editing key and reports whether it was consumed.
func (t *textInput) update(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		t.collapseSelection("", true)
		t.insert(strings.ReplaceAll(msg.Content, "\n", " "))

		return true
	case tea.KeyPressMsg:
		return t.key(msg)
	}

	return false
}

// inputKeys are the editing commands of textInput, readline style.
//
//nolint:gochecknoglobals // static key table
var inputKeys = map[string]func(t *textInput){
	keyLeft:         func(t *textInput) { t.pos = max(t.pos-1, 0) },
	keyCtrlB:        func(t *textInput) { t.pos = max(t.pos-1, 0) },
	keyRight:        func(t *textInput) { t.pos = min(t.pos+1, len(t.value)) },
	"ctrl+f":        func(t *textInput) { t.pos = min(t.pos+1, len(t.value)) },
	keyHome:         func(t *textInput) { t.pos = 0 },
	keyCtrlA:        func(t *textInput) { t.pos = 0 },
	keyEnd:          func(t *textInput) { t.pos = len(t.value) },
	keyCtrlE:        func(t *textInput) { t.pos = len(t.value) },
	keyBackspace:    (*textInput).backspace,
	"ctrl+h":        (*textInput).backspace,
	keyDelete:       (*textInput).deleteForward,
	keyCtrlD:        (*textInput).deleteForward,
	"ctrl+u":        func(t *textInput) { t.value = t.value[t.pos:]; t.pos = 0 },
	"ctrl+k":        func(t *textInput) { t.value = t.value[:t.pos] },
	"ctrl+w":        (*textInput).deleteWord,
	"alt+backspace": (*textInput).deleteWord,
}

func (t *textInput) key(msg tea.KeyPressMsg) bool {
	key := msg.String()

	if fn, ok := inputKeys[key]; ok {
		t.collapseSelection(key, false)
		fn(t)

		return true
	}

	k := msg.Key()

	if k.Text == "" || k.Mod&(tea.ModCtrl|tea.ModAlt) != 0 {
		return false
	}

	t.collapseSelection(key, true)
	t.insert(k.Text)

	return true
}

func (t *textInput) backspace() {
	if t.pos > 0 {
		t.value = append(t.value[:t.pos-1], t.value[t.pos:]...)
		t.pos--
	}
}

func (t *textInput) deleteForward() {
	if t.pos < len(t.value) {
		t.value = append(t.value[:t.pos], t.value[t.pos+1:]...)
	}
}

// deleteWord removes the word before the cursor; '/' counts as a boundary so path
// segments can be deleted one at a time.
func (t *textInput) deleteWord() {
	start := t.pos

	for start > 0 && unicode.IsSpace(t.value[start-1]) {
		start--
	}

	for start > 0 && !unicode.IsSpace(t.value[start-1]) && t.value[start-1] != '/' {
		start--
	}

	if start == t.pos && start > 0 {
		start--
	}

	t.value = append(t.value[:start], t.value[t.pos:]...)
	t.pos = start
}

func (t *textInput) insert(s string) {
	r := []rune(s)
	v := make([]rune, 0, len(t.value)+len(r))

	v = append(v, t.value[:t.pos]...)
	v = append(v, r...)
	v = append(v, t.value[t.pos:]...)

	t.value = v
	t.pos += len(r)
}

// render draws the value in w cells with a block cursor, scrolling horizontally so the
// cursor stays visible.
func (t *textInput) render(w int, base, cursor lipgloss.Style) string {
	runes := t.value

	if t.mask {
		runes = []rune(strings.Repeat("•", len(t.value)))
	}

	start := 0

	if t.pos >= w {
		start = t.pos - w + 1
	}

	var sb strings.Builder

	for i := start; i < len(runes) && ansi.StringWidth(sb.String()) < w; i++ {
		ch := string(runes[i])

		if i == t.pos || t.selected {
			sb.WriteString(cursor.Render(ch))

			continue
		}

		sb.WriteString(base.Render(ch))
	}

	if t.pos >= len(runes) {
		sb.WriteString(cursor.Render(" "))
	}

	out := sb.String()

	if pad := w - ansi.StringWidth(out); pad > 0 {
		out += base.Render(strings.Repeat(" ", pad))
	}

	return out
}
