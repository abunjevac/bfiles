package preview

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/abunjevac/bfiles/internal/launch"
	"github.com/abunjevac/bfiles/internal/vfs"
)

// externalTimeout bounds how long a previewer may run.
const externalTimeout = 5 * time.Second

// externalWaitDelay bounds the wait for output pipes after the previewer exits or
// is killed.
const externalWaitDelay = time.Second

// maxExternalOutput bounds how much previewer output is kept.
const maxExternalOutput = 2 << 20

// maxRemoteCopy bounds how much of a non-local file is fetched for a previewer.
const maxRemoteCopy = 64 << 20

// external runs the first matching rule. It reports false when no rule matched or the
// tool is missing or failed, so builtin previews take over.
func external(ctx context.Context, req Request) (Result, bool) {
	rule, ok := matchRule(req)
	if !ok || !launch.Available(rule) {
		return Result{}, false
	}

	file, cleanup, err := localCopy(ctx, req)
	if err != nil {
		return Result{}, false
	}

	defer cleanup()

	argv := launch.Expand(rule, launch.Vars{Values: map[string]string{
		"file":   file,
		"width":  strconv.Itoa(req.Width),
		"height": strconv.Itoa(req.Height),
	}})

	ctx, cancel := context.WithTimeout(ctx, externalTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // user-configured previewer

	// a child that inherits stdout would otherwise keep Run waiting past the timeout
	cmd.WaitDelay = externalWaitDelay

	cmd.Env = append(os.Environ(), "COLUMNS="+strconv.Itoa(req.Width), "LINES="+strconv.Itoa(req.Height))

	var out bytes.Buffer

	cmd.Stdout = &limitedWriter{w: &out, n: maxExternalOutput}

	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return Result{}, false
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	truncated := len(lines) > req.MaxLines

	if truncated {
		lines = lines[:req.MaxLines]
	}

	for i, l := range lines {
		lines[i] = sanitize(strings.TrimSuffix(l, "\r"), false) + "\x1b[0m"
	}

	return Result{Kind: KindText, Title: path.Base(argv[0]), Lines: lines, Truncated: truncated}, true
}

func matchRule(req Request) ([]string, bool) {
	name := strings.ToLower(req.Entry.Name)

	for _, r := range req.Rules {
		for _, m := range r.Match {
			if ok, _ := path.Match(strings.ToLower(m), name); ok {
				return r.Command, true
			}
		}
	}

	return nil, false
}

// localCopy returns a local path for the entry, downloading non-local files.
func localCopy(ctx context.Context, req Request) (string, func(), error) {
	if l, ok := req.FS.(vfs.Localizer); ok {
		if p, ok := l.LocalPath(req.Entry.Path); ok {
			return p, func() {}, nil
		}
	}

	if req.Entry.Size > maxRemoteCopy {
		return "", nil, errors.New("file too large to preview remotely")
	}

	dir, err := os.MkdirTemp("", "bfiles-preview-*")
	if err != nil {
		return "", nil, err
	}

	cleanup := func() { _ = os.RemoveAll(dir) }

	r, err := req.FS.Open(ctx, req.Entry.Path)
	if err != nil {
		cleanup()

		return "", nil, err
	}

	defer r.Close()

	dst := path.Join(dir, req.Entry.Name)

	f, err := os.Create(dst)
	if err != nil {
		cleanup()

		return "", nil, err
	}

	_, err = io.Copy(f, r)

	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		cleanup()

		return "", nil, err
	}

	return dst, cleanup, nil
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}

	keep := p[:min(len(p), l.n)]
	l.n -= len(keep)

	if _, err := l.w.Write(keep); err != nil {
		return 0, err
	}

	return len(p), nil
}
