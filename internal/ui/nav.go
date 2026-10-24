package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/location"
	"github.com/abunjevac/bfiles/internal/sshcfg"
	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/archive"
	"github.com/abunjevac/bfiles/internal/vfs/sftpfs"
)

// navigate lists dir on f for panel i in the background. The panel keeps showing its
// current location until the listing succeeds.
func (m *Model) navigate(i int, f vfs.FS, dir, selectName string, history bool) tea.Cmd {
	return m.list(i, listedMsg{fs: f, dir: dir, selectName: selectName, history: history})
}

// reload re-lists the panel's directory, keeping cursor and selection.
func (m *Model) reload(i int) tea.Cmd {
	p := m.panels[i]

	if p.fs == nil {
		return nil
	}

	if p.search != nil {
		return m.pruneSearch(i)
	}

	return m.list(i, listedMsg{fs: p.fs, dir: p.dir, keepCursor: true})
}

func (m *Model) reloadFS(id string) tea.Cmd {
	var cmds []tea.Cmd

	for i, p := range m.panels {
		if p.fs != nil && p.fs.ID() == id {
			cmds = append(cmds, m.reload(i))
		}
	}

	return tea.Batch(cmds...)
}

func (m *Model) list(i int, req listedMsg) tea.Cmd {
	p := m.panels[i]

	if p.cancel != nil {
		p.cancel()
	}

	ctx, cancel := context.WithCancel(m.ctx)

	p.reqID++
	p.cancel = cancel
	p.loading = true

	req.panel = i
	req.reqID = p.reqID

	// the listing holds a reference until it is applied or dropped
	m.retain(req.fs)

	return func() tea.Msg {
		defer cancel()

		req.entries, req.err = req.fs.ReadDir(ctx, req.dir)

		return req
	}
}

// applyListing installs a finished listing, unless the panel has moved on since.
func (m *Model) applyListing(msg listedMsg) tea.Cmd {
	p := m.panels[msg.panel]

	if msg.reqID != p.reqID {
		m.release(msg.fs)

		return nil
	}

	p.loading = false

	if msg.err != nil {
		return m.listingFailed(msg)
	}

	sameDir := p.fs == msg.fs && p.dir == msg.dir

	if !sameDir {
		p.leaveDir(msg.history)
	}

	if p.search != nil && !msg.keepCursor {
		m.endSearch(msg.panel)
	}

	keep := p.cursorPathIf(sameDir || msg.keepCursor)
	old := p.fs
	p.fs, p.dir, p.entries = msg.fs, msg.dir, msg.entries

	// the panel now owns the listing's reference; drop the one it held before
	m.release(old)

	p.rebuildSelect(keep, m.knownSize(p.fs))

	if msg.selectName != "" {
		p.selectName(msg.selectName)
	}

	m.syncWatches()

	return m.schedulePreview()
}

func (m *Model) listingFailed(msg listedMsg) tea.Cmd {
	m.release(msg.fs)

	if m.panels[msg.panel].fs == nil {
		// nothing to fall back to yet: start at home
		home, _ := os.UserHomeDir()

		return tea.Batch(m.errorToast(msg.err), m.navigate(msg.panel, m.local, home, "", false))
	}

	return m.errorToast(fmt.Errorf("%s: %w", msg.dir, unwrapPathErr(msg.err)))
}

// cursorPathIf returns the cursor entry's path when keep is set, so a reload can put
// the cursor back on it.
func (p *Panel) cursorPathIf(keep bool) string {
	if !keep {
		return ""
	}

	if cur, ok := p.current(); ok {
		return cur.Path
	}

	return ""
}

// leaveDir resets per-directory state before showing another directory.
func (p *Panel) leaveDir(history bool) {
	if history {
		p.pushHistory(p.place())
	}

	p.selected = map[string]bool{}
	p.filter = ""
	p.filtering = false
	p.offset = 0
}

func unwrapPathErr(err error) error {
	if pe, ok := errors.AsType[*os.PathError](err); ok {
		return pe.Err
	}

	return err
}

// enter opens the cursor entry: directories are entered, archives opened, other
// files handed to the opener.
func (m *Model) enter() tea.Cmd {
	p := m.panel()

	e, ok := p.current()
	if !ok {
		return nil
	}

	if isParent(e) {
		return m.parent()
	}

	if p.search != nil {
		if e.IsDir {
			return m.navigate(m.active, p.fs, e.Path, "", true)
		}

		return m.navigate(m.active, p.fs, path.Dir(e.Path), path.Base(e.Path), true)
	}

	if e.IsDir {
		return m.navigate(m.active, p.fs, e.Path, "", true)
	}

	if isArchiveName(e.Name) {
		return m.openArchiveCmd(m.active, p.fs, e.Path, "")
	}

	return m.openExternal(p.fs, e)
}

// parent goes up one level, leaving an archive or a search when at its root.
func (m *Model) parent() tea.Cmd {
	p := m.panel()

	if p.fs == nil {
		return nil
	}

	if p.search != nil {
		root := p.search.root

		m.endSearch(m.active)

		return m.navigate(m.active, p.fs, root, "", false)
	}

	if p.dir == "/" {
		if a, ok := p.fs.(*archive.FS); ok {
			outer, file := a.Outer()

			return m.navigate(m.active, outer, path.Dir(file), path.Base(file), true)
		}

		return nil
	}

	return m.navigate(m.active, p.fs, path.Dir(p.dir), path.Base(p.dir), true)
}

// closeLocation leaves an SSH remote, an archive or search results. An SSH panel
// returns to the last local directory it showed; the connection closes once no
// panel or job uses it any more.
func (m *Model) closeLocation() tea.Cmd {
	p := m.panel()

	switch {
	case p.search != nil:
		return m.parent()
	case p.fs.Kind() == vfs.KindArchive:
		a, ok := p.fs.(*archive.FS)
		if !ok {
			return nil
		}

		outer, file := a.Outer()

		return m.navigate(m.active, outer, path.Dir(file), path.Base(file), true)
	case p.fs.Kind() == vfs.KindSSH:
		for _, pl := range slices.Backward(p.back) {
			if pl.fs.Kind() == vfs.KindLocal {
				return m.navigate(m.active, pl.fs, pl.dir, "", true)
			}
		}

		home, _ := os.UserHomeDir()

		return m.navigate(m.active, m.local, home, "", true)
	}

	return nil
}

func (m *Model) historyBack() tea.Cmd {
	p := m.panel()

	if len(p.back) == 0 {
		return nil
	}

	pl := p.back[len(p.back)-1]
	p.back = p.back[:len(p.back)-1]
	p.fwd = append(p.fwd, p.place())

	return m.navigate(m.active, pl.fs, pl.dir, "", false)
}

func (m *Model) historyForward() tea.Cmd {
	p := m.panel()

	if len(p.fwd) == 0 {
		return nil
	}

	pl := p.fwd[len(p.fwd)-1]
	p.fwd = p.fwd[:len(p.fwd)-1]
	p.back = append(p.back, p.place())

	return m.navigate(m.active, pl.fs, pl.dir, "", false)
}

// openArchiveCmd opens an archive file in the background and enters it.
func (m *Model) openArchiveCmd(i int, outer vfs.FS, file, inner string) tea.Cmd {
	m.retain(outer)
	m.panels[i].loading = true

	ctx := m.ctx

	return func() tea.Msg {
		a, err := archive.Open(ctx, outer, file)

		msg := openedFSMsg{panel: i, dir: "/", err: err}

		if err == nil {
			msg.fs = a

			if inner != "" {
				msg.dir = inner
			}
		}

		return outerRelease{openedFSMsg: msg, outer: outer}
	}
}

// outerRelease carries an opened FS plus the temporary reference on its outer FS.
type outerRelease struct {
	openedFSMsg

	outer vfs.FS
}

func (m *Model) applyOpened(msg openedFSMsg) tea.Cmd {
	m.panels[msg.panel].loading = false

	if msg.err != nil {
		return m.errorToast(msg.err)
	}

	// the pool hands out one reference per Get; the model counts its own users, so a
	// connection already in use keeps a single pool reference
	if f, ok := msg.fs.(*sftpfs.FS); ok && m.refs[msg.fs] > 0 {
		m.pool.Release(f)
	}

	// list() takes the first model reference; a failed listing drops it and closes the FS
	return m.list(msg.panel, listedMsg{fs: msg.fs, dir: msg.dir, selectName: msg.selectName, history: true})
}

// connectCmd dials an SSH target for panel i.
func (m *Model) connectCmd(i int, spec location.Spec) tea.Cmd {
	m.panels[i].loading = true

	cfg := m.sshConfig()
	t := cfg.Resolve(spec.User, spec.Host, spec.Port)
	pool := m.pool
	ctx := m.ctx
	prompter := &uiPrompter{send: m.send}
	text := spec.Target() + spec.Path
	dirsHistory := m.bundle.Dir
	limit := m.cfg.SSH.HistoryLimit

	return func() tea.Msg {
		f, err := pool.Get(ctx, t, prompter)
		if err != nil {
			return openedFSMsg{panel: i, err: fmt.Errorf("ssh %s: %w", spec.Target(), err)}
		}

		dir := spec.Path

		switch {
		case dir == "":
			dir = f.Home()
		case !strings.HasPrefix(dir, "/"):
			dir = path.Join(f.Home(), dir)
		}

		rememberTarget(dirsHistory, text, limit)

		return openedFSMsg{panel: i, fs: f, dir: dir}
	}
}

func rememberTarget(dir, target string, limit int) {
	h, err := config.LoadHistory(dir)
	if err != nil {
		return
	}

	h.Touch(target, timeNow(), limit)

	_ = config.SaveHistory(dir, h)
}

func (m *Model) sshConfig() *sshcfg.Config {
	if m.sshc == nil {
		m.sshc, _ = sshcfg.Load(sshcfg.DefaultPath())
	}

	return m.sshc
}

// uiPrompter answers SSH prompts through dialogs. Dial runs in a command goroutine,
// so each prompt is a message to the UI plus a wait for the reply.
type uiPrompter struct {
	send func(tea.Msg)
}

var _ sftpfs.Prompter = (*uiPrompter)(nil)

func (u *uiPrompter) Secret(ctx context.Context, prompt string) (string, error) {
	r, err := u.ask(ctx, prompt, false)
	if err != nil {
		return "", err
	}

	if !r.ok {
		return "", errors.New("canceled")
	}

	return r.text, nil
}

func (u *uiPrompter) Confirm(ctx context.Context, question string) (bool, error) {
	r, err := u.ask(ctx, question, true)

	return r.ok, err
}

func (u *uiPrompter) ask(ctx context.Context, text string, confirm bool) (promptReply, error) {
	reply := make(chan promptReply, 1)

	u.send(promptMsg{text: text, confirm: confirm, reply: reply})

	select {
	case r := <-reply:
		return r, nil
	case <-ctx.Done():
		return promptReply{}, ctx.Err()
	}
}
