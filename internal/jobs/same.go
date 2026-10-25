package jobs

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// checkTargets rejects transfers that would overwrite a source with itself or
// copy a directory into its own subtree, also when the two paths reach the same
// item through symlinks or bind mounts.
func checkTargets(ctx context.Context, s Spec) error {
	if s.Rename != "" && len(s.Paths) != 1 {
		return errors.New("rename needs exactly one source")
	}

	if !vfs.Same(s.Src, s.Dst) {
		return nil
	}

	id, _ := s.Src.(vfs.Identifier)

	for _, p := range s.Paths {
		if err := checkTarget(ctx, s.Src, id, p, path.Join(s.DstDir, targetName(s, p))); err != nil {
			return err
		}
	}

	return nil
}

func checkTarget(ctx context.Context, fsys vfs.FS, id vfs.Identifier, src, target string) error {
	if err := checkPaths(src, target); err != nil {
		return err
	}

	if id == nil {
		return nil
	}

	realSrc, err := realLocation(ctx, id, src)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", src, err)
	}

	realTarget, err := realLocation(ctx, id, target)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", target, err)
	}

	if err := checkPaths(realSrc, realTarget); err != nil {
		return err
	}

	// bind mounts and hard links reach the same item under unrelated paths
	if se, err := fsys.Stat(ctx, src); err == nil && isRealDir(se) {
		return checkAncestors(ctx, id, src, target)
	}

	return nil
}

// checkPaths compares the paths themselves.
func checkPaths(src, target string) error {
	if target == src {
		return ErrSameFile
	}

	if vfs.IsWithin(target, src) {
		return ErrIntoItself
	}

	return nil
}

// checkAncestors reports whether target or one of its parents is the directory src.
func checkAncestors(ctx context.Context, id vfs.Identifier, src, target string) error {
	if id.SameFile(ctx, src, target) {
		return ErrSameFile
	}

	for d := path.Dir(target); ; d = path.Dir(d) {
		if id.SameFile(ctx, src, d) {
			return ErrIntoItself
		}

		if d == "/" || d == "." {
			return nil
		}
	}
}

// realLocation resolves the parents of p but not p itself, so a symlink names
// where the link is, not what it points to.
func realLocation(ctx context.Context, id vfs.Identifier, p string) (string, error) {
	dir, err := id.RealPath(ctx, path.Dir(p))
	if err != nil {
		return "", err
	}

	return path.Join(dir, path.Base(p)), nil
}

// sameItem reports whether the existing target de at dst is the source src under
// another name, so replacing dst would destroy src. A symlink target is replaced,
// never written through, so only its own location counts.
func sameItem(ctx context.Context, s Spec, src, dst string, de vfs.Entry) bool {
	if !vfs.Same(s.Src, s.Dst) {
		return false
	}

	id, ok := s.Dst.(vfs.Identifier)
	if !ok {
		return false
	}

	if !de.IsLink {
		return id.SameFile(ctx, src, dst)
	}

	rs, err := realLocation(ctx, id, src)
	if err != nil {
		return true
	}

	rd, err := realLocation(ctx, id, dst)

	// when in doubt, refuse rather than risk the source
	return err != nil || rs == rd
}
