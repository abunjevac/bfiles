package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/icons"
)

// completionRows is the fixed height of the suggestion list, so the dialog does not
// change size while typing.
const completionRows = 8

// maxCompletionEntries bounds how much of a huge directory is kept for completion.
const maxCompletionEntries = 5000

// completion is one suggestion: an entry of the directory being typed.
type completion struct {
	name string
	dir  bool
}

// pathListedMsg delivers the entries of a directory for completion.
type pathListedMsg struct {
	dir   string
	items []completion
}

// gotoDialog asks for a location and, for local paths, suggests entries of the
// directory being typed in a list below the input.
type gotoDialog struct {
	input    textInput
	base     string // directory relative paths start from
	complete bool   // suggestions are only offered for local paths

	listedDir  string // directory whose entries are in items
	pendingDir string // directory being listed
	items      []completion
	shown      []completion
	sel        int // -1: nothing chosen, Enter submits the typed text

	onSubmit func(m *Model, value string) tea.Cmd
}

func newGotoDialog(value, base string, complete bool, onSubmit func(m *Model, value string) tea.Cmd) (*gotoDialog, tea.Cmd) {
	d := &gotoDialog{input: newSelectedInput(value), base: base, complete: complete, sel: -1, onSubmit: onSubmit}

	return d, d.refresh()
}

func (d *gotoDialog) place() placement { return placeCenter }

// split separates the typed text into the directory part (kept verbatim, with its
// trailing slash) and the name prefix being completed.
func (d *gotoDialog) split() (string, string) {
	text := d.input.String()

	if i := strings.LastIndex(text, "/"); i >= 0 {
		return text[:i+1], text[i+1:]
	}

	return "", text
}

// absDir resolves the directory part against the home directory or the base.
func (d *gotoDialog) absDir(dirPart string) string {
	switch {
	case dirPart == "":
		return d.base
	case dirPart == "~/" || strings.HasPrefix(dirPart, "~/"):
		home, _ := os.UserHomeDir()

		return filepath.Join(home, dirPart[1:])
	case filepath.IsAbs(dirPart):
		return filepath.Clean(dirPart)
	}

	return filepath.Join(d.base, dirPart)
}

// active reports whether suggestions apply to the current text (not ssh:// targets).
func (d *gotoDialog) active() bool {
	return d.complete && !strings.Contains(d.input.String(), "://")
}

// refresh lists the typed directory when it changed and re-filters suggestions.
func (d *gotoDialog) refresh() tea.Cmd {
	if !d.active() {
		d.shown = nil

		return nil
	}

	dirPart, _ := d.split()
	dir := d.absDir(dirPart)

	d.filter()

	if dir == d.listedDir || dir == d.pendingDir {
		return nil
	}

	d.pendingDir = dir

	return func() tea.Msg { return pathListedMsg{dir: dir, items: listCompletions(dir)} }
}

func listCompletions(dir string) []completion {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	items := make([]completion, 0, min(len(des), maxCompletionEntries))

	for _, de := range des[:min(len(des), maxCompletionEntries)] {
		isDir := de.IsDir()

		if de.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(dir, de.Name())); err == nil {
				isDir = fi.IsDir()
			}
		}

		items = append(items, completion{name: de.Name(), dir: isDir})
	}

	slices.SortFunc(items, func(a, b completion) int {
		if a.dir != b.dir {
			if a.dir {
				return -1
			}

			return 1
		}

		return naturalCompare(a.name, b.name)
	})

	return items
}

// filter keeps entries starting with the typed prefix, then those containing it;
// dotfiles appear only once the prefix starts with a dot.
func (d *gotoDialog) filter() {
	_, prefix := d.split()
	p := strings.ToLower(prefix)

	var starts, contains []completion

	for _, it := range d.items {
		n := strings.ToLower(it.name)

		switch {
		case strings.HasPrefix(it.name, ".") && !strings.HasPrefix(prefix, "."):
		case strings.HasPrefix(n, p):
			starts = append(starts, it)
		case strings.Contains(n, p):
			contains = append(contains, it)
		}
	}

	d.shown = slices.Concat(starts, contains)
	d.sel = min(d.sel, len(d.shown)-1)
}

func (d *gotoDialog) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	if listed, ok := msg.(pathListedMsg); ok {
		d.listed(listed)

		return false, nil
	}

	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, d.edit(msg)
	}

	switch key := k.String(); {
	case isCancel(key):
		return true, nil
	case key == keyEnter:
		if d.sel >= 0 {
			d.accept(d.sel)
		}

		return true, d.onSubmit(m, d.input.String())
	case key == keyTab:
		return false, d.completeTab()
	case d.moveSel(key):
		return false, nil
	}

	return false, d.edit(msg)
}

// listed installs the entries of the directory being typed, unless the text moved on.
func (d *gotoDialog) listed(msg pathListedMsg) {
	if msg.dir != d.pendingDir {
		return
	}

	d.listedDir, d.pendingDir, d.items = msg.dir, "", msg.items
	d.filter()
}

// moveSel moves the suggestion cursor; above the first entry is the typed text.
func (d *gotoDialog) moveSel(key string) bool {
	switch key {
	case keyDown, "ctrl+n":
		d.sel = min(d.sel+1, len(d.shown)-1)
	case keyUp, "ctrl+p":
		d.sel = max(d.sel-1, -1)
	default:
		return false
	}

	return true
}

// edit passes a message to the input and refreshes suggestions if the text changed.
func (d *gotoDialog) edit(msg tea.Msg) tea.Cmd {
	before := d.input.String()

	if !d.input.update(msg) || d.input.String() == before {
		return nil
	}

	d.sel = -1

	return d.refresh()
}

// completeTab takes the chosen suggestion, the only one, or the longest common prefix.
func (d *gotoDialog) completeTab() tea.Cmd {
	switch {
	case d.sel >= 0:
		d.accept(d.sel)
	case len(d.shown) == 1:
		d.accept(0)
	case len(d.shown) > 1:
		dirPart, prefix := d.split()
		common := commonPrefix(d.shown)

		if len(common) <= len(prefix) {
			return nil
		}

		d.setText(dirPart + common)
	default:
		return nil
	}

	return d.refresh()
}

func (d *gotoDialog) accept(i int) {
	dirPart, _ := d.split()
	it := d.shown[i]
	text := dirPart + it.name

	if it.dir {
		text += "/"
	}

	d.setText(text)
}

func (d *gotoDialog) setText(text string) {
	d.input = newInput(text)
	d.sel = -1
}

// commonPrefix returns the longest name prefix shared by all suggestions that start
// with the same letters (case-sensitive, as the result becomes part of a path).
func commonPrefix(items []completion) string {
	common := items[0].name

	for _, it := range items[1:] {
		for !strings.HasPrefix(it.name, common) {
			common = common[:len(common)-1]
		}
	}

	return common
}

func (d *gotoDialog) view(m *Model, w, _ int) string {
	bw := boxWidth(w, 76)
	inner := bw - 4

	lines := []string{
		m.st.boxMuted.Render("path, ~/path, archive file or ssh://user@host/path"),
		"",
		d.input.render(inner, m.st.boxInput, m.st.segAccent),
	}

	if d.complete {
		lines = append(lines, "")
		lines = append(lines, d.suggestionLines(m, inner)...)
	}

	lines = append(lines, "", m.keyHint("⇥", "complete", "↑↓", "choose", "⏎", "go", "Esc", "cancel"))

	return m.renderBox("Go to", lines, bw)
}

// suggestionLines renders the list around the chosen entry, always completionRows high.
func (d *gotoDialog) suggestionLines(m *Model, w int) []string {
	lines := make([]string, 0, completionRows)
	start := max(min(d.sel-completionRows+1, len(d.shown)-completionRows), 0)

	for i := start; i < min(start+completionRows, len(d.shown)); i++ {
		it := d.shown[i]
		glyph, role := icons.For(it.name, it.dir, false, false, m.cfg.Icons)
		name := it.name

		if it.dir {
			name += "/"
		}

		text := fit(" "+strings.TrimSpace(glyph+" "+name), w)

		if i == d.sel {
			lines = append(lines, m.st.boxCursor.Render(text))
		} else {
			lines = append(lines, m.st.roles[role].Background(m.st.overlay).Render(text))
		}
	}

	if len(d.shown) == 0 && d.active() {
		msg := "no matches"
		if d.pendingDir != "" {
			msg = spinner()
		}

		lines = append(lines, m.st.boxMuted.Render(" "+msg))
	}

	for len(lines) < completionRows {
		lines = append(lines, "")
	}

	return lines
}
