package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// partSuffix marks a file being written; it is renamed to its final name only when
// complete, so an interrupted copy never leaves a truncated file under the real name.
const partSuffix = ".bfiles-part"

// copyBufSize favours throughput on both local disks and SFTP.
const copyBufSize = 256 << 10

func (q *Queue) execute(ctx context.Context, j *job) error {
	switch j.spec.Op {
	case OpDelete:
		return q.runDelete(ctx, j)
	case OpCopy, OpMove:
		return q.runTransfer(ctx, j)
	}

	return fmt.Errorf("unknown operation %d", j.spec.Op)
}

// scan totals bytes and files below the sources for progress reporting.
func scan(ctx context.Context, fsys vfs.FS, paths []string) (int64, int, error) {
	var (
		bytes int64
		files int
	)

	for _, p := range paths {
		err := vfs.Walk(ctx, fsys, p, func(e vfs.Entry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // unreadable parts surface during the operation
			}

			files++

			if !e.IsDir {
				bytes += e.Size
			}

			return nil
		})
		if err != nil {
			return 0, 0, err
		}
	}

	return bytes, files, nil
}

func (q *Queue) runTransfer(ctx context.Context, j *job) error {
	s := j.spec

	if err := checkTargets(ctx, s); err != nil {
		return err
	}

	j.setCurrent("scanning…")

	bytes, files, err := scan(ctx, s.Src, s.Paths)
	if err != nil {
		return err
	}

	j.mu.Lock()
	j.total, j.filesTotal = bytes, files
	j.mu.Unlock()

	if err := s.Dst.MkdirAll(ctx, s.DstDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", s.DstDir, err)
	}

	for _, p := range s.Paths {
		if err := q.transferOne(ctx, j, p); err != nil {
			return err
		}
	}

	return nil
}

// transferOne copies or moves one source item into the destination directory.
func (q *Queue) transferOne(ctx context.Context, j *job, p string) error {
	if err := j.checkpoint(ctx); err != nil {
		return err
	}

	target := path.Join(j.spec.DstDir, targetName(j.spec, p))

	if done, err := q.tryRename(ctx, j, p, target); err != nil || done {
		return err
	}

	_, err := q.copyItem(ctx, j, p, target)

	return err
}

func targetName(s Spec, p string) string {
	if s.Rename != "" {
		return s.Rename
	}

	return baseName(p)
}

func baseName(p string) string {
	return path.Base(p)
}

// tryRename moves by renaming when source and destination share a filesystem
// that supports it; see moveByRename.
func (q *Queue) tryRename(ctx context.Context, j *job, src, dst string) (bool, error) {
	s := j.spec

	if s.Op != OpMove || !vfs.Same(s.Src, s.Dst) || !s.Src.Caps().Rename {
		return false, nil
	}

	return q.moveByRename(ctx, j, src, dst)
}

// moveByRename tries a cheap same-filesystem rename. It reports false when the caller
// should fall back to copy and delete (e.g. across devices).
func (q *Queue) moveByRename(ctx context.Context, j *job, src, dst string) (bool, error) {
	se, err := j.spec.Src.Stat(ctx, src)
	if err != nil {
		j.fail(err)

		return true, nil
	}

	j.setCurrent(src)

	if de, statErr := j.spec.Dst.Stat(ctx, dst); statErr == nil {
		if sameItem(ctx, j.spec, src, dst, de) {
			j.fail(fmt.Errorf("%s: %w", dst, ErrSameFile))

			return true, nil
		}

		if isRealDir(de) && isRealDir(se) {
			// merging into an existing directory needs a per-file walk
			return false, nil
		}

		target, ok, err := q.overwriteTarget(ctx, j, se, de, dst)
		if !ok {
			return true, err
		}

		dst = target
	}

	if err := j.spec.Src.Rename(ctx, src, dst); err != nil {
		return false, nil //nolint:nilerr // fall back to copy+delete
	}

	j.addDone(sizeOf(se))
	j.fileDone()

	return true, nil
}

func sizeOf(e vfs.Entry) int64 {
	if e.IsDir {
		return 0
	}

	return e.Size
}

// isRealDir reports whether e is a directory and not a symlink to one.
func isRealDir(e vfs.Entry) bool {
	return e.IsDir && !e.IsLink
}

// copyItem copies src (file, link or tree) to dst. It reports whether everything
// below src was copied. A move deletes each source item right after it was copied,
// so it never deletes anything that was not copied.
func (q *Queue) copyItem(ctx context.Context, j *job, src, dst string) (bool, error) {
	if err := j.checkpoint(ctx); err != nil {
		return false, err
	}

	se, err := j.spec.Src.Stat(ctx, src)
	if err != nil {
		j.fail(err)

		return false, nil
	}

	j.setCurrent(src)

	ok, err := q.copyEntry(ctx, j, se, dst)
	if err != nil || !ok || j.spec.Op != OpMove {
		return ok, err
	}

	return removeMoved(ctx, j, se), nil
}

// copyEntry dispatches on the kind of source entry.
func (q *Queue) copyEntry(ctx context.Context, j *job, se vfs.Entry, dst string) (bool, error) {
	if se.IsLink && j.spec.Dst.Caps().Symlink {
		return q.copyLink(ctx, j, se, dst)
	}

	if se.IsDir {
		if se.IsLink {
			j.fail(fmt.Errorf("%s: cannot copy a directory symlink to this filesystem", se.Path))

			return false, nil
		}

		return q.copyDir(ctx, j, se, dst)
	}

	return q.copyFile(ctx, j, se, dst)
}

// removeMoved deletes a moved source item once it was copied. A directory is only
// removed when empty: anything that appeared in it during the move stays.
func removeMoved(ctx context.Context, j *job, se vfs.Entry) bool {
	err := j.spec.Src.Remove(ctx, se.Path)
	if err == nil {
		return true
	}

	if isRealDir(se) {
		j.fail(fmt.Errorf("%s changed during the move; left in place: %w", se.Path, err))
	} else {
		j.fail(fmt.Errorf("remove %s after move: %w", se.Path, err))
	}

	return false
}

func (q *Queue) copyDir(ctx context.Context, j *job, se vfs.Entry, dst string) (bool, error) {
	s := j.spec

	dst, ok, err := q.prepareDir(ctx, j, se, dst)
	if !ok {
		return false, err
	}

	children, err := s.Src.ReadDir(ctx, se.Path)
	if err != nil {
		j.fail(err)

		return false, nil
	}

	complete := true

	for _, c := range children {
		ok, err := q.copyItem(ctx, j, c.Path, path.Join(dst, c.Name))
		if err != nil {
			return false, err
		}

		complete = complete && ok
	}

	_ = s.Dst.Chmod(ctx, dst, se.Mode.Perm())
	_ = s.Dst.Chtimes(ctx, dst, se.ModTime)

	j.fileDone()

	return complete, nil
}

// prepareDir makes sure dst is a real directory to copy into. An existing file or
// symlink (even one to a directory) is replaced, never written through.
func (q *Queue) prepareDir(ctx context.Context, j *job, se vfs.Entry, dst string) (string, bool, error) {
	s := j.spec

	de, statErr := s.Dst.Stat(ctx, dst)

	switch {
	case statErr != nil:
	case isRealDir(de):
		if sameItem(ctx, s, se.Path, dst, de) {
			j.fail(fmt.Errorf("%s: %w", dst, ErrSameFile))

			return "", false, nil
		}
	default:
		target, ok, err := q.replaceTarget(ctx, j, se, de, dst)
		if !ok {
			return "", false, err
		}

		dst = target
	}

	if err := s.Dst.MkdirAll(ctx, dst, se.Mode.Perm()|0o700); err != nil {
		j.fail(fmt.Errorf("create %s: %w", dst, err))

		return "", false, nil
	}

	return dst, true, nil
}

func (q *Queue) copyLink(ctx context.Context, j *job, se vfs.Entry, dst string) (bool, error) {
	s := j.spec

	// Stat fills in the link target; an empty one means it could not be read
	if se.Link == "" {
		j.fail(fmt.Errorf("%s: cannot read link target", se.Path))

		return false, nil
	}

	if de, statErr := s.Dst.Stat(ctx, dst); statErr == nil {
		if sameItem(ctx, s, se.Path, dst, de) {
			j.fail(fmt.Errorf("%s: %w", dst, ErrSameFile))

			return false, nil
		}

		target, ok, err := q.overwriteTarget(ctx, j, se, de, dst)
		if !ok {
			return false, err
		}

		// an existing target is replaced atomically, never removed first
		return placeLink(ctx, j, se.Link, target, target == dst), nil
	}

	return placeLink(ctx, j, se.Link, dst, false), nil
}

// placeLink creates a symlink at dst. With replace, the link is created under a
// temporary name and renamed over dst, so dst is never missing or lost on failure.
func placeLink(ctx context.Context, j *job, target, dst string, replace bool) bool {
	fsys := j.spec.Dst
	at := dst

	if replace {
		at = partName(dst)
		_ = fsys.Remove(ctx, at) // a stale leftover of an interrupted job
	}

	if err := fsys.Symlink(ctx, target, at); err != nil {
		j.fail(err)

		return false
	}

	if replace {
		if err := fsys.Rename(ctx, at, dst); err != nil {
			_ = fsys.Remove(ctx, at)

			j.fail(fmt.Errorf("replace %s: %w", dst, err))

			return false
		}
	}

	j.fileDone()

	return true
}

// partName is the temporary name dst is written under.
func partName(dst string) string {
	return path.Join(path.Dir(dst), "."+path.Base(dst)+partSuffix)
}

func (q *Queue) copyFile(ctx context.Context, j *job, se vfs.Entry, dst string) (bool, error) {
	s := j.spec

	if de, statErr := s.Dst.Stat(ctx, dst); statErr == nil {
		if sameItem(ctx, s, se.Path, dst, de) {
			j.fail(fmt.Errorf("%s: %w", dst, ErrSameFile))

			return false, nil
		}

		target, ok, err := q.overwriteTarget(ctx, j, se, de, dst)
		if !ok {
			return false, err
		}

		dst = target
	}

	part := partName(dst)

	written, err := q.transfer(ctx, j, se, part)
	if err != nil {
		// the partial file must go even when the job was canceled
		_ = s.Dst.Remove(context.WithoutCancel(ctx), part)

		if ctx.Err() != nil {
			return false, ctx.Err()
		}

		j.fail(fmt.Errorf("copy %s: %w", se.Path, err))

		return false, nil
	}

	if written != se.Size {
		_ = s.Dst.Remove(ctx, part)

		j.fail(fmt.Errorf("copy %s: size changed during copy (%d of %d bytes)", se.Path, written, se.Size))

		return false, nil
	}

	_ = s.Dst.Chtimes(ctx, part, se.ModTime)

	if err := s.Dst.Rename(ctx, part, dst); err != nil {
		_ = s.Dst.Remove(ctx, part)

		j.fail(fmt.Errorf("finish %s: %w", dst, err))

		return false, nil
	}

	j.fileDone()

	return true, nil
}

// overwriteTarget resolves a conflict with an existing target that is replaced by
// a rename. It returns the target to use, or false when the item is done with
// (skipped or failed) or err is set. A symlink target is replaced, not followed.
func (q *Queue) overwriteTarget(ctx context.Context, j *job, se, de vfs.Entry, dst string) (string, bool, error) {
	res, err := q.resolve(ctx, j, se, de)
	if err != nil {
		return "", false, err
	}

	switch res {
	case Skip:
		j.addDone(sizeOf(se))

		return "", false, nil
	case RenameNew:
		return freeName(ctx, j.spec.Dst, dst), true, nil
	case Overwrite:
		if isRealDir(de) {
			j.fail(fmt.Errorf("%s: target is a directory", dst))

			return "", false, nil
		}
	default:
		// resolve maps every other answer to Overwrite or Skip
	}

	return dst, true, nil
}

// replaceTarget resolves a conflict with an existing file or symlink that must be
// removed before a directory takes its place. It returns the target to use, or
// false when the item is skipped or failed, or err is set.
func (q *Queue) replaceTarget(ctx context.Context, j *job, se, de vfs.Entry, dst string) (string, bool, error) {
	res, err := q.resolve(ctx, j, se, de)
	if err != nil {
		return "", false, err
	}

	switch res {
	case Skip:
		return "", false, nil
	case RenameNew:
		return freeName(ctx, j.spec.Dst, dst), true, nil
	default:
		if err := j.spec.Dst.Remove(ctx, dst); err != nil {
			j.fail(err)

			return "", false, nil
		}

		return dst, true, nil
	}
}

func (q *Queue) transfer(ctx context.Context, j *job, se vfs.Entry, dst string) (int64, error) {
	r, err := j.spec.Src.Open(ctx, se.Path)
	if err != nil {
		return 0, err
	}

	defer r.Close()

	w, err := j.spec.Dst.Create(ctx, dst, se.Mode.Perm())
	if err != nil {
		return 0, err
	}

	buf := make([]byte, copyBufSize)

	var written int64

	for {
		if err := j.checkpoint(ctx); err != nil {
			_ = w.Close()

			return written, err
		}

		n, rerr := r.Read(buf)

		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				_ = w.Close()

				return written, err
			}

			written += int64(n)
			j.addDone(int64(n))
		}

		if errors.Is(rerr, io.EOF) {
			break
		}

		if rerr != nil {
			_ = w.Close()

			return written, rerr
		}
	}

	return written, w.Close()
}

// resolve asks how to handle an existing target, honouring a remembered "all" answer
// and an earlier answer for the same target, e.g. when a rename failed and the item
// is copied instead.
func (q *Queue) resolve(ctx context.Context, j *job, se, de vfs.Entry) (Resolution, error) {
	if j.spec.Overwrite {
		return Overwrite, nil
	}

	j.mu.Lock()
	policy := j.policy
	prev, decided := j.decided[de.Path]
	j.mu.Unlock()

	if decided {
		return prev, nil
	}

	res, err := q.decide(ctx, j, se, de, policy)
	if err != nil {
		return res, err
	}

	j.mu.Lock()

	if j.decided == nil {
		j.decided = map[string]Resolution{}
	}

	j.decided[de.Path] = res
	j.mu.Unlock()

	return res, nil
}

// decide applies the remembered policy or asks the user, mapping the answer to
// Overwrite, Skip or RenameNew.
func (q *Queue) decide(ctx context.Context, j *job, se, de vfs.Entry, policy *Resolution) (Resolution, error) {
	var res Resolution

	if policy != nil {
		res = *policy
	} else {
		a, err := q.ask(ctx, j, se, de)
		if err != nil {
			return Skip, err
		}

		res = a
	}

	switch res {
	case CancelJob:
		j.mu.Lock()
		j.canceled = true
		j.mu.Unlock()

		return Skip, context.Canceled
	case OverwriteIfNewer:
		if se.ModTime.After(de.ModTime) {
			return Overwrite, nil
		}

		return Skip, nil
	default:
		return res, nil
	}
}

// ask raises a conflict event and waits for the answer, remembering it for the
// rest of the job when it applies to all.
func (q *Queue) ask(ctx context.Context, j *job, se, de vfs.Entry) (Resolution, error) {
	select {
	case <-j.answer: // drop a stale answer
	default:
	}

	q.emit(Event{Conflict: &Conflict{JobID: j.id, Src: se, Dst: de}})

	select {
	case a := <-j.answer:
		if a.All {
			j.mu.Lock()
			j.policy = &a.Res
			j.mu.Unlock()
		}

		return a.Res, nil
	case <-ctx.Done():
		return Skip, ctx.Err()
	}
}

// freeName returns "name (n).ext" for the first n that does not exist.
func freeName(ctx context.Context, fsys vfs.FS, p string) string {
	dir, base := path.Split(p)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	if trimmed, ok := strings.CutSuffix(stem, ".tar"); ok {
		stem = trimmed
		ext = ".tar" + ext
	}

	for i := 1; ; i++ {
		cand := path.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))

		if _, err := fsys.Stat(ctx, cand); err != nil {
			return cand
		}
	}
}

func (q *Queue) runDelete(ctx context.Context, j *job) error {
	s := j.spec

	trasher, canTrash := s.Src.(vfs.Trasher)
	useTrash := s.Trash && canTrash

	if !useTrash {
		j.setCurrent("scanning…")

		_, files, err := scan(ctx, s.Src, s.Paths)
		if err != nil {
			return err
		}

		j.mu.Lock()
		j.filesTotal = files
		j.mu.Unlock()
	} else {
		j.mu.Lock()
		j.filesTotal = len(s.Paths)
		j.mu.Unlock()
	}

	for _, p := range s.Paths {
		if err := j.checkpoint(ctx); err != nil {
			return err
		}

		if useTrash {
			j.setCurrent(p)

			if err := trasher.Trash(ctx, p); err != nil {
				j.fail(err)
			}

			j.fileDone()

			continue
		}

		if err := removeTree(ctx, j, s.Src, p); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			j.fail(err)
		}
	}

	return nil
}

// removeTree deletes p and, for real directories, everything below it. Symlinks are
// removed themselves, never followed.
func removeTree(ctx context.Context, j *job, fsys vfs.FS, p string) error {
	if err := j.checkpoint(ctx); err != nil {
		return err
	}

	e, err := fsys.Stat(ctx, p)
	if err != nil {
		return err
	}

	if e.IsDir && !e.IsLink {
		if err := removeChildren(ctx, j, fsys, p); err != nil {
			return err
		}
	}

	j.setCurrent(p)

	if err := fsys.Remove(ctx, p); err != nil {
		return fmt.Errorf("remove %s: %w", p, err)
	}

	j.fileDone()

	return nil
}

// removeChildren deletes everything below directory p.
func removeChildren(ctx context.Context, j *job, fsys vfs.FS, p string) error {
	children, err := fsys.ReadDir(ctx, p)
	if err != nil {
		return err
	}

	for _, c := range children {
		if err := removeTree(ctx, j, fsys, c.Path); err != nil {
			return err
		}
	}

	return nil
}
