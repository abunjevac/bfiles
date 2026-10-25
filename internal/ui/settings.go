package ui

import (
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/theme"
)

// setTheme switches the live theme without persisting it.
func (m *Model) setTheme(name string) error {
	th, err := m.bundle.LoadTheme(name)

	m.theme = th
	m.st = newStyles(th)
	m.previewFor = "" // re-highlight with the new style

	return err
}

func (m *Model) themePicker() {
	orig := m.theme.Name
	names := theme.List(m.bundle.Dir)
	items := make([]listItem, len(names))

	for i, n := range names {
		hint := ""
		if n == orig {
			hint = "current"
		}

		items[i] = listItem{text: n, hint: hint, value: n}
	}

	d := &listDialog{
		title:  "Theme — previews as you move",
		items:  items,
		width:  48,
		footer: []string{"↑↓", "preview"},
		onMove: func(m *Model, it listItem) {
			_ = m.setTheme(it.value)
		},
		onSelect: func(m *Model, it listItem, _ string) tea.Cmd {
			name := it.value

			if err := m.setTheme(name); err != nil {
				return m.errorToast(err)
			}

			m.opts.Theme = ""

			return m.saveConfigChanges([]config.Change{{Key: fieldTheme, Value: name}}, "theme "+name+" saved")
		},
		onCancel: func(m *Model) tea.Cmd {
			_ = m.setTheme(orig)

			return nil
		},
	}

	d.refilter()
	d.sel = max(slices.Index(names, orig), 0)

	m.push(d)
}

// settingsDialog edits config.toml through a form. Theme and icon changes preview
// live; everything is written on save, keeping comments in the file.
func (m *Model) settingsDialog() {
	origTheme, origIcons := m.theme.Name, m.cfg.Icons
	fields := m.settingsFields()
	orig := slices.Clone(fields)

	revert := func(m *Model) {
		_ = m.setTheme(origTheme)
		m.cfg.Icons = origIcons
	}

	m.push(&formDialog{
		title:  "Settings · " + m.bundle.ConfigPath(),
		fields: fields,
		width:  84,
		onChange: func(m *Model, f *field) {
			switch f.key {
			case fieldTheme:
				_ = m.setTheme(f.value)
			case fieldIcons:
				m.cfg.Icons = f.value
			}
		},
		onCancel: func(m *Model) tea.Cmd {
			revert(m)

			return nil
		},
		extraKey: func(m *Model, key string) (bool, tea.Cmd) {
			if key != "e" {
				return false, nil
			}

			revert(m)

			return true, m.editConfig()
		},
		onSubmit: func(m *Model, fields []field) (tea.Cmd, string) {
			return m.saveSettings(orig, fields)
		},
	})
}

// Keys of settings fields with live preview.
const (
	fieldTheme = "theme"
	fieldIcons = "icons"
)

func (m *Model) settingsFields() []field {
	c := m.cfg

	return []field{
		{key: fieldTheme, label: "Theme", kind: fieldEnum, value: m.theme.Name, options: theme.List(m.bundle.Dir), section: "Look"},
		{
			key: fieldIcons, label: "Icons", kind: fieldEnum, value: c.Icons, section: "Look",
			options: []string{config.IconsNerd, config.IconsUnicode, config.IconsNone}, help: "nerd needs a Nerd Font in the terminal",
		},
		{key: "panels.show_hidden", label: "Show hidden files", kind: fieldBool, value: boolStr(c.Panels.ShowHidden), section: sectionPanels},
		{key: "panels.show_special", label: "Show special dirs", kind: fieldBool, value: boolStr(c.Panels.ShowSpecial), section: sectionPanels},
		{key: "panels.show_mtime", label: "Show mtime column", kind: fieldBool, value: boolStr(c.Panels.ShowMtime), section: sectionPanels},
		{
			key: "panels.sort", label: "Sort by", kind: fieldEnum, value: c.Panels.Sort, section: sectionPanels,
			options: []string{config.SortName, config.SortSize, config.SortMtime, config.SortExt},
		},
		{
			key: "panels.special_dirs", label: "Special dirs", kind: fieldList, value: joinWords(c.Panels.SpecialDirs), section: sectionPanels,
			help: "space-separated directory names hidden by the special-dirs toggle",
		},
		{key: "confirm_delete", label: "Confirm delete", kind: fieldBool, value: boolStr(c.ConfirmDelete), section: sectionBehave},
		{key: "use_trash", label: "Delete to trash", kind: fieldBool, value: boolStr(c.UseTrash), section: sectionBehave},
		{
			key: "parallel_jobs", label: "Parallel jobs", kind: fieldInt, value: strconv.Itoa(c.ParallelJobs), min: 1, max: 16, section: sectionBehave,
			help: "starting value; adjust live with + and - in the queue window",
		},
		{key: "which_key_delay_ms", label: "Which-key delay ms", kind: fieldInt, value: strconv.Itoa(c.WhichKeyDelay), min: 0, max: 5000, section: sectionBehave},
		{
			key: "dir_size", label: "Dir size", kind: fieldEnum, value: c.DirSize, section: sectionBehave,
			options: []string{config.DirSizeApparent, config.DirSizeDisk},
		},
		{
			key: "editor", label: "Editor", kind: fieldList, value: joinWords(c.Editor), section: sectionProgs,
			help: "empty uses $VISUAL / $EDITOR; {file} is the file",
		},
		{key: "opener", label: "Opener", kind: fieldList, value: joinWords(c.Opener), section: sectionProgs},
		{key: "terminal", label: "Terminal", kind: fieldList, value: joinWords(c.Terminal), section: sectionProgs, help: "{cwd} is the directory"},
		{
			key: "terminal_exec", label: "Terminal + command", kind: fieldList, value: joinWords(c.TerminalExec), section: sectionProgs,
			help: "used for SSH panels; {cmd} expands to the ssh command",
		},
		{
			key: "explorer", label: "File explorer", kind: fieldList, value: joinWords(c.Explorer), section: sectionProgs,
			help: "{url} is a local path or an sftp:// URL",
		},
		{key: "ssh.history_limit", label: "History entries", kind: fieldInt, value: strconv.Itoa(c.SSH.HistoryLimit), min: 1, max: 1000, section: sectionSSH},
		{key: "ssh.keepalive_sec", label: "Keep-alive seconds", kind: fieldInt, value: strconv.Itoa(c.SSH.KeepaliveSec), min: 0, max: 3600, section: sectionSSH},
		{
			key: "preview.max_bytes", label: "Preview max size", kind: fieldSize, value: c.Preview.MaxBytes.String(),
			section: "Preview", help: "e.g. 512K, 1M, 2G; bookmarks and previewer rules are edited in the file (e)",
		},
	}
}

// saveSettings writes the fields that changed into config.toml and reloads it.
func (m *Model) saveSettings(orig, fields []field) (tea.Cmd, string) {
	var changes []config.Change

	for i, f := range fields {
		if f.value == orig[i].value {
			continue
		}

		v, err := fieldValue(f)
		if err != nil {
			return nil, err.Error()
		}

		changes = append(changes, config.Change{Key: f.key, Value: v})
	}

	if len(changes) == 0 {
		return nil, ""
	}

	if err := config.UpdateFile(m.bundle.ConfigPath(), changes); err != nil {
		return nil, err.Error()
	}

	m.opts.Theme = ""

	return tea.Batch(m.reloadConfigCmd(), m.toast("settings saved", levelInfo)), ""
}

func fieldValue(f field) (any, error) {
	switch f.kind { //nolint:exhaustive // text and enum values are stored as strings
	case fieldBool:
		return f.value == valueTrue, nil
	case fieldInt:
		return strconv.Atoi(f.value)
	case fieldList:
		words, err := splitWords(f.value)
		if words == nil {
			words = []string{}
		}

		return words, err
	}

	return f.value, nil
}

// reloadConfigCmd re-reads config, keymap and theme in the background.
func (m *Model) reloadConfigCmd() tea.Cmd {
	b := m.bundle
	override := m.opts.Theme

	return func() tea.Msg {
		cfg, err := config.LoadFile(b.ConfigPath())
		if err != nil {
			return configReloadedMsg{errText: "config: " + err.Error()}
		}

		msg := configReloadedMsg{cfg: cfg}

		var errs []string

		// on errors msg.km and msg.th stay nil and the current ones are kept
		if km, err := b.LoadKeymap(); err != nil {
			errs = append(errs, "keymap: "+err.Error())
		} else {
			msg.km = km
		}

		name := cfg.Theme
		if override != "" {
			name = override
		}

		if th, err := b.LoadTheme(name); err != nil {
			errs = append(errs, err.Error())
		} else {
			msg.th = th
		}

		msg.errText = strings.Join(errs, "; ")

		return msg
	}
}

func (m *Model) applyConfig(msg configReloadedMsg) tea.Cmd {
	if msg.cfg == nil {
		return m.toast(msg.errText+" — keeping the previous settings", levelError)
	}

	prevParallel, prevPanels := m.cfg.ParallelJobs, m.cfg.Panels

	m.cfg = msg.cfg
	m.bundle.Config = msg.cfg
	m.sshc = nil

	if msg.km != nil {
		m.km = msg.km
	}

	if msg.th != nil {
		m.theme = msg.th
		m.st = newStyles(msg.th)
	}

	if m.cfg.ParallelJobs != prevParallel {
		m.queue.SetParallel(m.cfg.ParallelJobs)
	}

	for i, p := range m.panels {
		p.applyDefaults(prevPanels, m.cfg.Panels)
		m.rebuild(i)
	}

	m.previewFor = ""

	if msg.errText != "" {
		return m.toast(msg.errText, levelError)
	}

	return m.schedulePreview()
}
