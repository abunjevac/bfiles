package ui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/jobs"
	"github.com/abunjevac/bfiles/internal/vfs"
)

// spinInterval drives redraws while something animates.
const spinInterval = 100 * time.Millisecond

type spinTickMsg struct{}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	wasBusy := m.busy()
	cmd := m.handle(msg)

	if _, isSpin := msg.(spinTickMsg); !isSpin && !wasBusy && m.busy() {
		cmd = tea.Batch(cmd, spinTick())
	}

	return m, cmd
}

func spinTick() tea.Cmd {
	return tea.Tick(spinInterval, func(time.Time) tea.Msg { return spinTickMsg{} })
}

// busy reports whether a spinner is visible, so redraws keep it moving.
func (m *Model) busy() bool {
	for _, p := range m.panels {
		if p.loading || (p.search != nil && p.search.running) {
			return true
		}
	}

	for _, s := range m.sizes {
		if s.running {
			return true
		}
	}

	return m.quickView && m.previewRes == nil
}

// appliedMsg is implemented by bfiles' own messages: each knows how to apply itself,
// which keeps the dispatch below small.
type appliedMsg interface {
	apply(m *Model) tea.Cmd
}

// effectMsg is an own message that only changes state and never yields a command.
type effectMsg interface {
	effect(m *Model)
}

func (m *Model) handle(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case appliedMsg:
		return msg.apply(m)
	case effectMsg:
		msg.effect(m)

		return nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

		return nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		return m.handlePaste(msg)
	case tea.MouseClickMsg:
		return m.handleClick(msg.Mouse())
	case tea.MouseWheelMsg:
		return m.handleWheel(msg.Mouse())
	}

	if o := m.top(); o != nil {
		return m.updateOverlay(o, msg)
	}

	return nil
}

func (m *Model) handlePaste(msg tea.PasteMsg) tea.Cmd {
	if o := m.top(); o != nil {
		return m.updateOverlay(o, msg)
	}

	if p := m.panel(); p.filtering {
		in := newInput(p.filter)
		in.update(msg)
		p.filter = in.String()

		m.rebuild(m.active)
	}

	return nil
}

func (m *Model) updateOverlay(o overlay, msg tea.Msg) tea.Cmd {
	done, cmd := o.update(m, msg)

	if done {
		// the overlay may have pushed a follow-up dialog; remove exactly this one
		for i, cand := range slices.Backward(m.overlays) {
			if cand == o {
				m.overlays = slices.Delete(m.overlays, i, i+1)

				break
			}
		}
	}

	return cmd
}

func (m *Model) handleJobs(ev jobs.Event) tea.Cmd {
	m.jobInfos = ev.Jobs

	var cmds []tea.Cmd

	if ev.Conflict != nil {
		m.conflicts = append(m.conflicts, *ev.Conflict)
		cmds = append(cmds, m.nextConflict())
	}

	if ev.Finished != nil {
		cmds = append(cmds, m.jobFinished(*ev.Finished))
	}

	return tea.Batch(cmds...)
}

// jobFinished refreshes affected panels, reports the outcome, and handles the queue
// running empty.
func (m *Model) jobFinished(f jobs.Info) tea.Cmd {
	cmds := []tea.Cmd{m.reloadFS(f.SrcID), m.reloadFS(f.DstID), m.reportJob(f)}

	if m.queue.Pending() > 0 {
		return tea.Batch(cmds...)
	}

	if m.quitWhenIdle {
		return tea.Quit
	}

	m.queueIdle = timeNow()

	if m.queueOpen && !m.queueFocus {
		cmds = append(cmds, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return queueAutoCloseMsg{} }))
	}

	return tea.Batch(cmds...)
}

func (m *Model) reportJob(f jobs.Info) tea.Cmd {
	switch {
	case f.State == jobs.Failed:
		msg := f.Title + ": " + errText(f.Err)

		if f.Errors > 1 {
			msg += " (" + itoa(f.Errors) + " errors)"
		}

		return m.toast(msg, levelError)
	case f.State == jobs.Done && f.Finished.Sub(f.Started) > 2*time.Second:
		return m.toast("done: "+f.Title, levelInfo)
	}

	return nil
}

func errText(err error) string {
	if err == nil {
		return "failed"
	}

	return err.Error()
}

func (m *Model) applySize(msg sizeMsg) tea.Cmd {
	m.sizes[msg.key] = sizeState{bytes: msg.result.Bytes, files: msg.result.Files, dirs: msg.result.Dirs, err: msg.err}

	for i, p := range m.panels {
		if p.fs != nil && p.sortKey == "size" {
			m.rebuild(i)
		}
	}

	if msg.err != nil && msg.err.Error() != "context canceled" {
		return m.errorToast(msg.err)
	}

	return nil
}

// handleDirChanged reloads panels showing a local directory the watcher reported.
func (m *Model) handleDirChanged(dir string) tea.Cmd {
	var cmds []tea.Cmd

	for i, p := range m.panels {
		if p.fs != nil && p.fs.Kind() == vfs.KindLocal && p.dir == dir && p.search == nil && !p.loading {
			cmds = append(cmds, m.reload(i))
		}
	}

	return tea.Batch(cmds...)
}
