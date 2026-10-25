package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/jobs"
	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/archive"
	"github.com/abunjevac/bfiles/internal/vfs/sftpfs"
)

// enqueueMsg delivers a job prepared in the background. held filesystems were
// retained when the dialog was submitted; fresh ones were created for the job.
type enqueueMsg struct {
	spec  jobs.Spec
	held  []vfs.FS
	fresh []vfs.FS
	err   error
}

func (m *Model) applyEnqueue(msg enqueueMsg) tea.Cmd {
	if msg.err != nil {
		for _, f := range msg.held {
			m.release(f)
		}

		for _, f := range msg.fresh {
			_ = f.Close()
		}

		return m.errorToast(msg.err)
	}

	for _, f := range msg.fresh {
		m.retain(f)
	}

	owned := append(append([]vfs.FS{}, msg.held...), msg.fresh...)
	send := m.send
	after := msg.spec.After

	msg.spec.After = func() {
		if after != nil {
			after()
		}

		for _, f := range owned {
			send(releaseMsg{fs: f})
		}
	}

	m.queue.Add(msg.spec)
	m.queueOpen = true

	return nil
}

// hold retains filesystems for an operation prepared in the background.
func (m *Model) hold(fss ...vfs.FS) []vfs.FS {
	for _, f := range fss {
		m.retain(f)
	}

	return fss
}

func describe(entries []vfs.Entry) string {
	if len(entries) == 1 {
		return path.Base(entries[0].Name)
	}

	return fmt.Sprintf("%d items", len(entries))
}

func fsName(f vfs.FS) string {
	if l := f.Label(); l != "" {
		return l
	}

	return "local disk"
}

// panelDir is where new items go for a panel; a search panel uses its root.
func panelDir(p *Panel) string {
	if p.search != nil {
		return p.search.root
	}

	return p.dir
}

func withSlash(dir string) string {
	if strings.HasSuffix(dir, "/") {
		return dir
	}

	return dir + "/"
}

// transferDialog asks where to copy or move the targets. The default is the other
// panel; an absolute path is taken on the other panel's filesystem and a relative one
// on the source filesystem, relative to the source directory.
func (m *Model) transferDialog(move bool) tea.Cmd {
	p, o := m.panel(), m.other()

	// filesystems are fixed when the dialog opens; a panel may switch while it is up
	srcFS, dstFS := p.fs, o.fs
	srcDir := panelDir(p)
	targets := p.targets()

	if len(targets) == 0 || dstFS == nil {
		return nil
	}

	verb, op := "Copy", jobs.OpCopy
	if move {
		verb, op = "Move", jobs.OpMove
	}

	def := withSlash(panelDir(o))

	m.push(&inputDialog{
		title: verb + " " + describe(targets),
		label: "to " + fsName(dstFS) + " (relative paths stay on " + fsName(srcFS) + ")",
		input: newInput(def),
		width: 72,
		onSubmit: func(m *Model, v string) tea.Cmd {
			v = strings.TrimSpace(v)
			if v == "" {
				return nil
			}

			dst, dstPath := dstFS, v

			if !strings.HasPrefix(v, "/") {
				dst, dstPath = srcFS, path.Join(srcDir, v)
				if strings.HasSuffix(v, "/") {
					dstPath += "/"
				}
			}

			return m.prepareTransfer(op, srcFS, targets, dst, dstPath)
		},
	})

	return nil
}

func (m *Model) prepareTransfer(op jobs.Op, src vfs.FS, targets []vfs.Entry, dst vfs.FS, dstPath string) tea.Cmd {
	held := m.hold(src, dst)
	ctx := m.ctx
	srcPaths := paths(targets)

	clear(m.panel().selected)

	return func() tea.Msg {
		spec := jobs.Spec{Op: op, Src: src, Paths: srcPaths, Dst: dst}

		isDir := strings.HasSuffix(dstPath, "/")
		dstPath = path.Clean(dstPath)

		if !isDir {
			e, err := dst.Stat(ctx, dstPath)
			isDir = (err == nil && e.IsDir) || (err != nil && len(srcPaths) > 1)
		}

		if isDir {
			spec.DstDir = dstPath
		} else {
			spec.DstDir = path.Dir(dstPath)
			spec.Rename = path.Base(dstPath)
		}

		return enqueueMsg{spec: spec, held: held}
	}
}

func (m *Model) deleteDialog() tea.Cmd {
	p := m.panel()

	// the filesystem is fixed when the dialog opens; the panel may switch while it is up
	srcFS := p.fs
	targets := p.targets()

	if len(targets) == 0 {
		return nil
	}

	_, canTrash := srcFS.(vfs.Trasher)
	trash := m.cfg.UseTrash && canTrash

	run := func(m *Model) tea.Cmd {
		clear(p.selected)

		return m.applyEnqueue(enqueueMsg{
			spec: jobs.Spec{Op: jobs.OpDelete, Src: srcFS, Paths: paths(targets), Trash: trash},
			held: m.hold(srcFS),
		})
	}

	if !m.cfg.ConfirmDelete {
		return run(m)
	}

	verb := "Delete"
	if trash {
		verb = "Trash"
	}

	m.push(&confirmDialog{
		title:   verb,
		lines:   deleteLines(verb, targets, trash),
		buttons: []string{verb, "Cancel"},
		sel:     1,
		cancel:  1,
		danger:  true,
		onChoice: func(m *Model, choice int, _ bool) tea.Cmd {
			if choice != 0 {
				return nil
			}

			return run(m)
		},
	})

	return nil
}

// deleteLines lists what is about to be deleted, abbreviated for long selections.
func deleteLines(verb string, targets []vfs.Entry, trash bool) []string {
	const shown = 6

	lines := []string{verb + " " + describe(targets) + "?"}

	for i, e := range targets {
		if i == shown {
			lines = append(lines, fmt.Sprintf("  … and %d more", len(targets)-i))

			break
		}

		lines = append(lines, "  "+e.Name)
	}

	if !trash {
		lines = append(lines, "", "This cannot be undone.")
	}

	return lines
}

// quickOp runs a short filesystem operation in the background and commits archives.
func (m *Model) quickOp(f vfs.FS, selectName string, op func(ctx context.Context) error) tea.Cmd {
	held := m.hold(f)
	ctx := m.ctx
	id := m.panel().id

	return func() tea.Msg {
		err := op(ctx)

		if c, ok := f.(vfs.Committer); ok && c.Dirty() {
			if cerr := c.Commit(ctx); err == nil {
				err = cerr
			}
		}

		return opDoneMsg{err: err, fsID: f.ID(), selectName: selectName, panel: id, held: held}
	}
}

func (m *Model) applyOpDone(msg opDoneMsg) tea.Cmd {
	for _, f := range msg.held {
		m.release(f)
	}

	var cmds []tea.Cmd

	if msg.err != nil {
		cmds = append(cmds, m.errorToast(msg.err))
	}

	if msg.info != "" {
		cmds = append(cmds, m.toast(msg.info, levelInfo))
	}

	for i, p := range m.panels {
		if p.fs == nil || p.fs.ID() != msg.fsID {
			continue
		}

		req := listedMsg{fs: p.fs, dir: p.dir, keepCursor: true}

		if p.id == msg.panel {
			req.selectName = msg.selectName
		}

		if p.search != nil {
			cmds = append(cmds, m.pruneSearch(i))

			continue
		}

		cmds = append(cmds, m.list(i, req))
	}

	return tea.Batch(cmds...)
}

func existsErr(ctx context.Context, f vfs.FS, p string) error {
	if _, err := f.Stat(ctx, p); err == nil {
		return fmt.Errorf("%s already exists", path.Base(p))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return nil
}

func validName(v string) error {
	if v == "" || v == "." || v == ".." || strings.Contains(v, "\x00") {
		return fmt.Errorf("invalid name %q", v)
	}

	return nil
}

func (m *Model) renameDialog() tea.Cmd {
	p := m.panel()

	// the filesystem is fixed when the dialog opens; the panel may switch while it is up
	srcFS := p.fs

	e, ok := p.current()
	if !ok || isParent(e) {
		return nil
	}

	name := path.Base(e.Path)
	in := newInput(name)

	// place the cursor before the extension, like most file managers
	if ext := path.Ext(name); ext != "" && ext != name && !e.IsDir {
		in.pos = len([]rune(strings.TrimSuffix(name, ext)))
	}

	m.push(&inputDialog{
		title: "Rename",
		label: e.Name,
		input: in,
		onSubmit: func(m *Model, v string) tea.Cmd {
			v = strings.TrimSpace(v)

			if v == name {
				return nil
			}

			if err := validName(v); err != nil || strings.Contains(v, "/") {
				return m.toast("invalid name", levelError)
			}

			dir := path.Dir(e.Path)
			to := path.Join(dir, v)

			return m.quickOp(srcFS, v, func(ctx context.Context) error {
				if err := existsErr(ctx, srcFS, to); err != nil {
					return err
				}

				return srcFS.Rename(ctx, e.Path, to)
			})
		},
	})

	return nil
}

func (m *Model) mkdirDialog() tea.Cmd {
	p := m.panel()

	// the filesystem is fixed when the dialog opens; the panel may switch while it is up
	srcFS := p.fs
	srcDir := panelDir(p)

	m.push(&inputDialog{
		title: "New directory",
		label: "in " + m.placeText(srcFS, srcDir) + " (use / for nested dirs)",
		input: newInput(""),
		onSubmit: func(m *Model, v string) tea.Cmd {
			v = strings.Trim(strings.TrimSpace(v), "/")

			if err := validName(v); err != nil {
				return m.errorToast(err)
			}

			target := path.Join(srcDir, v)
			first, _, _ := strings.Cut(v, "/")

			return m.quickOp(srcFS, first, func(ctx context.Context) error {
				if err := existsErr(ctx, srcFS, target); err != nil {
					return err
				}

				return srcFS.MkdirAll(ctx, target, 0o755)
			})
		},
	})

	return nil
}

func (m *Model) newFileDialog() tea.Cmd {
	p := m.panel()

	// the filesystem is fixed when the dialog opens; the panel may switch while it is up
	srcFS := p.fs
	srcDir := panelDir(p)

	m.push(&inputDialog{
		title: "New file",
		label: "in " + m.placeText(srcFS, srcDir),
		input: newInput(""),
		onSubmit: func(m *Model, v string) tea.Cmd {
			v = strings.TrimSpace(v)

			if err := validName(v); err != nil || strings.Contains(v, "/") {
				return m.toast("invalid name", levelError)
			}

			target := path.Join(srcDir, v)

			return m.quickOp(srcFS, v, func(ctx context.Context) error {
				if err := existsErr(ctx, srcFS, target); err != nil {
					return err
				}

				w, err := srcFS.Create(ctx, target, 0o644)
				if err != nil {
					return err
				}

				return w.Close()
			})
		},
	})

	return nil
}

func (m *Model) copyPath() tea.Cmd {
	p := m.panel()
	targets := p.targets()

	if len(targets) == 0 {
		targets = []vfs.Entry{{Path: p.dir}}
	}

	prefix := ""

	if s, ok := p.fs.(*sftpfs.FS); ok {
		t := s.Target()
		prefix = t.User + "@" + t.Alias + ":"
	}

	lines := make([]string, len(targets))

	for i, e := range targets {
		lines[i] = prefix + e.Path
	}

	text := strings.Join(lines, "\n")

	return tea.Batch(tea.SetClipboard(text), m.toast("copied "+describePaths(lines), levelInfo))
}

func describePaths(ps []string) string {
	if len(ps) == 1 {
		return ps[0]
	}

	return fmt.Sprintf("%d paths", len(ps))
}

// archiveStem strips a known archive suffix from a file name.
func archiveStem(name string) string {
	lower := strings.ToLower(name)

	for _, s := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".tgz", ".tbz2", ".txz", ".tzst", ".tar", ".zip", ".jar", ".7z", ".rar"} {
		if strings.HasSuffix(lower, s) {
			return name[:len(name)-len(s)]
		}
	}

	return name
}

func isArchiveName(name string) bool {
	return archiveStem(name) != name
}

func (m *Model) packDialog() tea.Cmd {
	p := m.panel()

	// the filesystem is fixed when the dialog opens; the panel may switch while it is up
	srcFS := p.fs
	srcDir := panelDir(p)
	targets := p.targets()

	if len(targets) == 0 {
		return nil
	}

	base := path.Base(targets[0].Path)
	if len(targets) > 1 {
		base = path.Base(srcDir)
	}

	if base == "/" || base == "." {
		base = "archive"
	}

	m.push(&inputDialog{
		title: "Pack " + describe(targets),
		label: "Archive name — the extension picks the format (.zip .tar .tar.gz .tar.xz .tar.zst .tar.bz2)",
		input: newInput(base + ".zip"),
		width: 72,
		onSubmit: func(m *Model, v string) tea.Cmd {
			v = strings.TrimSpace(v)

			if err := validName(v); err != nil {
				return m.errorToast(err)
			}

			target := path.Join(srcDir, v)
			src := srcFS
			held := m.hold(src)
			ctx := m.ctx
			srcPaths := paths(targets)

			clear(p.selected)

			return func() tea.Msg {
				if err := existsErr(ctx, src, target); err != nil {
					return enqueueMsg{held: held, err: err}
				}

				arc, err := archive.NewEmpty(ctx, src, target)
				if err != nil {
					return enqueueMsg{held: held, err: err}
				}

				spec := jobs.Spec{
					Op: jobs.OpCopy, Src: src, Paths: srcPaths, Dst: arc, DstDir: "/",
					Title: "pack " + path.Base(target),
				}

				return enqueueMsg{spec: spec, held: held, fresh: []vfs.FS{arc}}
			}
		},
	})

	return nil
}

// extractDialog copies out of an open archive, or unpacks the archive under the cursor
// into a directory in the other panel.
func (m *Model) extractDialog() tea.Cmd {
	p, o := m.panel(), m.other()

	// filesystems are fixed when the dialog opens; a panel may switch while it is up
	srcFS, dstFS := p.fs, o.fs
	srcDir := panelDir(p)

	if srcFS.Kind() == vfs.KindArchive {
		return m.transferDialog(false)
	}

	e, ok := p.current()
	if !ok || isParent(e) || !isArchiveName(e.Name) || dstFS == nil {
		return m.toast("place the cursor on an archive to extract it", levelWarn)
	}

	m.push(&inputDialog{
		title: "Extract " + path.Base(e.Name),
		label: "into directory on " + fsName(dstFS),
		input: newInput(withSlash(panelDir(o)) + archiveStem(path.Base(e.Name)) + "/"),
		width: 72,
		onSubmit: func(m *Model, v string) tea.Cmd {
			v = strings.TrimSpace(v)
			if v == "" {
				return nil
			}

			dst, dstDir := dstFS, path.Clean(v)

			if !strings.HasPrefix(v, "/") {
				dst, dstDir = srcFS, path.Join(srcDir, v)
			}

			src := srcFS
			held := m.hold(src, dst)
			ctx := m.ctx

			return func() tea.Msg {
				arc, err := archive.Open(ctx, src, e.Path)
				if err != nil {
					return enqueueMsg{held: held, err: err}
				}

				root, err := arc.ReadDir(ctx, "/")
				if err != nil {
					_ = arc.Close()

					return enqueueMsg{held: held, err: err}
				}

				spec := jobs.Spec{
					Op: jobs.OpCopy, Src: arc, Paths: paths(root), Dst: dst, DstDir: dstDir,
					Title: "extract " + path.Base(e.Name),
				}

				return enqueueMsg{spec: spec, held: held, fresh: []vfs.FS{arc}}
			}
		},
	})

	return nil
}

// nextConflict shows the oldest unanswered conflict, one dialog at a time.
func (m *Model) nextConflict() tea.Cmd {
	if len(m.conflicts) == 0 {
		return nil
	}

	for _, o := range m.overlays {
		if _, ok := o.(*conflictDialog); ok {
			return nil
		}
	}

	c := m.conflicts[0]
	m.conflicts = m.conflicts[1:]

	m.push(&conflictDialog{confirmDialog{
		opened:   timeNow(),
		title:    "Target exists",
		lines:    conflictLines(c),
		buttons:  []string{"Overwrite", "Skip", "Rename", "Newer only", "Cancel job"},
		sel:      1,
		cancel:   1,
		check:    "apply to all remaining conflicts of this job",
		onChoice: func(m *Model, choice int, all bool) tea.Cmd { return m.answerConflict(c, choice, all) },
	}})

	return nil
}

func conflictLines(c jobs.Conflict) []string {
	now := timeNow()
	desc := func(e vfs.Entry) string {
		size := "dir"
		if !e.IsDir {
			size = humanSize(e.Size)
		}

		return fmt.Sprintf("%-9s %s", size, shortTime(e.ModTime, now))
	}

	newer := ""
	if c.Src.ModTime.After(c.Dst.ModTime) {
		newer = "  (newer)"
	}

	return []string{
		c.Dst.Path,
		"",
		"incoming   " + desc(c.Src) + newer,
		"existing   " + desc(c.Dst),
	}
}

// answerConflict replies to the job; an answer for all also settles that job's
// conflicts already waiting in line.
func (m *Model) answerConflict(c jobs.Conflict, choice int, all bool) tea.Cmd {
	res := []jobs.Resolution{jobs.Overwrite, jobs.Skip, jobs.RenameNew, jobs.OverwriteIfNewer, jobs.CancelJob}[choice]

	m.queue.Answer(c.JobID, jobs.Answer{Res: res, All: all})

	if all {
		m.conflicts = slices.DeleteFunc(m.conflicts, func(other jobs.Conflict) bool {
			return other.JobID == c.JobID
		})
	}

	return func() tea.Msg { return nextConflictMsg{} }
}

// conflictDialog marks the confirm dialog that answers job conflicts.
type conflictDialog struct {
	confirmDialog
}

type nextConflictMsg struct{}
