package ui

import (
	"cmp"
	"context"
	"path"
	"slices"
	"strings"

	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/search"
	"github.com/abunjevac/bfiles/internal/vfs"
)

// historyLimit bounds the per-panel back/forward stacks.
const historyLimit = 100

// place is a location a panel can show.
type place struct {
	fs  vfs.FS
	dir string
}

// searchState turns a panel into a list of search results.
type searchState struct {
	id      int
	root    string
	opts    search.Options
	results []vfs.Entry
	running bool
	cancel  context.CancelFunc
	err     error
}

// Panel is one side of the screen.
type Panel struct {
	fs      vfs.FS
	dir     string
	entries []vfs.Entry // raw listing of dir
	view    []vfs.Entry // filtered and sorted rows, with ".." first when applicable

	cursor   int
	offset   int
	selected map[string]bool // by Path

	filter    string
	filtering bool

	showHidden  bool
	showSpecial bool
	showMtime   bool
	exactSizes  bool // byte counts instead of K/M/G; runtime only
	special     []string
	sortKey     string
	sortRev     bool

	back []place
	fwd  []place

	loading bool
	reqID   int
	cancel  context.CancelFunc

	search *searchState

	// rows is the number of list rows at the last render, for paging.
	rows int
}

func newPanel(cfg *config.Config) *Panel {
	return &Panel{
		selected:    map[string]bool{},
		showHidden:  cfg.Panels.ShowHidden,
		showSpecial: cfg.Panels.ShowSpecial,
		showMtime:   cfg.Panels.ShowMtime,
		special:     cfg.Panels.SpecialDirs,
		sortKey:     cfg.Panels.Sort,
		rows:        20,
	}
}

func (p *Panel) place() place {
	return place{fs: p.fs, dir: p.dir}
}

// atRoot reports whether ".." should not be shown.
func (p *Panel) atRoot() bool {
	if p.search != nil {
		return false
	}

	return p.dir == "/" && p.fs != nil && p.fs.Kind() != vfs.KindArchive
}

// current returns the entry under the cursor.
func (p *Panel) current() (vfs.Entry, bool) {
	if p.cursor < 0 || p.cursor >= len(p.view) {
		return vfs.Entry{}, false
	}

	return p.view[p.cursor], true
}

func isParent(e vfs.Entry) bool {
	return e.Name == ".."
}

// targets returns the entries an operation acts on: the selection, or the cursor
// entry when nothing is selected. ".." is never a target.
func (p *Panel) targets() []vfs.Entry {
	if len(p.selected) > 0 {
		out := make([]vfs.Entry, 0, len(p.selected))

		for _, e := range p.view {
			if p.selected[e.Path] {
				out = append(out, e)
			}
		}

		return out
	}

	if e, ok := p.current(); ok && !isParent(e) {
		return []vfs.Entry{e}
	}

	return nil
}

func paths(entries []vfs.Entry) []string {
	out := make([]string, len(entries))

	for i, e := range entries {
		out[i] = e.Path
	}

	return out
}

// visible applies hidden/special/filter rules to one entry.
func (p *Panel) visible(e vfs.Entry) bool {
	name := path.Base(e.Name)

	if !p.showSpecial && e.IsDir && slices.Contains(p.special, name) {
		return false
	}

	if !p.showHidden && strings.HasPrefix(name, ".") {
		return false
	}

	if p.filter != "" && !matchFilter(p.filter, e.Name) {
		return false
	}

	return true
}

// matchFilter is a case-insensitive subsequence-free substring match, with glob
// support when the filter contains wildcards.
func matchFilter(filter, name string) bool {
	f := strings.ToLower(filter)
	n := strings.ToLower(name)

	if strings.ContainsAny(f, "*?[") {
		ok, _ := path.Match(f, n)

		return ok
	}

	return strings.Contains(n, f)
}

// rebuild recomputes view from entries (or search results) keeping the cursor on the
// same name when possible.
func (p *Panel) rebuild(sizes func(vfs.Entry) (int64, bool)) {
	keep := ""

	if e, ok := p.current(); ok {
		keep = e.Path
	}

	p.rebuildSelect(keep, sizes)
}

func (p *Panel) rebuildSelect(keepPath string, sizes func(vfs.Entry) (int64, bool)) {
	src := p.entries

	if p.search != nil {
		src = p.search.results
	}

	view := make([]vfs.Entry, 0, len(src)+1)

	for _, e := range src {
		if p.visible(e) {
			view = append(view, e)
		}
	}

	sortEntries(view, p.sortKey, p.sortRev, sizes)

	if !p.atRoot() && p.search == nil {
		view = slices.Insert(view, 0, vfs.Entry{Name: "..", Path: path.Dir(p.dir), IsDir: true})
	}

	p.view = view
	p.pruneSelection()
	p.cursor = 0

	if keepPath != "" {
		if i := slices.IndexFunc(view, func(e vfs.Entry) bool { return e.Path == keepPath }); i >= 0 {
			p.cursor = i
		}
	}

	p.clamp()
}

// pruneSelection drops selections that are no longer visible.
func (p *Panel) pruneSelection() {
	if len(p.selected) == 0 {
		return
	}

	inView := make(map[string]bool, len(p.view))

	for _, e := range p.view {
		inView[e.Path] = true
	}

	for k := range p.selected {
		if !inView[k] {
			delete(p.selected, k)
		}
	}
}

func (p *Panel) selectName(name string) {
	if i := slices.IndexFunc(p.view, func(e vfs.Entry) bool { return e.Name == name }); i >= 0 {
		p.cursor = i
		p.clamp()
	}
}

// clamp keeps the cursor in range and visible.
func (p *Panel) clamp() {
	p.cursor = max(min(p.cursor, len(p.view)-1), 0)

	rows := max(p.rows, 1)

	if p.cursor < p.offset {
		p.offset = p.cursor
	}

	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}

	p.offset = max(min(p.offset, len(p.view)-rows), 0)
}

func (p *Panel) move(delta int) {
	p.cursor += delta
	p.clamp()
}

func (p *Panel) toggle(e vfs.Entry) {
	if isParent(e) {
		return
	}

	if p.selected[e.Path] {
		delete(p.selected, e.Path)
	} else {
		p.selected[e.Path] = true
	}
}

func (p *Panel) setSelected(e vfs.Entry, on bool) {
	if isParent(e) {
		return
	}

	if on {
		p.selected[e.Path] = true
	} else {
		delete(p.selected, e.Path)
	}
}

func (p *Panel) pushHistory(pl place) {
	if pl.fs == nil {
		return
	}

	p.back = append(p.back, pl)

	if len(p.back) > historyLimit {
		p.back = p.back[1:]
	}

	p.fwd = nil
}

// sortEntries orders directories first, then by key with natural name ordering.
func sortEntries(es []vfs.Entry, key string, rev bool, sizes func(vfs.Entry) (int64, bool)) {
	byKey := keyComparer(key, sizes)

	slices.SortStableFunc(es, func(a, b vfs.Entry) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}

			return 1
		}

		c := byKey(a, b)

		if c == 0 {
			c = naturalCompare(a.Name, b.Name)
		}

		if rev {
			return -c
		}

		return c
	})
}

// keyComparer compares by a sort key other than name; 0 defers to the name.
func keyComparer(key string, sizes func(vfs.Entry) (int64, bool)) func(a, b vfs.Entry) int {
	size := func(e vfs.Entry) int64 {
		if !e.IsDir || sizes == nil {
			return e.Size
		}

		if s, ok := sizes(e); ok {
			return s
		}

		return -1
	}

	switch key {
	case config.SortSize:
		return func(a, b vfs.Entry) int { return cmp.Compare(size(b), size(a)) }
	case config.SortMtime:
		return func(a, b vfs.Entry) int { return b.ModTime.Compare(a.ModTime) }
	case config.SortExt:
		return func(a, b vfs.Entry) int {
			return strings.Compare(strings.ToLower(path.Ext(a.Name)), strings.ToLower(path.Ext(b.Name)))
		}
	}

	return func(vfs.Entry, vfs.Entry) int { return 0 }
}

// naturalCompare orders names case-insensitively with digit runs compared by value,
// so "file2" sorts before "file10".
func naturalCompare(a, b string) int {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	i, j := 0, 0

	for i < len(la) && j < len(lb) {
		if isDigit(la[i]) && isDigit(lb[j]) {
			var c int

			i, j, c = compareNumbers(la, lb, i, j)
			if c != 0 {
				return c
			}

			continue
		}

		if la[i] != lb[j] {
			return cmp.Compare(la[i], lb[j])
		}

		i++
		j++
	}

	if c := cmp.Compare(len(la)-i, len(lb)-j); c != 0 {
		return c
	}

	return strings.Compare(a, b)
}

// compareNumbers compares the digit runs starting at a[i] and b[j] by value and
// returns the indexes after them.
func compareNumbers(a, b string, i, j int) (int, int, int) {
	si, sj := i, j

	for i < len(a) && isDigit(a[i]) {
		i++
	}

	for j < len(b) && isDigit(b[j]) {
		j++
	}

	na := strings.TrimLeft(a[si:i], "0")
	nb := strings.TrimLeft(b[sj:j], "0")

	if len(na) != len(nb) {
		return i, j, cmp.Compare(len(na), len(nb))
	}

	return i, j, strings.Compare(na, nb)
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// sizeText renders a size as the panel shows it: compact units or exact bytes.
func (p *Panel) sizeText(n int64) string {
	if p.exactSizes {
		return groupDigits(n)
	}

	return humanSize(n)
}
