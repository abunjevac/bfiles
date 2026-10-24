package preview_test

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/preview"
	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/local"
)

func render(t *testing.T, p string, rules []config.PreviewRule) preview.Result {
	t.Helper()

	e, err := local.New().Stat(context.Background(), p)
	require.NoError(t, err)

	return preview.Render(context.Background(), preview.Request{
		FS: local.New(), Entry: e, Width: 40, Height: 10, Style: "tokyonight-night", Rules: rules,
	})
}

func TestTextHighlighted(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "main.go")

	require.NoError(t, os.WriteFile(p, []byte("package main\n\nfunc main() {\n\tprintln(1)\n}\n"), 0o600))

	r := render(t, p, nil)

	require.Equal(t, preview.KindText, r.Kind)
	require.Equal(t, "Go", r.Title)
	require.Len(t, r.Lines, 5)
	require.Contains(t, r.Lines[0], "\x1b[")
	require.Contains(t, stripANSI(r.Lines[0]), "package main")
	require.Contains(t, stripANSI(r.Lines[3]), "    println", "tabs are expanded")
	require.NotContains(t, strings.Join(r.Lines, ""), "48;2;", "background colours are stripped")
}

func TestTextLimitedToHeight(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "long.txt")

	require.NoError(t, os.WriteFile(p, []byte(strings.Repeat("line\n", 100)), 0o600))

	r := render(t, p, nil)

	require.Len(t, r.Lines, 10)
	require.True(t, r.Truncated)
}

func TestEscapesSanitized(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "evil.txt")

	require.NoError(t, os.WriteFile(p, []byte("a\x1b]0;title\x07b\x1b[2Jc\x1b[31md\n"), 0o600))

	r := render(t, p, nil)

	require.Equal(t, "abcd", stripANSI(r.Lines[0]))
	require.NotContains(t, r.Lines[0], "]0;")
	require.NotContains(t, r.Lines[0], "[2J")
}

func TestBinaryHex(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "blob")

	require.NoError(t, os.WriteFile(p, []byte{0, 1, 2, 'A', 0xff}, 0o600))

	r := render(t, p, nil)

	require.Equal(t, preview.KindBinary, r.Kind)
	require.Contains(t, r.Lines[0], "00 01 02 41 ff")
}

func TestImageHalfBlocks(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "pic.png")
	img := image.NewRGBA(image.Rect(0, 0, 80, 40))

	for x := range 80 {
		for y := range 40 {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}

	f, err := os.Create(p)
	require.NoError(t, err)
	require.NoError(t, png.Encode(f, img))
	require.NoError(t, f.Close())

	r := render(t, p, nil)

	require.Equal(t, preview.KindImage, r.Kind)
	require.Contains(t, r.Title, "80×40")
	require.LessOrEqual(t, len(r.Lines), 10)
	require.Contains(t, r.Lines[0], "▀")
	require.Contains(t, r.Lines[0], "38;2;255;0;0")
}

func TestDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "b"), nil, 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "a"), 0o755))

	r := render(t, dir, nil)

	require.Equal(t, preview.KindEntries, r.Kind)
	require.Equal(t, []string{"a", "b"}, names(r.Entries))
}

func TestExternalRuleAndFallback(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "doc.md")

	require.NoError(t, os.WriteFile(p, []byte("# Title\n"), 0o600))

	r := render(t, p, []config.PreviewRule{{Match: []string{"*.MD"}, Command: []string{"echo", "rendered:{file}"}}})
	require.Equal(t, "rendered:"+p, stripANSI(r.Lines[0]))

	r = render(t, p, []config.PreviewRule{{Match: []string{"*.md"}, Command: []string{"no-such-previewer-xyz", "{file}"}}})
	require.Equal(t, preview.KindText, r.Kind)
	require.Contains(t, stripANSI(r.Lines[0]), "# Title")
}

func TestMissingFile(t *testing.T) {
	t.Parallel()

	r := preview.Render(context.Background(), preview.Request{
		FS: local.New(), Entry: vfs.Entry{Name: "x", Path: "/nonexistent/x"}, Width: 10, Height: 5,
	})

	require.Equal(t, preview.KindError, r.Kind)
}

func names(entries []vfs.Entry) []string {
	out := make([]string, len(entries))

	for i, e := range entries {
		out[i] = e.Name
	}

	return out
}

func stripANSI(s string) string {
	var sb strings.Builder

	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			for i < len(s) && s[i] != 'm' {
				i++
			}

			continue
		}

		sb.WriteByte(s[i])
	}

	return sb.String()
}
