package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/abunjevac/bfiles/internal/preview"
	"github.com/abunjevac/bfiles/internal/vfs"
)

// previewDelay debounces quick view while the cursor moves quickly.
const previewDelay = 60 * time.Millisecond

func (m *Model) previewRequest(f vfs.FS, e vfs.Entry, w, h int) preview.Request {
	return preview.Request{
		FS:       f,
		Entry:    e,
		Width:    w,
		Height:   h,
		Style:    preview.StyleFor(m.theme.Name, m.theme.Dark),
		MaxBytes: int64(m.cfg.Preview.MaxBytes),
		Rules:    m.cfg.Preview.Rules,
	}
}

// schedulePreview refreshes quick view after a short pause in cursor movement.
func (m *Model) schedulePreview() tea.Cmd {
	if !m.quickView {
		return nil
	}

	p := m.panel()

	e, ok := p.current()
	if !ok || p.fs == nil {
		return nil
	}

	key := sizeKey(p.fs, e.Path)

	if key == m.previewFor {
		return nil
	}

	m.previewID++
	id := m.previewID

	return tea.Tick(previewDelay, func(time.Time) tea.Msg { return previewTickMsg{id: id} })
}

func (m *Model) startPreview(id int) tea.Cmd {
	if id != m.previewID || !m.quickView {
		return nil
	}

	p := m.panel()

	e, ok := p.current()
	if !ok {
		return nil
	}

	if m.previewCnl != nil {
		m.previewCnl()
	}

	ctx, cancel := context.WithCancel(m.ctx)
	m.previewCnl = cancel
	m.previewFor = sizeKey(p.fs, e.Path)
	m.previewRes = nil

	_, rw := m.panelWidths()
	req := m.previewRequest(p.fs, e, rw-2, m.height-2)
	held := m.hold(p.fs)

	return func() tea.Msg {
		defer cancel()

		return previewMsg{id: id, path: e.Path, result: preview.Render(ctx, req), held: held}
	}
}

func (m *Model) applyPreview(msg previewMsg) tea.Cmd {
	for _, f := range msg.held {
		m.release(f)
	}

	if msg.id == m.previewID {
		m.previewRes = &msg.result
	}

	return nil
}

// viewerLoadedMsg delivers content to the full-screen viewer.
type viewerLoadedMsg struct {
	viewer *viewer
	result preview.Result
	held   []vfs.FS
}

// effect releases the held filesystem and loads the viewer if it is still open.
func (msg viewerLoadedMsg) effect(m *Model) {
	for _, f := range msg.held {
		m.release(f)
	}

	if m.isOpen(msg.viewer) {
		msg.viewer.load(msg)
	}
}

// viewer shows a file full screen with scrolling and search.
type viewer struct {
	name      string
	res       *preview.Result
	plain     []string // lines without colour, for search
	offset    int
	xoff      int
	searching bool
	query     textInput
	match     int
	cancel    context.CancelFunc
}

func (m *Model) openViewer() tea.Cmd {
	p := m.panel()

	e, ok := p.current()
	if !ok || isParent(e) {
		return nil
	}

	ctx, cancel := context.WithCancel(m.ctx)
	v := &viewer{name: e.Name, cancel: cancel, match: -1}

	m.push(v)

	req := m.previewRequest(p.fs, e, m.width-2, m.height-2)
	req.MaxLines = 100000
	req.MaxBytes = max(int64(m.cfg.Preview.MaxBytes), 16<<20)
	held := m.hold(p.fs)

	return func() tea.Msg {
		defer cancel()

		return viewerLoadedMsg{viewer: v, result: preview.Render(ctx, req), held: held}
	}
}

func (v *viewer) place() placement { return placeFull }

func (v *viewer) pageRows(m *Model) int {
	return max(m.height-2, 1)
}

func (v *viewer) lineCount() int {
	if v.res == nil {
		return 0
	}

	if v.res.Kind == preview.KindEntries {
		return len(v.res.Entries)
	}

	return len(v.res.Lines)
}

func (v *viewer) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}

	if v.searching {
		v.searchKey(k)

		return false, nil
	}

	key := k.String()

	if key == "q" || key == "f3" || isCancel(key) {
		v.cancel()

		return true, nil
	}

	v.scrollKey(key, v.pageRows(m))

	return false, nil
}

func (v *viewer) load(loaded viewerLoadedMsg) {
	v.res = &loaded.result
	v.plain = make([]string, len(v.res.Lines))

	for i, l := range v.res.Lines {
		v.plain[i] = strings.ToLower(ansi.Strip(l))
	}
}

func (v *viewer) searchKey(k tea.KeyPressMsg) {
	switch k.String() {
	case keyEnter:
		v.searching = false
		v.find(1, true)
	case keyEsc:
		v.searching = false
	default:
		v.query.update(k)
	}
}

// viewerScroll maps keys to a scroll amount computed from the page height.
//
//nolint:gochecknoglobals // static key table
var viewerScroll = map[string]func(rows, offset, last int) int{
	"j":       func(_, o, _ int) int { return o + 1 },
	keyDown:   func(_, o, _ int) int { return o + 1 },
	keyEnter:  func(_, o, _ int) int { return o + 1 },
	"k":       func(_, o, _ int) int { return o - 1 },
	keyUp:     func(_, o, _ int) int { return o - 1 },
	keyPgDown: func(r, o, _ int) int { return o + r - 1 },
	keySpace:  func(r, o, _ int) int { return o + r - 1 },
	"f":       func(r, o, _ int) int { return o + r - 1 },
	"ctrl+f":  func(r, o, _ int) int { return o + r - 1 },
	keyPgUp:   func(r, o, _ int) int { return o - r + 1 },
	"b":       func(r, o, _ int) int { return o - r + 1 },
	keyCtrlB:  func(r, o, _ int) int { return o - r + 1 },
	keyCtrlD:  func(r, o, _ int) int { return o + r/2 },
	"ctrl+u":  func(r, o, _ int) int { return o - r/2 },
	"g":       func(_, _, _ int) int { return 0 },
	keyHome:   func(_, _, _ int) int { return 0 },
	"G":       func(_, _, l int) int { return l },
	keyEnd:    func(_, _, l int) int { return l },
}

func (v *viewer) scrollKey(key string, rows int) {
	last := max(v.lineCount()-rows, 0)

	switch key {
	case "l", keyRight:
		v.xoff += 8
	case "h", keyLeft:
		v.xoff = max(v.xoff-8, 0)
	case "/":
		v.searching = true
		v.query = newInput("")
	case "n":
		v.find(1, false)
	case "N":
		v.find(-1, false)
	default:
		if fn, ok := viewerScroll[key]; ok {
			v.offset = fn(rows, v.offset, last)
		}
	}

	v.offset = max(min(v.offset, last), 0)
}

// find moves to the next (dir 1) or previous (dir -1) line matching the query.
func (v *viewer) find(dir int, fromTop bool) {
	q := strings.ToLower(v.query.String())

	if q == "" || len(v.plain) == 0 {
		return
	}

	start := v.match

	if fromTop {
		start = v.offset - 1
	}

	n := len(v.plain)

	for step := 1; step <= n; step++ {
		i := ((start+dir*step)%n + n) % n

		if strings.Contains(v.plain[i], q) {
			v.match = i
			v.offset = max(i-3, 0)

			return
		}
	}

	v.match = -1
}

func (v *viewer) view(m *Model, w, h int) string {
	st := m.st
	rows := max(h-2, 1)

	title := " " + v.name

	if v.res != nil && v.res.Title != "" {
		title += " · " + v.res.Title
	}

	if n := v.lineCount(); n > 0 {
		title += fmt.Sprintf(" · %d/%d", min(v.offset+rows, n), n)
	}

	if v.res != nil && v.res.Truncated {
		title += " · truncated"
	}

	out := make([]string, 0, rows+2)
	out = append(out, st.headActive.Render(fit(title, w)))
	out = append(out, v.bodyLines(m, w, rows)...)

	var footer string

	if v.searching {
		footer = st.segAccent.Render(" / ") + v.query.render(w-3, st.status, st.segAccent)
	} else {
		hint := " q close  j/k scroll  space page  g/G top/end  h/l pan  / search  n/N next/prev"
		footer = st.status.Render(fit(hint, w))
	}

	out = append(out, footer)

	return strings.Join(out, "\n")
}

func (v *viewer) bodyLines(m *Model, w, rows int) []string {
	var out []string

	switch {
	case v.res == nil:
		out = []string{m.st.muted.Render("  loading " + spinner())}
	case v.res.Kind == preview.KindEntries:
		out = v.entryLines(m, w, rows)
	case v.res.Kind == preview.KindError:
		out = []string{m.st.errorText.Render("  " + v.res.Err.Error())}
	case v.res.Kind == preview.KindInfo:
		out = []string{m.st.muted.Render("  " + v.res.Title)}
	default:
		out = v.textLines(m, w, rows)
	}

	for len(out) < rows {
		out = append(out, strings.Repeat(" ", w))
	}

	return out
}

func (v *viewer) entryLines(m *Model, w, rows int) []string {
	tmp := &Panel{selected: map[string]bool{}}
	now := timeNow()
	end := min(v.offset+rows, len(v.res.Entries))
	out := make([]string, 0, rows)

	for i := v.offset; i < end; i++ {
		out = append(out, m.renderRow(tmp, v.res.Entries[i], w, false, false, now))
	}

	return out
}

func (v *viewer) textLines(m *Model, w, rows int) []string {
	end := min(v.offset+rows, len(v.res.Lines))
	out := make([]string, 0, rows)

	for i := v.offset; i < end; i++ {
		gutter := " "

		if i == v.match {
			gutter = m.st.accent.Render("▌")
		}

		line := v.res.Lines[i]

		if v.xoff > 0 {
			line = ansi.Cut(line, v.xoff, v.xoff+w)
		}

		out = append(out, fit(gutter+line, w))
	}

	return out
}
