package search

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// maxContentBytes caps how much of a file is scanned for content matches.
const maxContentBytes = 32 << 20

// chunkSize is how much of a file is searched at a time.
const chunkSize = 64 << 10

// batch limits how often results are handed to the UI.
const (
	batchSize     = 200
	batchInterval = 100 * time.Millisecond
)

// Options configure a search.
type Options struct {
	// Pattern matches names: a glob ("*.log"), a regular expression prefixed with
	// "re:", or plain text matched as a substring. Empty matches everything.
	Pattern       string
	Content       string
	CaseSensitive bool
	Hidden        bool
	// Skip lists directory names not descended into (special dirs).
	Skip []string
}

type matcher func(name string) bool

func compile(o Options) (matcher, error) {
	p := o.Pattern

	if p == "" {
		return func(string) bool { return true }, nil
	}

	if expr, ok := strings.CutPrefix(p, "re:"); ok {
		if !o.CaseSensitive {
			expr = "(?i)" + expr
		}

		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("invalid regular expression: %w", err)
		}

		return re.MatchString, nil
	}

	fold := func(s string) string {
		if o.CaseSensitive {
			return s
		}

		return strings.ToLower(s)
	}

	p = fold(p)

	if !strings.ContainsAny(p, "*?[") {
		return func(name string) bool { return strings.Contains(fold(name), p) }, nil
	}

	if _, err := path.Match(p, ""); err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	return func(name string) bool {
		ok, _ := path.Match(p, fold(name))

		return ok
	}, nil
}

// Run walks root and emits matching entries in batches. Entry.Name is the path
// relative to root; Entry.Path stays the real path.
func Run(ctx context.Context, fsys vfs.FS, root string, o Options, emit func([]vfs.Entry)) error {
	match, err := compile(o)
	if err != nil {
		return err
	}

	b := &batcher{emit: emit, last: time.Now()}

	err = vfs.Walk(ctx, fsys, root, func(e vfs.Entry, err error) error {
		if err != nil || e.Path == root {
			return nil //nolint:nilerr // unreadable directories are skipped
		}

		if !o.Hidden && strings.HasPrefix(e.Name, ".") {
			return skip(e)
		}

		if e.IsDir && slices.Contains(o.Skip, e.Name) {
			return vfs.SkipDir
		}

		if match(e.Name) && hasContent(ctx, fsys, e, o) {
			e.Name = strings.TrimPrefix(strings.TrimPrefix(e.Path, root), "/")
			b.pending = append(b.pending, e)
		}

		b.flush(false)

		return nil
	})

	b.flush(true)

	return err
}

// batcher collects results and hands them on in batches, so the UI is not
// flooded with one message per match.
type batcher struct {
	pending []vfs.Entry
	last    time.Time
	emit    func([]vfs.Entry)
}

// flush emits pending results once a batch is full or old enough, or always
// with force.
func (b *batcher) flush(force bool) {
	if len(b.pending) == 0 || (!force && len(b.pending) < batchSize && time.Since(b.last) < batchInterval) {
		return
	}

	b.emit(b.pending)

	b.pending = nil
	b.last = time.Now()
}

// hasContent reports whether e satisfies the content filter; without one every
// entry does.
func hasContent(ctx context.Context, fsys vfs.FS, e vfs.Entry, o Options) bool {
	return o.Content == "" || (!e.IsDir && contains(ctx, fsys, e, o))
}

func skip(e vfs.Entry) error {
	if e.IsDir {
		return vfs.SkipDir
	}

	return nil
}

func contains(ctx context.Context, fsys vfs.FS, e vfs.Entry, o Options) bool {
	if e.IsLink || e.Size == 0 {
		return false
	}

	r, err := fsys.Open(ctx, e.Path)
	if err != nil {
		return false
	}

	defer r.Close()

	needle := []byte(o.Content)

	if !o.CaseSensitive {
		needle = bytes.ToLower(needle)
	}

	return readContains(io.LimitReader(r, maxContentBytes), needle, !o.CaseSensitive)
}

// readContains searches r chunk by chunk, keeping the last len(needle)-1 bytes of
// each chunk so a match across a chunk boundary is found. With fold, chunks are
// lowered only up to complete UTF-8 sequences, so no rune is split.
func readContains(r io.Reader, needle []byte, fold bool) bool {
	var (
		keep    = len(needle) - 1
		window  = make([]byte, 0, chunkSize+keep+utf8.UTFMax)
		chunk   = make([]byte, chunkSize)
		partial []byte
	)

	for {
		n, err := r.Read(chunk)
		data := slices.Concat(partial, chunk[:n])

		partial = nil

		if fold {
			cut := runeBoundary(data, err != nil)
			partial = bytes.Clone(data[cut:])
			data = bytes.ToLower(data[:cut])
		}

		window = append(window, data...)

		if bytes.Contains(window, needle) {
			return true
		}

		if err != nil {
			return false
		}

		if len(window) > keep {
			window = window[:copy(window, window[len(window)-keep:])]
		}
	}
}

// runeBoundary returns where an incomplete UTF-8 sequence at the end of b starts,
// or len(b) when there is none or at the end of input.
func runeBoundary(b []byte, eof bool) int {
	if eof {
		return len(b)
	}

	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return i
			}

			break
		}
	}

	return len(b)
}
