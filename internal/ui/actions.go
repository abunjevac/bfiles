package ui

import (
	"maps"
	"os"
	"path"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/keymap"
)

// actionFunc performs one keymap action.
type actionFunc func(m *Model) tea.Cmd

// dispatch runs a keymap action and refreshes quick view if the cursor moved.
func (m *Model) dispatch(a keymap.Action) tea.Cmd {
	if m.panel().fs == nil && a != keymap.Quit && a != keymap.Help {
		return nil
	}

	fn, ok := m.actions[a]
	if !ok {
		return nil
	}

	return tea.Batch(fn(m), m.schedulePreview())
}

// actionTable maps every action to its handler.
func actionTable() map[keymap.Action]actionFunc {
	t := map[keymap.Action]actionFunc{}

	for _, part := range []map[keymap.Action]actionFunc{navActions(), selectActions(), fileActions(), viewActions(), appActions()} {
		maps.Copy(t, part)
	}

	return t
}

// effect wraps a model change that needs no command.
func effect(fn func(m *Model)) actionFunc {
	return func(m *Model) tea.Cmd {
		fn(m)

		return nil
	}
}

// cursorAction wraps a panel-only change that needs no command.
func cursorAction(fn func(p *Panel)) actionFunc {
	return func(m *Model) tea.Cmd {
		fn(m.panel())

		return nil
	}
}

func navActions() map[keymap.Action]actionFunc {
	return map[keymap.Action]actionFunc{
		keymap.CursorUp:   cursorAction(func(p *Panel) { p.move(-1) }),
		keymap.CursorDown: cursorAction(func(p *Panel) { p.move(1) }),
		keymap.PageUp:     cursorAction(func(p *Panel) { p.move(-max(p.rows-1, 1)) }),
		keymap.PageDown:   cursorAction(func(p *Panel) { p.move(max(p.rows-1, 1)) }),
		keymap.Top:        cursorAction(func(p *Panel) { p.cursor = 0; p.clamp() }),
		keymap.Bottom:     cursorAction(func(p *Panel) { p.cursor = len(p.view) - 1; p.clamp() }),
		keymap.Parent:     (*Model).parent,
		keymap.Enter:      (*Model).enter,
		keymap.Back:       (*Model).historyBack,
		keymap.Forward:    (*Model).historyForward,
		keymap.GotoPath:   (*Model).gotoDialog,
		keymap.Bookmarks:  (*Model).bookmarksDialog,
		keymap.SwitchPanel: func(m *Model) tea.Cmd {
			m.active = 1 - m.active
			m.queueFocus = false

			return nil
		},
		keymap.HomeDir: func(m *Model) tea.Cmd {
			home, _ := os.UserHomeDir()

			return m.navigate(m.active, m.local, home, "", true)
		},
		keymap.SameLocation: func(m *Model) tea.Cmd {
			p := m.panel()

			return m.navigate(1-m.active, p.fs, p.dir, "", true)
		},
		keymap.SwapPanels: func(m *Model) tea.Cmd {
			m.panels[0], m.panels[1] = m.panels[1], m.panels[0]

			m.syncWatches()

			return nil
		},
		keymap.Reload: func(m *Model) tea.Cmd {
			m.clearSizes(m.panel())

			return m.reload(m.active)
		},
		keymap.Connect:    (*Model).connectDialog,
		keymap.Disconnect: (*Model).closeLocation,
	}
}

func selectActions() map[keymap.Action]actionFunc {
	return map[keymap.Action]actionFunc{
		keymap.ToggleSelect: cursorAction(func(p *Panel) {
			if e, ok := p.current(); ok {
				p.toggle(e)
				p.move(1)
			}
		}),
		keymap.SelectUp:   cursorAction(func(p *Panel) { p.extendSelection(-1) }),
		keymap.SelectDown: cursorAction(func(p *Panel) { p.extendSelection(1) }),
		keymap.SelectAll: cursorAction(func(p *Panel) {
			for _, e := range p.view {
				p.setSelected(e, true)
			}
		}),
		keymap.InvertSelection: cursorAction(func(p *Panel) {
			for _, e := range p.view {
				p.toggle(e)
			}
		}),
		keymap.SelectPattern:   func(m *Model) tea.Cmd { return m.patternDialog(true) },
		keymap.UnselectPattern: func(m *Model) tea.Cmd { return m.patternDialog(false) },
		keymap.ClearSelection:  (*Model).clearPanelState,
	}
}

func fileActions() map[keymap.Action]actionFunc {
	return map[keymap.Action]actionFunc{
		keymap.Copy:     func(m *Model) tea.Cmd { return m.transferDialog(false) },
		keymap.Move:     func(m *Model) tea.Cmd { return m.transferDialog(true) },
		keymap.Rename:   (*Model).renameDialog,
		keymap.Mkdir:    (*Model).mkdirDialog,
		keymap.NewFile:  (*Model).newFileDialog,
		keymap.Delete:   (*Model).deleteDialog,
		keymap.Edit:     (*Model).edit,
		keymap.Chmod:    (*Model).chmodDialog,
		keymap.CopyPath: (*Model).copyPath,
		keymap.DirSize:  (*Model).dirSizeTargets,
		keymap.SizeAll:  (*Model).dirSizeAll,
		keymap.Search:   (*Model).searchDialog,
		keymap.Pack:     (*Model).packDialog,
		keymap.Extract:  (*Model).extractDialog,
		keymap.Terminal: (*Model).launchTerminal,
		keymap.Explorer: (*Model).launchExplorer,
		keymap.Open: func(m *Model) tea.Cmd {
			p := m.panel()

			if e, ok := p.current(); ok && !isParent(e) {
				return m.openExternal(p.fs, e)
			}

			return nil
		},
	}
}

func viewActions() map[keymap.Action]actionFunc {
	rebuildAfter := func(fn func(p *Panel)) actionFunc {
		return func(m *Model) tea.Cmd {
			fn(m.panel())
			m.rebuild(m.active)

			return nil
		}
	}

	return map[keymap.Action]actionFunc{
		keymap.Filter:        cursorAction(func(p *Panel) { p.filtering = true }),
		keymap.ToggleHidden:  rebuildAfter(func(p *Panel) { p.showHidden = !p.showHidden }),
		keymap.ToggleSpecial: rebuildAfter(func(p *Panel) { p.showSpecial = !p.showSpecial }),
		keymap.ToggleMtime:   cursorAction(func(p *Panel) { p.showMtime = !p.showMtime }),
		keymap.ToggleBytes:   cursorAction(func(p *Panel) { p.exactSizes = !p.exactSizes }),
		keymap.FileInfo:      (*Model).infoDialog,
		keymap.SortName:      rebuildAfter(func(p *Panel) { p.setSort(config.SortName) }),
		keymap.SortSize:      rebuildAfter(func(p *Panel) { p.setSort(config.SortSize) }),
		keymap.SortMtime:     rebuildAfter(func(p *Panel) { p.setSort(config.SortMtime) }),
		keymap.SortExt:       rebuildAfter(func(p *Panel) { p.setSort(config.SortExt) }),
		keymap.SortReverse:   rebuildAfter(func(p *Panel) { p.sortRev = !p.sortRev }),
		keymap.View:          (*Model).openViewer,
		keymap.QuickView: func(m *Model) tea.Cmd {
			m.quickView = !m.quickView
			m.previewFor = ""

			return nil
		},
	}
}

func appActions() map[keymap.Action]actionFunc {
	return map[keymap.Action]actionFunc{
		keymap.Queue:      effect((*Model).toggleQueue),
		keymap.Help:       effect((*Model).helpDialog),
		keymap.Theme:      effect((*Model).themePicker),
		keymap.Settings:   effect((*Model).settingsDialog),
		keymap.EditConfig: (*Model).editConfig,
		keymap.Quit:       (*Model).quit,
	}
}

// setSort selects a sort key; choosing the current key again reverses the order.
func (p *Panel) setSort(key string) {
	if p.sortKey == key {
		p.sortRev = !p.sortRev

		return
	}

	p.sortKey = key
	p.sortRev = false
}

// extendSelection selects the cursor entry, moves, and selects the new one, so
// shift+arrows grow a range.
func (p *Panel) extendSelection(delta int) {
	if e, ok := p.current(); ok {
		p.setSelected(e, true)
	}

	p.move(delta)

	if e, ok := p.current(); ok {
		p.setSelected(e, true)
	}
}

func (m *Model) clearSizes(p *Panel) {
	prefix := sizeKey(p.fs, p.dir)

	for k, s := range m.sizes {
		if !s.running && strings.HasPrefix(k, prefix) {
			delete(m.sizes, k)
		}
	}
}

// clearPanelState is Esc in a panel: drop the filter, then the selection, then a search.
func (m *Model) clearPanelState() tea.Cmd {
	p := m.panel()

	switch {
	case p.filter != "":
		p.filter = ""

		m.rebuild(m.active)
	case len(p.selected) > 0:
		clear(p.selected)
	case p.search != nil && p.search.running:
		p.search.cancel()
	case p.search != nil:
		return m.parent()
	case m.queueFocus:
		m.queueFocus = false
	}

	return nil
}

func (m *Model) patternDialog(sel bool) tea.Cmd {
	title := "Select by pattern"
	if !sel {
		title = "Unselect by pattern"
	}

	m.push(&inputDialog{
		title: title,
		label: "Glob pattern, e.g. *.go (case-insensitive)",
		input: newInput("*"),
		onSubmit: func(m *Model, v string) tea.Cmd {
			p := m.panel()
			n := 0

			for _, e := range p.view {
				if isParent(e) || (e.IsDir && v == "*") {
					continue
				}

				if ok, _ := path.Match(strings.ToLower(v), strings.ToLower(path.Base(e.Name))); ok {
					p.setSelected(e, sel)

					n++
				}
			}

			return m.infof("%d matched", n)
		},
	})

	return nil
}

func (m *Model) dirSizeAll() tea.Cmd {
	p := m.panel()

	var dirs []string

	for _, e := range p.view {
		if e.IsDir && !e.IsLink && !isParent(e) {
			dirs = append(dirs, e.Path)
		}
	}

	return m.calcSizes(p.fs, dirs)
}

func (m *Model) dirSizeTargets() tea.Cmd {
	p := m.panel()

	var dirs []string

	for _, e := range p.targets() {
		if e.IsDir && !e.IsLink {
			dirs = append(dirs, e.Path)
		}
	}

	if len(dirs) == 0 {
		if e, ok := p.current(); ok && isParent(e) {
			dirs = []string{p.dir}
		}
	}

	return m.calcSizes(p.fs, dirs)
}

// quit leaves immediately unless jobs are pending; asking a second time while the
// question is open quits anyway.
func (m *Model) quit() tea.Cmd {
	n := m.queue.Pending()

	if n == 0 || m.quitAsked {
		return tea.Quit
	}

	m.quitAsked = true

	m.push(&confirmDialog{
		title:   "Jobs still running",
		lines:   []string{itoa(n) + " job" + plural(n) + " not finished."},
		buttons: []string{"Wait and quit", "Cancel jobs and quit", "Back"},
		sel:     2,
		cancel:  2,
		danger:  true,
		onChoice: func(m *Model, choice int, _ bool) tea.Cmd {
			m.quitAsked = false

			switch choice {
			case 0:
				m.quitWhenIdle = true
				m.queueOpen = true

				return m.toast("bfiles will quit when all jobs are done", levelInfo)
			case 1:
				return tea.Quit
			}

			return nil
		},
	})

	return nil
}
