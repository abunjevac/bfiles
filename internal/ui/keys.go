package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/keymap"
)

// doubleClick is the maximum interval between clicks of a double click.
const doubleClick = 400 * time.Millisecond

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	// quit keys work everywhere, dialogs included, so leaving is always one key away
	if len(m.seq) == 0 {
		if a, _ := m.km.Lookup([]string{msg.String()}); a == keymap.Quit {
			return m.quit()
		}
	}

	if o := m.top(); o != nil {
		return m.updateOverlay(o, msg)
	}

	if m.panel().filtering {
		return m.filterKey(msg)
	}

	key := msg.String()

	if m.queueFocus && len(m.seq) == 0 {
		if m.queueKey(key) {
			return nil
		}
	}

	if len(m.seq) > 0 && m.editSequence(key) {
		return nil
	}

	return m.sequenceKey(key)
}

// editSequence handles Esc and Backspace while a key sequence is pending.
func (m *Model) editSequence(key string) bool {
	switch {
	case isCancel(key):
		m.resetSeq()
	case key == keyBackspace:
		m.seq = m.seq[:len(m.seq)-1]
		m.seqID++

		if len(m.seq) == 0 {
			m.whichKey = false
		}
	default:
		return false
	}

	return true
}

// sequenceKey extends the pending key sequence and runs the action it completes.
func (m *Model) sequenceKey(key string) tea.Cmd {
	seq := append(append([]string{}, m.seq...), key)
	action, prefix := m.km.Lookup(seq)

	switch {
	case action != "":
		m.resetSeq()

		return m.dispatch(action)
	case prefix:
		m.seq = seq
		m.seqID++

		if m.whichKey {
			return nil
		}

		id := m.seqID

		return tea.Tick(time.Duration(m.cfg.WhichKeyDelay)*time.Millisecond, func(time.Time) tea.Msg {
			return whichKeyTickMsg{seq: id}
		})
	case len(m.seq) > 0:
		m.resetSeq()

		return m.toast("no binding for "+keymap.DisplaySequence(strings.Join(seq, " ")), levelWarn)
	}

	return nil
}

func (m *Model) resetSeq() {
	m.seq = nil
	m.seqID++
	m.whichKey = false
}

// filterKey edits the quick filter; the listing narrows as you type.
func (m *Model) filterKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.panel()

	switch msg.String() {
	case keyEsc, keyCtrlC:
		p.filtering = false
		p.filter = ""

		m.rebuild(m.active)

		return m.schedulePreview()
	case keyEnter:
		p.filtering = false

		return nil
	case keyUp, keyDown, keyPgUp, keyPgDown:
		delta := map[string]int{keyUp: -1, keyDown: 1, keyPgUp: -p.rows, keyPgDown: p.rows}[msg.String()]

		p.move(delta)

		return m.schedulePreview()
	}

	in := newInput(p.filter)

	if !in.update(msg) {
		return nil
	}

	if in.String() == "" && msg.String() == keyBackspace && p.filter == "" {
		p.filtering = false
	}

	p.filter = in.String()

	m.rebuild(m.active)

	return m.schedulePreview()
}

// panelAt maps a screen column to a panel index.
func (m *Model) panelAt(x int) int {
	lw, _ := m.panelWidths()

	if x <= lw {
		return 0
	}

	return 1
}

func (m *Model) handleClick(ms tea.Mouse) tea.Cmd {
	idx, ok := m.clickTarget(ms)
	if !ok {
		return nil
	}

	p := m.panel()
	now := timeNow()
	double := idx == p.cursor && ms.Y == m.lastClickY && now.Sub(m.lastClick) < doubleClick

	m.lastClick, m.lastClickY = now, ms.Y
	p.cursor = idx
	p.clamp()

	switch {
	case ms.Button == tea.MouseRight:
		p.toggle(p.view[idx])

		return nil
	case double:
		return m.enter()
	}

	return m.schedulePreview()
}

// clickTarget focuses the clicked panel and returns the row index under the mouse.
func (m *Model) clickTarget(ms tea.Mouse) (int, bool) {
	if m.top() != nil || ms.Y >= m.height-1 {
		return 0, false
	}

	i := m.panelAt(ms.X)

	if m.quickView && i != m.active {
		return 0, false
	}

	m.active = i
	m.queueFocus = false

	p := m.panel()
	idx := p.offset + ms.Y - 1

	if ms.Y == 0 || idx >= len(p.view) {
		return 0, false
	}

	return idx, true
}

func (m *Model) handleWheel(ms tea.Mouse) tea.Cmd {
	if m.top() != nil {
		return nil
	}

	p := m.panels[m.panelAt(ms.X)]

	switch ms.Button { //nolint:exhaustive // only vertical wheel scrolls
	case tea.MouseWheelUp:
		p.move(-3)
	case tea.MouseWheelDown:
		p.move(3)
	}

	return m.schedulePreview()
}
