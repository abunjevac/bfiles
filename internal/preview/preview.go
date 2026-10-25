package preview

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/icons"
	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/archive"
)

// Kind tells the UI how to present a result.
type Kind int

// Result kinds.
const (
	KindText Kind = iota
	KindImage
	KindEntries
	KindBinary
	KindInfo
	KindError
)

// Request asks for a preview of one entry.
type Request struct {
	FS     vfs.FS
	Entry  vfs.Entry
	Width  int
	Height int
	// MaxLines bounds text output; 0 means Height (quick view). The viewer asks for more.
	MaxLines int
	// Style is the chroma style used for highlighting.
	Style    string
	MaxBytes int64
	Rules    []config.PreviewRule
}

// Result is a rendered preview. Lines may contain SGR colour sequences only.
type Result struct {
	Kind      Kind
	Title     string
	Lines     []string
	Entries   []vfs.Entry
	Truncated bool
	Err       error
}

// Render produces a preview, trying external rules first, then builtin renderers.
// It never returns an error: failures become a KindError result.
func Render(ctx context.Context, req Request) Result {
	req.MaxLines = max(req.MaxLines, req.Height)

	if req.MaxBytes <= 0 {
		req.MaxBytes = 1 << 20
	}

	e := req.Entry

	if e.IsDir {
		return dirPreview(ctx, req)
	}

	if r, ok := external(ctx, req); ok {
		return r
	}

	if r, ok := mediaPreview(ctx, req); ok {
		return r
	}

	head, truncated, err := readHead(ctx, req.FS, e.Path, req.MaxBytes)
	if err != nil {
		return errorResult(err)
	}

	if len(head) == 0 {
		return Result{Kind: KindInfo, Title: "empty file"}
	}

	if !isText(head) {
		return hexPreview(head, truncated, req)
	}

	return textPreview(head, truncated, req)
}

// mediaPreview renders archives, images and videos by file name; ok is false when
// the file should be previewed by content instead.
func mediaPreview(ctx context.Context, req Request) (Result, bool) {
	name := req.Entry.Name

	switch {
	case icons.IsArchive(name):
		return archivePreview(ctx, req), true
	case icons.IsImage(name):
		return imagePreview(ctx, req), true
	case icons.IsVideo(name):
		return videoPreview(ctx, req)
	default:
		return Result{}, false
	}
}

func errorResult(err error) Result {
	return Result{Kind: KindError, Title: "preview failed", Err: err, Lines: []string{err.Error()}}
}

func readHead(ctx context.Context, fsys vfs.FS, p string, limit int64) ([]byte, bool, error) {
	r, err := fsys.Open(ctx, p)
	if err != nil {
		return nil, false, err
	}

	defer r.Close()

	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}

	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}

	return data, false, nil
}

// isText treats data as text when it is valid UTF-8 (allowing a cut final rune)
// without NUL bytes.
func isText(data []byte) bool {
	sample := data[:min(len(data), 8192)]

	if bytes.IndexByte(sample, 0) >= 0 {
		return false
	}

	for len(sample) > 0 {
		r, size := utf8.DecodeRune(sample)
		if r == utf8.RuneError && size == 1 {
			// a rune cut by the sample boundary is fine
			return len(sample) < utf8.UTFMax
		}

		sample = sample[size:]
	}

	return true
}

func dirPreview(ctx context.Context, req Request) Result {
	entries, err := req.FS.ReadDir(ctx, req.Entry.Path)
	if err != nil {
		return errorResult(err)
	}

	sortEntries(entries)

	return Result{Kind: KindEntries, Title: fmt.Sprintf("%d entries", len(entries)), Entries: entries}
}

func archivePreview(ctx context.Context, req Request) Result {
	a, err := archive.Open(ctx, req.FS, req.Entry.Path)
	if err != nil {
		return errorResult(err)
	}

	defer a.Close()

	entries, err := a.ReadDir(ctx, "/")
	if err != nil {
		return errorResult(err)
	}

	sortEntries(entries)

	return Result{Kind: KindEntries, Title: fmt.Sprintf("archive · %d entries at top level", len(entries)), Entries: entries}
}

func sortEntries(entries []vfs.Entry) {
	slices.SortFunc(entries, func(a, b vfs.Entry) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}

			return 1
		}

		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}

func hexPreview(data []byte, truncated bool, req Request) Result {
	total := (len(data) + 15) / 16
	rows := min(req.MaxLines, total)
	lines := make([]string, 0, rows)

	for i := range rows {
		chunk := data[i*16 : min(len(data), i*16+16)]

		var hexPart, textPart strings.Builder

		for k := range 16 {
			if k < len(chunk) {
				fmt.Fprintf(&hexPart, "%02x ", chunk[k])

				if chunk[k] >= 32 && chunk[k] < 127 {
					textPart.WriteByte(chunk[k])
				} else {
					textPart.WriteByte('.')
				}
			} else {
				hexPart.WriteString("   ")
			}

			if k == 7 {
				hexPart.WriteByte(' ')
			}
		}

		lines = append(lines, fmt.Sprintf("\x1b[2m%08x\x1b[22m  %s %s", i*16, hexPart.String(), textPart.String()))
	}

	return Result{Kind: KindBinary, Title: "binary", Lines: lines, Truncated: truncated || rows < total}
}
