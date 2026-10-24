package dirsize

import (
	"context"
	"sync"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// Result is the total size of a tree.
type Result struct {
	Bytes int64
	Files int
	Dirs  int
	// Errors counts unreadable directories; their contents are missing from Bytes.
	Errors int
}

// Calc sums the tree below root without following symlinks. Top-level subdirectories
// are walked by up to workers goroutines; disk selects allocated instead of apparent size.
func Calc(ctx context.Context, fsys vfs.FS, root string, workers int, disk bool) (Result, error) {
	children, err := fsys.ReadDir(ctx, root)
	if err != nil {
		return Result{}, err
	}

	var (
		mu    sync.Mutex
		total = Result{Dirs: 1}
		wg    sync.WaitGroup
		sem   = make(chan struct{}, max(workers, 1))
	)

	add := func(r Result) {
		mu.Lock()
		defer mu.Unlock()

		total.Bytes += r.Bytes
		total.Files += r.Files
		total.Dirs += r.Dirs
		total.Errors += r.Errors
	}

	for _, c := range children {
		if !c.IsDir || c.IsLink {
			add(Result{Bytes: size(c, disk), Files: 1})

			continue
		}

		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()

			return total, ctx.Err()
		}

		wg.Go(func() {
			defer func() { <-sem }()

			add(walk(ctx, fsys, c.Path, disk))
		})
	}

	wg.Wait()

	return total, ctx.Err()
}

func walk(ctx context.Context, fsys vfs.FS, root string, disk bool) Result {
	var r Result

	_ = vfs.Walk(ctx, fsys, root, func(e vfs.Entry, err error) error {
		if err != nil {
			r.Errors++

			return nil //nolint:nilerr // unreadable entries are counted, not fatal
		}

		if e.IsDir && !e.IsLink {
			r.Dirs++

			return nil
		}

		r.Files++
		r.Bytes += size(e, disk)

		return nil
	})

	return r
}

func size(e vfs.Entry, disk bool) int64 {
	if disk && e.Disk > 0 {
		return e.Disk
	}

	if e.IsLink {
		// count the link itself, not its target
		return int64(len(e.Link))
	}

	return e.Size
}
