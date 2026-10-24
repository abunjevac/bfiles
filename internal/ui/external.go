package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/jobs"
	"github.com/abunjevac/bfiles/internal/launch"
	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/archive"
	"github.com/abunjevac/bfiles/internal/vfs/local"
	"github.com/abunjevac/bfiles/internal/vfs/sftpfs"
)

// Placeholders of command templates.
const (
	phCwd  = "cwd"
	phFile = "file"
	phURL  = "url"
	phCmd  = "cmd"
)

func localPathOf(f vfs.FS, p string) (string, bool) {
	if l, ok := f.(vfs.Localizer); ok {
		return l.LocalPath(p)
	}

	return "", false
}

// downloadedMsg reports a temporary local copy made for an external program.
type downloadedMsg struct {
	tmpDir string
	file   string
	err    error
	held   []vfs.FS
}

// download copies a non-local file into a private temp dir and returns the dir and
// the file path.
func download(ctx context.Context, f vfs.FS, p string) (string, string, error) {
	dir, err := os.MkdirTemp("", "bfiles-open-*")
	if err != nil {
		return "", "", err
	}

	file := filepath.Join(dir, path.Base(p))

	r, err := f.Open(ctx, p)
	if err != nil {
		_ = os.RemoveAll(dir)

		return "", "", err
	}

	defer r.Close()

	out, err := os.Create(file)
	if err != nil {
		_ = os.RemoveAll(dir)

		return "", "", err
	}

	_, err = io.Copy(out, r)

	if cerr := out.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		_ = os.RemoveAll(dir)

		return "", "", err
	}

	return dir, file, nil
}

// openExternal hands a file to the configured opener, fetching non-local files first.
func (m *Model) openExternal(f vfs.FS, e vfs.Entry) tea.Cmd {
	if lp, ok := localPathOf(f, e.Path); ok {
		return m.runOpener(lp)
	}

	held := m.hold(f)
	ctx := m.ctx

	return func() tea.Msg {
		dir, file, err := download(ctx, f, e.Path)

		return downloadedMsg{tmpDir: dir, file: file, err: err, held: held}
	}
}

func (m *Model) applyDownloaded(msg downloadedMsg) tea.Cmd {
	for _, f := range msg.held {
		m.release(f)
	}

	if msg.err != nil {
		return m.errorToast(msg.err)
	}

	m.tempDirs = append(m.tempDirs, msg.tmpDir)

	return m.runOpener(msg.file)
}

func (m *Model) runOpener(file string) tea.Cmd {
	argv := launch.Expand(m.cfg.Opener, launch.Vars{Values: map[string]string{phFile: file}})

	if err := launch.Detached(argv, filepath.Dir(file)); err != nil {
		return m.errorToast(fmt.Errorf("open: %w", err))
	}

	return m.infof("opened %s", filepath.Base(file))
}

// edit opens the cursor file in the editor. Non-local files are edited as a temp copy
// that is uploaded (or repacked into the archive) when it changed.
func (m *Model) edit() tea.Cmd {
	p := m.panel()

	e, ok := p.current()
	if !ok || isParent(e) || e.IsDir {
		return nil
	}

	if lp, ok := localPathOf(p.fs, e.Path); ok {
		return m.startEditor(editReadyMsg{panel: m.active, fs: p.fs, path: e.Path, local: lp})
	}

	f := p.fs
	held := m.hold(f)
	panel := m.active
	ctx := m.ctx

	return func() tea.Msg {
		dir, file, err := download(ctx, f, e.Path)

		msg := editReadyMsg{panel: panel, fs: f, path: e.Path, local: file, tmpDir: dir, err: err, held: held}

		if err == nil {
			msg.sum, msg.err = fileSum(file)
		}

		return msg
	}
}

func fileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}

	defer f.Close()

	h := sha256.New()

	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func (m *Model) startEditor(msg editReadyMsg) tea.Cmd {
	if msg.err != nil {
		for _, f := range msg.held {
			m.release(f)
		}

		if msg.tmpDir != "" {
			_ = os.RemoveAll(msg.tmpDir)
		}

		return m.errorToast(msg.err)
	}

	argv := launch.Expand(launch.Editor(m.cfg.Editor), launch.Vars{Values: map[string]string{phFile: msg.local}})

	if !launch.HasPlaceholder(launch.Editor(m.cfg.Editor), phFile) {
		argv = append(argv, msg.local)
	}

	cmd := exec.CommandContext(m.ctx, argv[0], argv[1:]...) //nolint:gosec // user-configured editor
	cmd.Dir = filepath.Dir(msg.local)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editDoneMsg{ready: msg, err: err}
	})
}

func (m *Model) finishEdit(msg editDoneMsg) tea.Cmd {
	r := msg.ready

	var cmds []tea.Cmd

	if msg.err != nil {
		cmds = append(cmds, m.errorToast(fmt.Errorf("editor: %w", msg.err)))
	}

	if r.tmpDir == "" {
		return tea.Batch(append(cmds, m.reloadFS(r.fs.ID()))...)
	}

	sum, err := fileSum(r.local)

	if err != nil || sum == r.sum {
		_ = os.RemoveAll(r.tmpDir)

		for _, f := range r.held {
			m.release(f)
		}

		return tea.Batch(cmds...)
	}

	tmp := r.tmpDir

	// the editor's held reference moves to the upload job
	cmds = append(cmds, m.applyEnqueue(enqueueMsg{
		spec: jobs.Spec{
			Op: jobs.OpCopy, Src: local.New(), Paths: []string{r.local},
			Dst: r.fs, DstDir: path.Dir(r.path), Rename: path.Base(r.path), Overwrite: true,
			Title: "save " + path.Base(r.path),
			After: func() { _ = os.RemoveAll(tmp) },
		},
		held: r.held,
	}))

	return tea.Batch(cmds...)
}

// localDirOf finds a local directory for launching programs from a panel: the panel
// dir itself, or the directory holding an archive that lives on the local disk.
func localDirOf(f vfs.FS, dir string) (string, bool) {
	if lp, ok := localPathOf(f, dir); ok {
		return lp, true
	}

	if a, ok := f.(*archive.FS); ok {
		outer, file := a.Outer()

		return localDirOf(outer, path.Dir(file))
	}

	return "", false
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (m *Model) launchTerminal() tea.Cmd {
	p := m.panel()
	dir := panelDir(p)

	if s, ok := p.fs.(*sftpfs.FS); ok {
		t := s.Target()
		ssh := []string{"ssh", "-t"}

		if t.Port != 22 {
			ssh = append(ssh, "-p", strconv.Itoa(t.Port))
		}

		ssh = append(ssh, t.User+"@"+t.Alias, "cd "+shellQuote(dir)+` && exec "$SHELL" -l`)

		home, _ := os.UserHomeDir()
		argv := launch.Expand(m.cfg.TerminalExec, launch.Vars{
			Values: map[string]string{phCwd: home},
			Lists:  map[string][]string{phCmd: holdOnFailure(ssh)},
		})

		return m.detached(argv, home, "terminal")
	}

	ld, ok := localDirOf(p.fs, dir)
	if !ok {
		return m.toast("no local directory to open a terminal in", levelWarn)
	}

	argv := launch.Expand(m.cfg.Terminal, launch.Vars{Values: map[string]string{phCwd: ld}})

	return m.detached(argv, ld, "terminal")
}

// holdOnFailure wraps a command so that, when it fails, the terminal stays open with
// the exit status instead of vanishing before the error can be read.
func holdOnFailure(argv []string) []string {
	const script = `"$@"; s=$?; if [ "$s" -ne 0 ]; then ` +
		`printf '\n[bfiles] %s exited with status %s - press Enter to close' "$1" "$s"; read -r _; fi`

	return append([]string{"sh", "-c", script, "bfiles"}, argv...)
}

func (m *Model) launchExplorer() tea.Cmd {
	p := m.panel()
	dir := panelDir(p)
	home, _ := os.UserHomeDir()

	var url string

	if s, ok := p.fs.(*sftpfs.FS); ok {
		t := s.Target()
		url = "sftp://" + t.User + "@" + net.JoinHostPort(t.Host, strconv.Itoa(t.Port)) + dir
	} else if ld, ok := localDirOf(p.fs, dir); ok {
		url = ld
	} else {
		return m.toast("this location cannot be opened in a file explorer", levelWarn)
	}

	argv := launch.Expand(m.cfg.Explorer, launch.Vars{Values: map[string]string{phURL: url, phCwd: url}})

	return m.detached(argv, home, "file explorer")
}

func (m *Model) detached(argv []string, dir, what string) tea.Cmd {
	if err := launch.Detached(argv, dir); err != nil {
		return m.errorToast(fmt.Errorf("%s: %w", what, err))
	}

	return nil
}

// editConfig opens config.toml in the editor; the watcher applies the changes.
func (m *Model) editConfig() tea.Cmd {
	return m.startEditor(editReadyMsg{panel: m.active, fs: m.local, path: m.bundle.ConfigPath(), local: m.bundle.ConfigPath()})
}
