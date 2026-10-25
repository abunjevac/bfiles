package ui

import (
	"context"
	"errors"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/search"
	"github.com/abunjevac/bfiles/internal/vfs"
)

// maxSearchResults keeps a runaway search from exhausting memory.
const maxSearchResults = 100000

func (m *Model) searchDialog() tea.Cmd {
	p := m.panel()
	fields := m.lastSearch

	if fields == nil {
		fields = defaultSearchFields(p)
	}

	d := &formDialog{
		title:       "Search in " + m.placeText(p.fs, panelDir(p)),
		submitLabel: "search",
		fields:      slices.Clone(fields),
		onSubmit: func(m *Model, fields []field) (tea.Cmd, string) {
			// remembered for the next search in this session
			m.lastSearch = slices.Clone(fields)

			m.startSearch(m.active, searchOptions(fields, p.special))

			return nil, ""
		},
	}

	// start typing the pattern right away, keeping the previous one
	d.editing = true
	d.input = newInput(d.fields[0].value)

	m.push(d)

	return nil
}

func defaultSearchFields(p *Panel) []field {
	return []field{
		{
			key: "pattern", label: "Name", kind: fieldText, value: "", section: "Find",
			help: "glob (*.log), plain text (substring) or re:<regex>; empty matches all",
		},
		{
			key: "content", label: "Containing text", kind: fieldText, section: "Find",
			help: "only files whose content contains this text",
		},
		{key: "case", label: "Case sensitive", kind: fieldBool, value: valueFalse, section: sectionOptions},
		{key: "hidden", label: "Include hidden", kind: fieldBool, value: boolStr(p.showHidden), section: sectionOptions},
		{
			key: "special", label: "Include special dirs", kind: fieldBool, value: boolStr(p.showSpecial), section: sectionOptions,
			help: "descend into .git, node_modules and the other special dirs",
		},
	}
}

func searchOptions(fields []field, special []string) search.Options {
	get := func(k string) string {
		for _, f := range fields {
			if f.key == k {
				return f.value
			}
		}

		return ""
	}

	opts := search.Options{
		Pattern:       get("pattern"),
		Content:       get("content"),
		CaseSensitive: get("case") == valueTrue,
		Hidden:        get("hidden") == valueTrue,
	}

	if get("special") != valueTrue {
		opts.Skip = special
	}

	return opts
}

func boolStr(b bool) string {
	if b {
		return valueTrue
	}

	return valueFalse
}

func (m *Model) startSearch(i int, opts search.Options) {
	p := m.panels[i]

	m.endSearch(i)

	ctx, cancel := context.WithCancel(m.ctx)

	m.searchSeq++
	id := m.searchSeq

	root := panelDir(p)
	p.search = &searchState{id: id, root: root, opts: opts, running: true, cancel: cancel}
	p.filter = ""
	clear(p.selected)
	m.rebuild(i)

	f := p.fs
	send := m.send
	held := m.hold(f)

	go func() {
		defer cancel()

		count := 0

		err := search.Run(ctx, f, root, opts, func(batch []vfs.Entry) {
			count += len(batch)

			send(searchBatchMsg{panel: p.id, id: id, entries: batch})

			if count >= maxSearchResults {
				cancel()
			}
		})

		send(searchDoneMsg{panel: p.id, id: id, err: err})

		for _, h := range held {
			send(releaseMsg{fs: h})
		}
	}()
}

func (m *Model) endSearch(i int) {
	p := m.panels[i]

	if p.search == nil {
		return
	}

	if p.search.cancel != nil {
		p.search.cancel()
	}

	p.search = nil
}

func (m *Model) applySearchBatch(msg searchBatchMsg) tea.Cmd {
	i := m.panelIndex(msg.panel)
	p := m.panels[i]

	if p.search == nil || p.search.id != msg.id {
		return nil
	}

	p.search.results = append(p.search.results, msg.entries...)

	m.rebuild(i)

	return m.schedulePreview()
}

func (m *Model) applySearchDone(msg searchDoneMsg) tea.Cmd {
	p := m.panels[m.panelIndex(msg.panel)]

	if p.search == nil || p.search.id != msg.id {
		return nil
	}

	p.search.running = false

	if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
		p.search.err = msg.err

		return m.errorToast(msg.err)
	}

	n := len(p.search.results)

	if n >= maxSearchResults {
		return m.toast("search stopped at "+itoa(maxSearchResults)+" results", levelWarn)
	}

	return m.infof("search finished: %d found", n)
}

// pruneSearch drops results that no longer exist, after operations on them.
func (m *Model) pruneSearch(i int) tea.Cmd {
	p := m.panels[i]

	if p.search == nil {
		return nil
	}

	id := p.search.id
	results := append([]vfs.Entry(nil), p.search.results...)
	f := p.fs
	held := m.hold(f)
	ctx := m.ctx

	return func() tea.Msg {
		kept := results[:0]

		for _, e := range results {
			if fresh, err := f.Stat(ctx, e.Path); err == nil {
				fresh.Name = e.Name
				kept = append(kept, fresh)
			}
		}

		return searchPrunedMsg{panel: p.id, id: id, results: kept, held: held}
	}
}

type searchPrunedMsg struct {
	panel   int // Panel.id
	id      int
	results []vfs.Entry
	held    []vfs.FS
}

func (m *Model) applySearchPruned(msg searchPrunedMsg) {
	for _, f := range msg.held {
		m.release(f)
	}

	i := m.panelIndex(msg.panel)
	p := m.panels[i]

	if p.search == nil || p.search.id != msg.id || p.search.running {
		return
	}

	p.search.results = msg.results

	m.rebuild(i)
}
