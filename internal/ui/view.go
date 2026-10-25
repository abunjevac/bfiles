package ui

import (
	"fmt"
	"path"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/icons"
	"github.com/abunjevac/bfiles/internal/jobs"
	"github.com/abunjevac/bfiles/internal/keymap"
	"github.com/abunjevac/bfiles/internal/location"
	"github.com/abunjevac/bfiles/internal/preview"
	"github.com/abunjevac/bfiles/internal/theme"
	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/archive"
)

// View implements tea.Model.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())

	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "bfiles " + m.title()

	if m.theme.Bg != theme.DefaultColor {
		v.BackgroundColor = m.st.bg
	}

	if m.theme.Fg != theme.DefaultColor {
		v.ForegroundColor = m.st.fg
	}

	return v
}

func (m *Model) title() string {
	p := m.panel()

	if p.fs == nil {
		return ""
	}

	return m.locationText(p)
}

func (m *Model) render() string {
	if m.width < 20 || m.height < 5 {
		return "terminal too small"
	}

	if o := m.top(); o != nil && o.place() == placeFull {
		return o.view(m, m.width, m.height)
	}

	base := m.renderBase()
	layers := m.floatingLayers()

	if len(layers) == 0 {
		return base
	}

	return lipgloss.NewCompositor(append([]*lipgloss.Layer{lipgloss.NewLayer(base)}, layers...)...).Render()
}

// floatingLayers positions which-key, the queue card, dialogs and toasts over the panels.
func (m *Model) floatingLayers() []*lipgloss.Layer {
	var layers []*lipgloss.Layer

	add := func(content string, x, y, z int) {
		layers = append(layers, lipgloss.NewLayer(content).X(max(x, 0)).Y(max(y, 0)).Z(z))
	}

	statusY := m.height - 1

	if m.whichKey && len(m.seq) > 0 {
		wk := m.renderWhichKey()
		add(wk, 1, statusY-lipgloss.Height(wk), 2)
	}

	if m.queueOpen {
		q := m.renderQueue()
		add(q, m.width-lipgloss.Width(q)-1, statusY-lipgloss.Height(q), 3)
	}

	for i, o := range m.overlays {
		content := o.view(m, m.width, m.height)
		y := (m.height - lipgloss.Height(content)) / 3

		if o.place() == placeTop {
			y = 1
		}

		add(content, (m.width-lipgloss.Width(content))/2, y, 10+i)
	}

	if len(m.toasts) > 0 {
		t := m.renderToasts()
		add(t, m.width-lipgloss.Width(t)-1, 1, 50)
	}

	return layers
}

func (m *Model) panelWidths() (int, int) {
	lw := (m.width - 1) / 2

	return lw, m.width - 1 - lw
}

func (m *Model) renderBase() string {
	lw, rw := m.panelWidths()
	h := m.height - 1

	var left, right []string

	if m.quickView {
		if m.active == 0 {
			left, right = m.renderPanel(0, lw, h), m.renderQuickView(rw, h)
		} else {
			left, right = m.renderQuickView(lw, h), m.renderPanel(1, rw, h)
		}
	} else {
		left, right = m.renderPanel(0, lw, h), m.renderPanel(1, rw, h)
	}

	div := m.st.divider.Render("│")
	rows := make([]string, 0, m.height)

	for i := range h {
		rows = append(rows, left[i]+div+right[i])
	}

	rows = append(rows, m.renderStatus())

	return strings.Join(rows, "\n")
}

// locationText describes where a panel is, for headers and the window title.
func (m *Model) locationText(p *Panel) string {
	if p.fs == nil {
		return "…"
	}

	if p.search != nil {
		return fmt.Sprintf("search %q in %s", p.search.opts.Pattern, m.placeText(p.fs, p.search.root))
	}

	return m.placeText(p.fs, p.dir)
}

func (m *Model) placeText(f vfs.FS, dir string) string {
	if f.Kind() == vfs.KindLocal {
		return location.AbbrevHome(dir)
	}

	if a, ok := f.(*archive.FS); ok {
		outer, file := a.Outer()

		return m.placeText(outer, file) + " ▸ " + dir
	}

	return f.Label() + ":" + dir
}

func backendGlyph(f vfs.FS, style string, searching bool) string {
	if style == config.IconsNone || f == nil {
		return ""
	}

	if style == config.IconsUnicode {
		return "▣ "
	}

	switch {
	case searching:
		return icons.BackendSearch + " "
	case f.Kind() == vfs.KindSSH:
		return icons.BackendSSH + " "
	case f.Kind() == vfs.KindArchive:
		return icons.BackendArchive + " "
	}

	return icons.BackendLocal + " "
}

func (m *Model) renderHeader(p *Panel, w int, active bool) string {
	st := m.st.headIdle
	if active {
		st = m.st.headActive
	}

	badges := panelBadges(p)

	if !p.listedAt.IsZero() {
		badges = append(badges, listedTime(p.listedAt, timeNow()))
	}

	if hint := m.closeHint(p); hint != "" {
		badges = append(badges, hint)
	}

	right := ""

	if len(badges) > 0 {
		right = strings.Join(badges, "  ") + " "
	}

	left := " " + backendGlyph(p.fs, m.cfg.Icons, p.search != nil)
	locW := w - ansi.StringWidth(left) - ansi.StringWidth(right) - 1

	return st.Render(left + fitLeft(m.locationText(p), locW) + " " + right)
}

// listedTime shows when a listing was read: the time of day, with the date when it
// is not from today.
func listedTime(t, now time.Time) string {
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04:05")
	}

	return t.Format("Jan _2 15:04")
}

// closeHint tells how to leave a remote, archive or search panel.
func (m *Model) closeHint(p *Panel) string {
	if p.fs == nil || (p.fs.Kind() == vfs.KindLocal && p.search == nil) {
		return ""
	}

	keys := m.km.Keys(keymap.Disconnect)
	if len(keys) == 0 {
		return ""
	}

	return "⏏ " + keymap.DisplaySequence(keys[0])
}

// panelBadges are the small state indicators on the right of a panel header.
func panelBadges(p *Panel) []string {
	var badges []string

	if p.loading {
		badges = append(badges, spinner())
	}

	if p.search != nil && p.search.running {
		badges = append(badges, fmt.Sprintf("%d found %s", len(p.search.results), spinner()))
	}

	return append(badges, viewBadges(p)...)
}

// viewBadges show filter, sort and visibility settings that differ from the default.
func viewBadges(p *Panel) []string {
	var badges []string

	if p.filter != "" || p.filtering {
		badges = append(badges, "/"+p.filter)
	}

	if p.sortKey != config.SortName || p.sortRev {
		arrow := "↑"
		if p.sortRev {
			arrow = "↓"
		}

		badges = append(badges, p.sortKey+arrow)
	}

	flags := []struct {
		on    bool
		badge string
	}{{p.showHidden, ".*"}, {p.showSpecial, "◆"}, {p.exactSizes, "bytes"}}

	for _, f := range flags {
		if f.on {
			badges = append(badges, f.badge)
		}
	}

	return badges
}

func (m *Model) renderPanel(i, w, h int) []string {
	p := m.panels[i]
	active := i == m.active && !m.queueFocus

	rows := make([]string, 0, h)
	rows = append(rows, m.renderHeader(p, w, i == m.active))

	listH := h - 1

	if p.filtering {
		listH--
	}

	p.rows = listH

	p.clamp()

	now := timeNow()

	for r := range listH {
		idx := p.offset + r

		if idx >= len(p.view) {
			if idx == 0 && r == 0 && p.fs != nil && !p.loading {
				rows = append(rows, m.st.muted.Render(fit("   (empty)", w)))

				continue
			}

			rows = append(rows, strings.Repeat(" ", w))

			continue
		}

		rows = append(rows, m.renderRow(p, p.view[idx], w, idx == p.cursor, active, now))
	}

	if p.filtering {
		in := newInput(p.filter)
		rows = append(rows, m.st.accent.Render(" / ")+in.render(w-3, m.st.text, m.st.segAccent))
	}

	return rows
}

// painter applies a row's cursor background to every segment, since nested lipgloss
// renders would otherwise reset it.
type painter struct {
	bg *lipgloss.Style
}

func (pt painter) paint(st lipgloss.Style, s string) string {
	if pt.bg != nil {
		st = st.Background(pt.bg.GetBackground())
	}

	return st.Render(s)
}

func (m *Model) rowPainter(cursor, active bool) painter {
	if !cursor {
		return painter{}
	}

	st := m.st.cursor
	if !active {
		st = m.st.cursorDim
	}

	return painter{bg: &st}
}

func (m *Model) renderRow(p *Panel, e vfs.Entry, w int, cursor, active bool, now time.Time) string {
	glyph, role := icons.For(path.Base(e.Name), e.IsDir, e.IsLink, e.IsExec(), m.cfg.Icons)
	sel := p.selected[e.Path]
	pt := m.rowPainter(cursor, active)

	nameStyle, mark := m.st.roles[role], "  "
	if sel {
		nameStyle, mark = m.st.selected, "✓ "
	}

	iconPart := ""
	if glyph != "" {
		iconPart = glyph + " "
	}

	sizeW := 8
	if p.exactSizes {
		sizeW = 15
	}

	timeW := 0
	if p.showMtime && w >= 56 {
		timeW = 14
	}

	nameW := max(w-2-ansi.StringWidth(iconPart)-sizeW-timeW-1, 4)

	name := e.Name
	if e.IsDir && m.cfg.Icons == config.IconsNone && !isParent(e) {
		name += "/"
	}

	var out strings.Builder

	out.WriteString(pt.paint(m.st.selected, mark))
	out.WriteString(pt.paint(nameStyle, iconPart+fit(name, nameW)))
	out.WriteString(pt.paint(m.st.muted, " "))
	out.WriteString(m.renderSizeCell(p, e, sizeW, pt.paint))

	if timeW > 0 {
		out.WriteString(pt.paint(m.st.muted, padRight(shortTime(e.ModTime, now), timeW)))
	}

	return out.String()
}

func (m *Model) renderSizeCell(p *Panel, e vfs.Entry, w int, paint func(lipgloss.Style, string) string) string {
	switch {
	case isParent(e):
		return paint(m.st.muted, padRight("", w))
	case e.IsDir:
		s, ok := m.sizes[sizeKey(p.fs, e.Path)]

		switch {
		case ok && s.running:
			return paint(m.st.warnText, padRight(spinner(), w))
		case ok && s.err == nil:
			return paint(m.st.accent, padRight(p.sizeText(s.bytes), w))
		default:
			return paint(m.st.muted, padRight("<DIR>", w))
		}
	default:
		return paint(m.st.text, padRight(p.sizeText(e.Size), w))
	}
}

// spinner is a time-based frame, so any redraw advances it.
func spinner() string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

	return frames[(timeNow().UnixMilli()/100)%int64(len(frames))]
}

func (m *Model) renderQuickView(w, h int) []string {
	title := " preview"

	if e, ok := m.panel().current(); ok {
		title += " · " + e.Name
	}

	if m.previewRes != nil && m.previewRes.Title != "" {
		title += " · " + m.previewRes.Title
	}

	rows := make([]string, 0, h)
	rows = append(rows, m.st.headIdle.Render(fit(title, w)))
	body := m.previewBody(w)

	for i := range h - 1 {
		if i < len(body) {
			rows = append(rows, fit(body[i], w))
		} else {
			rows = append(rows, strings.Repeat(" ", w))
		}
	}

	return rows
}

func (m *Model) previewBody(w int) []string {
	r := m.previewRes

	switch {
	case r == nil:
		return []string{m.st.muted.Render("  " + spinner())}
	case r.Kind == preview.KindEntries:
		tmp := &Panel{selected: map[string]bool{}}
		now := timeNow()
		body := make([]string, 0, len(r.Entries))

		for _, e := range r.Entries {
			body = append(body, m.renderRow(tmp, e, w, false, false, now))
		}

		return body
	case r.Kind == preview.KindError:
		return []string{m.st.errorText.Render("  " + r.Err.Error())}
	case r.Kind == preview.KindInfo:
		return []string{m.st.muted.Render("  " + r.Title)}
	}

	body := make([]string, 0, len(r.Lines))

	for _, l := range r.Lines {
		body = append(body, " "+l)
	}

	return body
}

func (m *Model) renderStatus() string {
	st := m.st
	p := m.panel()

	left := st.segAccent.Render(m.modeLabel())
	right := m.jobsSegment()
	count := st.status.Render(fmt.Sprintf(" %d items ", max(len(p.view)-btoi(!p.atRoot() && p.search == nil), 0)))

	help := ""
	if keys := m.km.Keys(keymap.Help); len(keys) > 0 {
		help = st.statusKey.Render(" "+keymap.DisplaySequence(keys[0])) + st.status.Render(" help ")
	}

	fixed := ansi.StringWidth(left) + ansi.StringWidth(right) + ansi.StringWidth(count) + ansi.StringWidth(help)
	infoW := max(m.width-fixed, 0)

	return left + st.status.Render(fit(m.statusInfo(p), infoW)) + help + count + right
}

func (m *Model) modeLabel() string {
	p := m.panel()

	switch {
	case len(m.seq) > 0:
		return " " + keymap.DisplaySequence(strings.Join(m.seq, " ")) + " "
	case p.filtering:
		return " FILTER "
	case m.queueFocus:
		return " QUEUE "
	case p.search != nil:
		return " SEARCH "
	}

	return " NORMAL "
}

// statusInfo describes the selection, or else the entry under the cursor.
func (m *Model) statusInfo(p *Panel) string {
	st := m.st

	if n := p.selectedVisible(); n > 0 {
		total := m.selectionSize(p)

		return st.selected.Background(st.status.GetBackground()).Render(fmt.Sprintf(" %d selected · %s ", n, humanSize(total)))
	}

	e, ok := p.current()
	if !ok || isParent(e) {
		return ""
	}

	parts := []string{e.Name}

	if !e.IsDir {
		parts = append(parts, humanSize(e.Size))
	}

	parts = append(parts, permString(e.Mode), e.ModTime.Format("2006-01-02 15:04"))

	if e.IsLink {
		parts = append(parts, "→ "+e.Link)
	}

	return st.status.Render(" " + strings.Join(parts, "  ") + " ")
}

// selectionSize sums selected files plus the known sizes of selected directories.
func (m *Model) selectionSize(p *Panel) int64 {
	var total int64

	known := m.knownSize(p.fs)

	for _, e := range p.targets() {
		if !e.IsDir {
			total += e.Size

			continue
		}

		if s, ok := known(e); ok {
			total += s
		}
	}

	return total
}

func btoi(b bool) int {
	if b {
		return 1
	}

	return 0
}

func (m *Model) jobsSegment() string {
	running, waiting, failed := 0, 0, 0

	var done, total int64

	for _, j := range m.jobInfos {
		switch j.State { //nolint:exhaustive // finished jobs other than failures are not shown
		case jobs.Running:
			running++
			done += j.Done
			total += j.Total
		case jobs.Waiting:
			waiting++
		case jobs.Failed:
			failed++
		}
	}

	switch {
	case running+waiting > 0:
		pct := ""
		if total > 0 {
			pct = fmt.Sprintf(" %d%%", done*100/total)
		}

		return m.st.segWarn.Render(fmt.Sprintf(" ⚡ %d job%s%s ", running+waiting, plural(running+waiting), pct))
	case failed > 0:
		return m.st.segError.Render(fmt.Sprintf(" ✗ %d failed ", failed))
	}

	return ""
}

func plural(n int) string {
	if n == 1 {
		return ""
	}

	return "s"
}

func (m *Model) renderWhichKey() string {
	hints := m.km.Children(m.seq)
	inner := m.width - 6

	// key cell (3) + arrow (3) + label + gap (2)
	colW := 0

	for _, h := range hints {
		colW = max(colW, 3+3+ansi.StringWidth(h.Label)+2)
	}

	colW = min(max(colW, 18), inner)
	cols := max(inner/colW, 1)
	nrows := (len(hints) + cols - 1) / cols

	lines := make([]string, 0, nrows)

	for r := range nrows {
		var sb strings.Builder

		for c := range cols {
			i := c*nrows + r

			if i >= len(hints) {
				break
			}

			h := hints[i]
			label := m.st.boxText.Render(h.Label)

			if h.Group {
				label = m.st.boxTitle.Render(h.Label)
			}

			cell := m.st.boxKey.Render(fit(keymap.Display(h.Key), 3)) + m.st.boxMuted.Render(" → ") + label

			sb.WriteString(padBox(cell, colW, m.st.boxText))
		}

		lines = append(lines, sb.String())
	}

	title := keymap.DisplaySequence(strings.Join(m.seq, " "))

	if g := m.km.GroupLabel(m.seq); g != "" {
		title += "  " + g
	}

	return m.renderBox(title, lines, m.width-2)
}

func (m *Model) renderToasts() string {
	out := make([]string, 0, len(m.toasts))

	for _, t := range m.toasts {
		icon, st := " ", m.st.okText

		switch t.level {
		case levelWarn:
			icon, st = " ", m.st.warnText
		case levelError:
			icon, st = " ", m.st.errorText
		case levelInfo:
		}

		w := min(max(ansi.StringWidth(t.text)+8, 24), m.width/2)
		box := m.st.box.BorderForeground(st.GetForeground()).Width(w)
		lines := wrap(t.text, w-6)

		for i, l := range lines {
			if i == 0 {
				lines[i] = st.Background(m.st.overlay).Render(icon) + m.st.boxText.Render(l)
			} else {
				lines[i] = m.st.boxText.Render("   " + l)
			}
		}

		out = append(out, box.Render(strings.Join(lines, "\n")))
	}

	return lipgloss.JoinVertical(lipgloss.Right, out...)
}

// wrap breaks text into lines of at most w cells.
func wrap(s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}

	return strings.Split(ansi.Wrap(s, w, " /"), "\n")
}
