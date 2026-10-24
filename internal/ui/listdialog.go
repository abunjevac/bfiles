package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"
)

// listItem is one row of a listDialog.
type listItem struct {
	text  string
	hint  string // shown dimmed on the right
	icon  string
	value string // what the item stands for (a path, a target, an action name)
}

// listDialog is a filterable picker.
type listDialog struct {
	title     string
	items     []listItem
	filter    textInput
	shown     []int // indexes into items after filtering
	sel       int
	offset    int
	width     int
	rows      int
	footer    []string // key hint pairs
	allowFree bool     // Enter with no match submits the typed text

	onSelect func(m *Model, it listItem, typed string) tea.Cmd
	onMove   func(m *Model, it listItem)
	onCancel func(m *Model) tea.Cmd
	// onKey handles extra keys (e.g. delete a history entry); handled stops processing.
	onKey func(m *Model, d *listDialog, key string) (handled bool, cmd tea.Cmd)
}

func (d *listDialog) place() placement { return placeCenter }

func (d *listDialog) refilter() {
	q := d.filter.String()
	d.shown = d.shown[:0]

	if q == "" {
		for i := range d.items {
			d.shown = append(d.shown, i)
		}
	} else {
		texts := make([]string, len(d.items))

		for i, it := range d.items {
			texts[i] = it.text + " " + it.hint
		}

		for _, match := range fuzzy.Find(q, texts) {
			d.shown = append(d.shown, match.Index)
		}
	}

	d.sel = min(d.sel, max(len(d.shown)-1, 0))
	d.offset = 0
}

func (d *listDialog) current() (listItem, bool) {
	if d.sel < 0 || d.sel >= len(d.shown) {
		return listItem{}, false
	}

	return d.items[d.shown[d.sel]], true
}

func (d *listDialog) moveSel(m *Model, delta int) {
	if len(d.shown) == 0 {
		return
	}

	d.sel = max(min(d.sel+delta, len(d.shown)-1), 0)

	d.notifyMove(m)
}

// listMove is a cursor movement: step rows, or step pages when page is set.
type listMove struct {
	step int
	page bool
}

//nolint:gochecknoglobals // static key table
var listMoves = map[string]listMove{
	keyUp: {step: -1}, "ctrl+p": {step: -1}, "ctrl+k": {step: -1},
	keyDown: {step: 1}, "ctrl+n": {step: 1}, "ctrl+j": {step: 1},
	keyPgUp: {step: -1, page: true}, keyPgDown: {step: 1, page: true},
}

func (d *listDialog) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	if d.shown == nil {
		d.refilter()
	}

	if k, ok := msg.(tea.KeyPressMsg); ok {
		if done, cmd, handled := d.key(m, k.String()); handled {
			return done, cmd
		}
	}

	before := d.filter.String()

	if d.filter.update(msg) && d.filter.String() != before {
		d.refilter()
		d.notifyMove(m)
	}

	return false, nil
}

// key handles non-editing keys. It returns whether the dialog is done, its command,
// and whether the key was handled at all (unhandled keys go to the filter).
func (d *listDialog) key(m *Model, key string) (bool, tea.Cmd, bool) {
	if d.onKey != nil {
		if ok, c := d.onKey(m, d, key); ok {
			return false, c, true
		}
	}

	if mv, ok := listMoves[key]; ok {
		delta := mv.step

		if mv.page {
			delta *= d.rows
		}

		d.moveSel(m, delta)

		return false, nil, true
	}

	switch {
	case isCancel(key) && d.onCancel != nil:
		return true, d.onCancel(m), true
	case isCancel(key):
		return true, nil, true
	case key == keyEnter:
		return d.choose(m)
	}

	return false, nil, false
}

func (d *listDialog) choose(m *Model) (bool, tea.Cmd, bool) {
	it, ok := d.current()
	typed := d.filter.String()

	if !ok && (!d.allowFree || typed == "") {
		return false, nil, true
	}

	return true, d.onSelect(m, it, typed), true
}

func (d *listDialog) notifyMove(m *Model) {
	if d.onMove == nil {
		return
	}

	if it, ok := d.current(); ok {
		d.onMove(m, it)
	}
}

func (d *listDialog) view(m *Model, w, h int) string {
	if d.shown == nil {
		d.refilter()
	}

	bw := boxWidth(w, max(d.width, 64))
	inner := bw - 4
	d.rows = max(min(h-10, 16), 3)

	if d.sel < d.offset {
		d.offset = d.sel
	}

	if d.sel >= d.offset+d.rows {
		d.offset = d.sel - d.rows + 1
	}

	lines := make([]string, 0, d.rows+4)
	lines = append(lines, m.st.boxMuted.Render(" ")+d.filter.render(inner-2, m.st.boxInput, m.st.segAccent), "")
	lines = append(lines, d.renderItems(m, inner)...)

	if len(d.shown) == 0 {
		lines[2] = m.st.boxMuted.Render(" " + d.emptyMessage())
	}

	count := m.st.boxMuted.Render(itoa(len(d.shown)) + "/" + itoa(len(d.items)))
	footer := append([]string{"⏎", "select", "Esc", "close"}, d.footer...)

	lines = append(lines, "", count+m.st.boxMuted.Render("  ")+m.keyHint(footer...))

	return m.renderBox(d.title, lines, bw)
}

func (d *listDialog) renderItems(m *Model, inner int) []string {
	lines := make([]string, 0, d.rows)

	for row := range d.rows {
		i := d.offset + row

		if i >= len(d.shown) {
			lines = append(lines, "")

			continue
		}

		it := d.items[d.shown[i]]
		text := it.text

		if it.icon != "" {
			text = it.icon + " " + text
		}

		hintW := min(len([]rune(it.hint))+1, inner/2)
		left := fit(" "+text, inner-hintW)
		right := padRight(it.hint+" ", hintW)

		if i == d.sel {
			lines = append(lines, m.st.boxCursor.Render(left)+m.st.boxCursor.Render(right))
		} else {
			lines = append(lines, m.st.boxText.Render(left)+m.st.boxMuted.Render(right))
		}
	}

	return lines
}

func (d *listDialog) emptyMessage() string {
	switch {
	case d.allowFree && d.filter.String() != "":
		return "⏎ to use the typed text"
	case d.allowFree:
		return "type a target, e.g. user@host:/path"
	}

	return "no matches"
}
