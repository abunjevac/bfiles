package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// armDelay is how long a dialog that opened on its own (a job conflict, an SSH
// prompt) ignores keys, so keystrokes meant for something else cannot answer it.
const armDelay = 400 * time.Millisecond

// armed reports whether a dialog opened asynchronously may take keys yet.
func armed(since time.Time) bool {
	return since.IsZero() || timeNow().Sub(since) >= armDelay
}

// inputDialog asks for one line of text.
type inputDialog struct {
	opened   time.Time // set for dialogs that open on their own; see armDelay
	title    string
	label    string
	input    textInput
	hint     string
	width    int
	onSubmit func(m *Model, value string) tea.Cmd
	onCancel func(m *Model) tea.Cmd
}

func (d *inputDialog) place() placement { return placeCenter }

func (d *inputDialog) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	if !armed(d.opened) {
		return false, nil
	}

	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch key := k.String(); {
		case isCancel(key) && d.onCancel != nil:
			return true, d.onCancel(m)
		case isCancel(key):
			return true, nil
		case key == keyEnter:
			return true, d.onSubmit(m, d.input.String())
		}
	}

	d.input.update(msg)

	return false, nil
}

func (d *inputDialog) view(m *Model, w, _ int) string {
	bw := boxWidth(w, max(d.width, 60))
	inner := bw - 4

	lines := []string{}

	if d.label != "" {
		for l := range strings.SplitSeq(d.label, "\n") {
			lines = append(lines, m.st.boxText.Render(l))
		}

		lines = append(lines, "")
	}

	lines = append(lines, d.input.render(inner, m.st.boxInput, m.st.segAccent), "")

	hint := m.keyHint("⏎", "ok", "Esc", "cancel")

	if d.hint != "" {
		hint = m.st.boxMuted.Render(d.hint) + m.st.boxMuted.Render("  ") + hint
	}

	lines = append(lines, hint)

	return m.renderBox(d.title, lines, bw)
}

// checkbox renders a check mark box.
func checkbox(on bool) string {
	if on {
		return "[x]"
	}

	return "[ ]"
}

// confirmDialog offers a set of buttons. Each button's first letter is a hotkey.
type confirmDialog struct {
	opened   time.Time // set for dialogs that open on their own; see armDelay
	title    string
	lines    []string
	buttons  []string
	sel      int
	cancel   int // index chosen by Esc; -1 disables Esc
	danger   bool
	check    string // optional checkbox label toggled with "a"
	checked  bool
	onChoice func(m *Model, choice int, checked bool) tea.Cmd
}

func (d *confirmDialog) place() placement { return placeCenter }

func (d *confirmDialog) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok || !armed(d.opened) {
		return false, nil
	}

	key := k.String()

	if d.moveSel(key) {
		return false, nil
	}

	if key == keyEnter {
		return true, d.onChoice(m, d.sel, d.checked)
	}

	if isCancel(key) {
		if d.cancel < 0 {
			return false, nil
		}

		return true, d.onChoice(m, d.cancel, d.checked)
	}

	if d.check != "" && (key == keySpace || key == "a") {
		d.checked = !d.checked

		return false, nil
	}

	return d.hotkey(m, key)
}

// moveSel moves between buttons and reports whether key was a movement key.
func (d *confirmDialog) moveSel(key string) bool {
	switch key {
	case keyLeft, "h", keyShiftTab:
		d.sel = (d.sel + len(d.buttons) - 1) % len(d.buttons)
	case keyRight, "l", keyTab:
		d.sel = (d.sel + 1) % len(d.buttons)
	default:
		return false
	}

	return true
}

// hotkey chooses the button whose first letter matches key.
func (d *confirmDialog) hotkey(m *Model, key string) (bool, tea.Cmd) {
	for i, b := range d.buttons {
		if strings.EqualFold(b[:1], key) {
			return true, d.onChoice(m, i, d.checked)
		}
	}

	return false, nil
}

func (d *confirmDialog) view(m *Model, w, _ int) string {
	bw := boxWidth(w, 64)
	lines := []string{}

	for _, l := range d.lines {
		for part := range strings.SplitSeq(l, "\n") {
			lines = append(lines, m.st.boxText.Render(part))
		}
	}

	lines = append(lines, "")

	if d.check != "" {
		lines = append(lines, m.st.boxKey.Render("a")+m.st.boxText.Render(" "+checkbox(d.checked)+" "+d.check), "")
	}

	var buttons []string

	for i, b := range d.buttons {
		label := b

		if i == d.sel {
			st := m.st.boxButtonA
			if d.danger && i != d.cancel {
				st = m.st.segError.Padding(0, 1)
			}

			buttons = append(buttons, st.Render(label))
		} else {
			buttons = append(buttons, m.st.boxButton.Render(label))
		}
	}

	lines = append(lines, strings.Join(buttons, m.st.boxText.Render(" ")))

	return m.renderBox(d.title, lines, bw)
}
