package ui

import (
	"path"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/keymap"
	"github.com/abunjevac/bfiles/internal/location"
	"github.com/abunjevac/bfiles/internal/sshcfg"
	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/archive"
	"github.com/abunjevac/bfiles/internal/vfs/sftpfs"
)

// locationString renders a panel location so gotoText can parse it back.
func (m *Model) locationString(f vfs.FS, dir string) string {
	switch t := f.(type) {
	case *sftpfs.FS:
		tg := t.Target()
		spec := location.Spec{Kind: location.SSH, User: tg.User, Host: tg.Alias, Port: tg.Port, Path: dir}

		return spec.String()
	case *archive.FS:
		outer, file := t.Outer()

		return m.locationString(outer, file)
	}

	return dir
}

// parseRemote interprets connect-dialog text: an ssh:// URL, scp-style user@host:path,
// or a bare host alias.
func parseRemote(text string) (location.Spec, bool) {
	text = strings.TrimSpace(text)

	if text == "" {
		return location.Spec{}, false
	}

	if strings.HasPrefix(text, "ssh://") || strings.HasPrefix(text, "sftp://") || strings.ContainsAny(text, "@:") {
		// a cwd that cannot exist forces remote interpretation
		spec, err := location.ParseArg(text, "/\x00")
		if err != nil || spec.Kind != location.SSH {
			return location.Spec{}, false
		}

		return spec, true
	}

	if strings.ContainsAny(text, "/ ") {
		return location.Spec{}, false
	}

	return location.Spec{Kind: location.SSH, Host: text}, true
}

// gotoText navigates the active panel to typed text: a remote target, or a path on
// the panel's filesystem (relative to its directory, ~ for the local home).
func (m *Model) gotoText(text string) tea.Cmd {
	text = strings.TrimSpace(text)
	p := m.panel()

	switch {
	case text == "":
		return nil
	case strings.HasPrefix(text, "ssh://") || strings.HasPrefix(text, "sftp://"):
		spec, ok := parseRemote(text)
		if !ok {
			return m.toast("not a valid target: "+text, levelError)
		}

		return m.connectCmd(m.active, spec)
	case p.fs.Kind() == vfs.KindLocal || strings.HasPrefix(text, "~"):
		spec, err := location.ParseArg(text, panelDir(p))
		if err != nil {
			return m.errorToast(err)
		}

		return m.openSpec(m.active, spec)
	}

	return m.gotoOnFS(p, text)
}

// gotoOnFS goes to a path on the panel's own (non-local) filesystem.
func (m *Model) gotoOnFS(p *Panel, text string) tea.Cmd {
	target := text
	if !strings.HasPrefix(target, "/") {
		target = path.Join(panelDir(p), target)
	}

	f := p.fs
	held := m.hold(f)
	ctx := m.ctx
	id, reqID := m.startOpen(m.panelIndex(p.id))

	return func() tea.Msg {
		e, err := f.Stat(ctx, target)

		msg := openedFSMsg{panel: id, reqID: reqID, fs: f, dir: target, err: err}

		if err == nil && !e.IsDir {
			msg.dir, msg.selectName = path.Dir(target), path.Base(target)
		}

		return heldOpened{openedFSMsg: msg, held: held}
	}
}

// heldOpened is an openedFSMsg for an FS the panel already uses.
type heldOpened struct {
	openedFSMsg

	held []vfs.FS
}

func (m *Model) gotoDialog() tea.Cmd {
	p := m.panel()
	d, cmd := newGotoDialog(m.locationString(p.fs, panelDir(p)), panelDir(p), p.fs.Kind() == vfs.KindLocal,
		func(m *Model, v string) tea.Cmd { return m.gotoText(v) })

	m.push(d)

	return cmd
}

func (m *Model) bookmarksDialog() tea.Cmd {
	names := make([]string, 0, len(m.cfg.Bookmarks))

	for n := range m.cfg.Bookmarks {
		names = append(names, n)
	}

	slices.Sort(names)

	items := make([]listItem, 0, len(names))

	for _, n := range names {
		items = append(items, listItem{text: n, hint: m.cfg.Bookmarks[n], icon: "", value: m.cfg.Bookmarks[n]})
	}

	m.push(&listDialog{
		title:  "Bookmarks",
		items:  items,
		footer: []string{"C-a", "add current", "C-d", "delete"},
		onSelect: func(m *Model, it listItem, _ string) tea.Cmd {
			return m.gotoText(it.value)
		},
		onKey: func(m *Model, d *listDialog, key string) (bool, tea.Cmd) {
			switch key {
			case keyCtrlA:
				m.pop()
				m.addBookmarkDialog()

				return true, nil
			case keyCtrlD:
				it, ok := d.current()
				if !ok {
					return true, nil
				}

				d.items = slices.DeleteFunc(d.items, func(x listItem) bool { return x.text == it.text })
				d.refilter()

				return true, m.saveConfigChanges([]config.Change{{Key: "bookmarks." + it.text}}, "bookmark removed")
			}

			return false, nil
		},
	})

	return nil
}

func (m *Model) addBookmarkDialog() {
	p := m.panel()
	loc := m.locationString(p.fs, panelDir(p))

	m.push(&inputDialog{
		title: "Add bookmark",
		label: loc,
		input: newInput(path.Base(panelDir(p))),
		onSubmit: func(m *Model, v string) tea.Cmd {
			v = strings.TrimSpace(v)
			if v == "" {
				return nil
			}

			return m.saveConfigChanges([]config.Change{{Key: "bookmarks." + v, Value: loc}}, "bookmark added")
		},
	})
}

// saveConfigChanges edits config.toml in place and reloads it.
func (m *Model) saveConfigChanges(changes []config.Change, info string) tea.Cmd {
	if err := config.UpdateFile(m.bundle.ConfigPath(), changes); err != nil {
		return m.errorToast(err)
	}

	return tea.Batch(m.reloadConfigCmd(), m.toast(info, levelInfo))
}

func (m *Model) connectDialog() tea.Cmd {
	cfg := m.sshConfig()
	hist, _ := config.LoadHistory(m.bundle.Dir)

	items := m.connectItems(cfg, hist)

	m.push(&listDialog{
		title:     "SSH connect",
		items:     items,
		allowFree: true,
		width:     72,
		footer:    []string{"C-d", "forget"},
		onSelect: func(m *Model, it listItem, typed string) tea.Cmd {
			text := typed

			if it.value != "" {
				text = it.value
			}

			spec, ok := parseRemote(text)
			if !ok {
				return m.toast("not a valid target: "+text, levelError)
			}

			return m.connectCmd(m.active, spec)
		},
		onKey: func(m *Model, d *listDialog, key string) (bool, tea.Cmd) {
			if key != keyCtrlD {
				return false, nil
			}

			it, ok := d.current()
			if !ok || it.icon != historyIcon {
				return true, nil
			}

			hist.Remove(it.value)

			if err := config.SaveHistory(m.bundle.Dir, hist); err != nil {
				return true, m.errorToast(err)
			}

			d.items = m.connectItems(cfg, hist)
			d.refilter()

			return true, nil
		},
	})

	return nil
}

const (
	historyIcon = ""
	aliasIcon   = ""
)

func (m *Model) connectItems(cfg *sshcfg.Config, hist *config.History) []listItem {
	items := make([]sshcfg.HistoryItem, 0, len(hist.Entries))

	for _, e := range hist.Entries {
		items = append(items, sshcfg.HistoryItem{Target: e.Target, Last: e.Last})
	}

	cands := sshcfg.Merge(cfg.Aliases(), items, func(text string) string {
		spec, ok := parseRemote(text)
		if !ok {
			return ""
		}

		t := cfg.Resolve(spec.User, spec.Host, spec.Port)

		return t.Key() + strings.TrimSuffix(spec.Path, "/")
	})

	out := make([]listItem, 0, len(cands))

	for _, c := range cands {
		icon := aliasIcon
		if !c.LastUsed.IsZero() {
			icon = historyIcon
		}

		out = append(out, listItem{text: c.Text, hint: c.ResolvedAs, icon: icon, value: c.Text})
	}

	return out
}

// helpDialog lists every binding; choosing one runs it, so help doubles as a
// command palette.
func (m *Model) helpDialog() {
	var items []listItem

	for _, cat := range keymap.Categories() {
		for _, info := range keymap.All() {
			if info.Category != cat {
				continue
			}

			keys := m.km.Keys(info.Action)
			disp := make([]string, len(keys))

			for i, k := range keys {
				disp[i] = keymap.DisplaySequence(k)
			}

			items = append(items, listItem{
				text:  string(cat) + " · " + info.Label,
				hint:  strings.Join(disp, " · "),
				value: string(info.Action),
			})
		}
	}

	m.push(&listDialog{
		title: "Keys — type to search, ⏎ runs the action",
		items: items,
		width: 84,
		onSelect: func(m *Model, it listItem, _ string) tea.Cmd {
			return m.dispatch(keymap.Action(it.value))
		},
	})
}

// openPrompt shows a dialog for an SSH prompt and sends the answer back.
func (m *Model) openPrompt(msg promptMsg) {
	reply := func(r promptReply) {
		select {
		case msg.reply <- r:
		default:
		}
	}

	if msg.confirm {
		m.push(&confirmDialog{
			opened:  timeNow(),
			title:   "SSH",
			lines:   []string{msg.text},
			buttons: []string{"Yes", "No"},
			sel:     1,
			cancel:  1,
			onChoice: func(_ *Model, choice int, _ bool) tea.Cmd {
				reply(promptReply{ok: choice == 0})

				return nil
			},
		})

		return
	}

	in := newInput("")
	in.mask = true

	m.push(&inputDialog{
		opened: timeNow(),
		title:  "SSH authentication",
		label:  msg.text,
		input:  in,
		onSubmit: func(_ *Model, v string) tea.Cmd {
			reply(promptReply{text: v, ok: true})

			return nil
		},
		onCancel: func(_ *Model) tea.Cmd {
			reply(promptReply{})

			return nil
		},
	})
}
