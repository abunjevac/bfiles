package ui

import (
	"fmt"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/abunjevac/bfiles/internal/jobs"
	"github.com/abunjevac/bfiles/internal/keymap"
)

// queueRows bounds the visible jobs in the queue card.
const queueRows = 8

func (m *Model) toggleQueue() {
	switch {
	case !m.queueOpen:
		m.queueOpen = true
		m.queueFocus = true
	case !m.queueFocus:
		m.queueFocus = true
	default:
		m.queueOpen = false
		m.queueFocus = false
	}
}

func (m *Model) selectedJob() (jobs.Info, bool) {
	if m.queueSel < 0 || m.queueSel >= len(m.jobInfos) {
		return jobs.Info{}, false
	}

	return m.jobInfos[m.queueSel], true
}

// queueKeys are the queue card's own keys; each acts on the selected job, if any.
//
//nolint:gochecknoglobals // static key table
var queueKeys = map[string]func(m *Model, j jobs.Info, ok bool){
	keyEsc:    closeQueue,
	"q":       closeQueue,
	"j":       func(m *Model, _ jobs.Info, _ bool) { m.queueSel = min(m.queueSel+1, len(m.jobInfos)-1) },
	keyDown:   func(m *Model, _ jobs.Info, _ bool) { m.queueSel = min(m.queueSel+1, len(m.jobInfos)-1) },
	"k":       func(m *Model, _ jobs.Info, _ bool) { m.queueSel = max(m.queueSel-1, 0) },
	keyUp:     func(m *Model, _ jobs.Info, _ bool) { m.queueSel = max(m.queueSel-1, 0) },
	"+":       func(m *Model, _ jobs.Info, _ bool) { m.queue.SetParallel(m.queue.Parallel() + 1) },
	"=":       func(m *Model, _ jobs.Info, _ bool) { m.queue.SetParallel(m.queue.Parallel() + 1) },
	"-":       func(m *Model, _ jobs.Info, _ bool) { m.queue.SetParallel(m.queue.Parallel() - 1) },
	"p":       togglePauseJob,
	keySpace:  togglePauseJob,
	"x":       cancelJob,
	keyDelete: cancelJob,
	"K": func(m *Model, j jobs.Info, ok bool) {
		if ok {
			m.queue.MoveUp(j.ID)
			m.queueSel = max(m.queueSel-1, 0)
		}
	},
	"J": func(m *Model, j jobs.Info, ok bool) {
		if ok {
			m.queue.MoveDown(j.ID)
			m.queueSel = min(m.queueSel+1, len(m.jobInfos)-1)
		}
	},
	"c": func(m *Model, _ jobs.Info, _ bool) {
		m.queue.ClearFinished()
		m.queueSel = 0
	},
}

// closeQueue hides the card; it comes back with the next job or the queue key.
func closeQueue(m *Model, _ jobs.Info, _ bool) {
	m.queueFocus = false
	m.queueOpen = false
}

func togglePauseJob(m *Model, j jobs.Info, ok bool) {
	if ok {
		m.queue.TogglePause(j.ID)
	}
}

// cancelJob cancels a pending job, or dismisses a finished one.
func cancelJob(m *Model, j jobs.Info, ok bool) {
	switch {
	case !ok:
	case j.State.Finished():
		m.queue.Dismiss(j.ID)
	default:
		m.queue.Cancel(j.ID)
	}
}

// queueKey handles keys while the queue card has focus. Unhandled keys fall through
// to the normal keymap so navigation keeps working.
func (m *Model) queueKey(key string) bool {
	fn, found := queueKeys[key]
	if !found {
		return false
	}

	j, ok := m.selectedJob()
	fn(m, j, ok)

	return true
}

func (m *Model) renderQueue() string {
	st := m.st
	w := min(max(m.width/2, 64), m.width-2, 80)
	inner := w - 4

	infos := m.jobInfos
	m.queueSel = max(min(m.queueSel, len(infos)-1), 0)

	title := fmt.Sprintf("⚡ Jobs · parallel %d", m.queue.Parallel())

	var lines []string

	if len(infos) == 0 {
		lines = append(lines, st.boxMuted.Render("no jobs"))
	}

	start := max(min(m.queueSel-queueRows/2, len(infos)-queueRows), 0)

	for i := start; i < min(start+queueRows, len(infos)); i++ {
		lines = append(lines, m.renderJob(infos[i], inner, m.queueFocus && i == m.queueSel)...)
	}

	if m.queueFocus {
		lines = append(lines, "", m.keyHint("p", "pause", "x", "cancel", "K/J", "order", "+/-", "parallel", "c", "clear", "Esc", "close"))
	} else if keys := m.km.Keys(keymap.Queue); len(keys) > 0 {
		lines = append(lines, m.st.boxMuted.Render(keymap.DisplaySequence(keys[0])+" to manage"))
	}

	box := st.box

	if m.queueFocus {
		box = box.BorderForeground(st.warnText.GetForeground())
	}

	return m.renderBoxWith(box, title, lines, w)
}

// jobIcon returns the state glyph of a job and its style.
func (m *Model) jobIcon(j jobs.Info) (string, lipgloss.Style) {
	st := m.st

	switch {
	case j.Paused:
		return "⏸", st.boxKey
	case j.State == jobs.Running:
		return "▶", st.boxKey
	case j.State == jobs.Done:
		return "✓", st.okText.Background(st.overlay)
	case j.State == jobs.Failed:
		return "✗", st.errorText.Background(st.overlay)
	case j.State == jobs.Canceled:
		return "⊘", st.boxMuted
	}

	return "○", st.boxMuted
}

// jobProgress is the right-hand status of a job row.
func jobProgress(j jobs.Info) string {
	switch {
	case j.State == jobs.Running && j.Total > 0:
		return fmt.Sprintf(" %s %3d%%", progressBar(j.Done, j.Total, 12), j.Done*100/j.Total)
	case j.State == jobs.Running && j.FilesTotal > 0:
		return fmt.Sprintf(" %d/%d", j.Files, j.FilesTotal)
	}

	return " " + j.State.String()
}

// jobDetail is the second line of a running job: speed, ETA and current file.
func jobDetail(j jobs.Info) string {
	if j.Speed <= 0 {
		return j.Current
	}

	eta := ""

	if remaining := j.Total - j.Done; remaining > 0 {
		eta = " · " + duration(time.Duration(float64(remaining)/j.Speed*float64(time.Second))) + " left"
	}

	return fmt.Sprintf("%s/s%s · %s", humanSize(int64(j.Speed)), eta, j.Current)
}

func (m *Model) renderJob(j jobs.Info, w int, selected bool) []string {
	st := m.st
	icon, iconStyle := m.jobIcon(j)

	textStyle := st.boxMuted
	if j.State == jobs.Running {
		textStyle = st.boxText
	}

	if selected {
		textStyle = st.boxCursor
		iconStyle = iconStyle.Background(st.cursor.GetBackground())
	}

	right := jobProgress(j)
	lines := []string{iconStyle.Render(icon+" ") + textStyle.Render(fit(j.Title, w-2-len([]rune(right)))+right)}

	if j.State == jobs.Running {
		lines = append(lines, st.boxMuted.Render("  "+fitLeft(jobDetail(j), w-2)))
	}

	if j.State == jobs.Failed && j.Err != nil {
		lines = append(lines, st.errorText.Background(st.overlay).Render("  "+fit(j.Err.Error(), w-2)))
	}

	return lines
}
