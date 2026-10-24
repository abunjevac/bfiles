package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/jobs"
)

func (spinTickMsg) apply(m *Model) tea.Cmd {
	if m.busy() {
		return spinTick()
	}

	return nil
}

func (msg listedMsg) apply(m *Model) tea.Cmd { return m.applyListing(msg) }

func (msg openedFSMsg) apply(m *Model) tea.Cmd { return m.applyOpened(msg) }

func (msg outerRelease) apply(m *Model) tea.Cmd {
	cmd := m.applyOpened(msg.openedFSMsg)

	m.release(msg.outer)

	return cmd
}

func (msg heldOpened) apply(m *Model) tea.Cmd {
	var cmd tea.Cmd

	if msg.err != nil {
		cmd = m.errorToast(msg.err)
	} else {
		cmd = m.list(msg.panel, listedMsg{fs: msg.fs, dir: msg.dir, selectName: msg.selectName, history: true})
	}

	for _, f := range msg.held {
		m.release(f)
	}

	return cmd
}

func (msg jobsMsg) apply(m *Model) tea.Cmd { return m.handleJobs(jobs.Event(msg)) }

func (msg sizeMsg) apply(m *Model) tea.Cmd { return m.applySize(msg) }

func (msg previewTickMsg) apply(m *Model) tea.Cmd { return m.startPreview(msg.id) }

func (msg previewMsg) apply(m *Model) tea.Cmd { return m.applyPreview(msg) }

func (msg whichKeyTickMsg) effect(m *Model) {
	if msg.seq == m.seqID && len(m.seq) > 0 {
		m.whichKey = true
	}
}

func (msg searchBatchMsg) apply(m *Model) tea.Cmd { return m.applySearchBatch(msg) }

func (msg searchDoneMsg) apply(m *Model) tea.Cmd { return m.applySearchDone(msg) }

func (msg searchPrunedMsg) effect(m *Model) { m.applySearchPruned(msg) }

func (toastExpireMsg) effect(m *Model) { m.expireToasts() }

func (msg opDoneMsg) apply(m *Model) tea.Cmd { return m.applyOpDone(msg) }

func (msg enqueueMsg) apply(m *Model) tea.Cmd { return m.applyEnqueue(msg) }

func (nextConflictMsg) apply(m *Model) tea.Cmd { return m.nextConflict() }

func (msg releaseMsg) effect(m *Model) { m.release(msg.fs) }

func (msg dirChangedMsg) apply(m *Model) tea.Cmd { return m.handleDirChanged(msg.dir) }

func (configChangedMsg) apply(m *Model) tea.Cmd { return m.reloadConfigCmd() }

func (msg configReloadedMsg) apply(m *Model) tea.Cmd { return m.applyConfig(msg) }

func (msg promptMsg) effect(m *Model) { m.openPrompt(msg) }

func (msg editReadyMsg) apply(m *Model) tea.Cmd { return m.startEditor(msg) }

func (msg editDoneMsg) apply(m *Model) tea.Cmd { return m.finishEdit(msg) }

func (msg downloadedMsg) apply(m *Model) tea.Cmd { return m.applyDownloaded(msg) }

func (queueAutoCloseMsg) effect(m *Model) {
	if m.queueOpen && !m.queueFocus && m.queue.Pending() == 0 && timeNow().Sub(m.queueIdle) >= time.Second {
		m.queueOpen = false
	}
}
