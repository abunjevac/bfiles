package preview_test

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/preview"
)

func TestImageHugeDimensionsRejected(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "bomb.png")

	require.NoError(t, os.WriteFile(p, pngHeader(50000, 50000), 0o600))

	r := render(t, p, nil)

	require.Equal(t, preview.KindError, r.Kind)
	require.ErrorContains(t, r.Err, "too large")
}

func TestSanitizeKeepsOnlyTextAndSGR(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"a\x1b[\x1b(0mqqq":    "amqqq",
		"\x1b[\x1b#8m":        "m",
		"\x1b[>4;2m":          "",
		"\x1b[?4m":            "",
		"x\x1b[\b\b\bm":       "xm",
		"\xc2\x9b2J":          "2J",
		"\xc2\x9d0;t\xc2\x9c": "0;t",
		"a\x9bb":              "ab",
		"é\x1b[1;31mx":        "é\x1b[1;31mx",
		"\x1b[38:2::1:2:3mx":  "\x1b[38:2::1:2:3mx",
		"\x1b[mx":             "\x1b[mx",
	}

	for in, want := range cases {
		require.Equal(t, want, preview.Sanitize(in, false), "%q", in)
	}
}

func TestSanitizeDropsColonBackground(t *testing.T) {
	t.Parallel()

	require.Equal(t, "\x1b[1mx", preview.Sanitize("\x1b[1;48:2::1:2:3mx", true))
}

func TestTextTruncatedWithoutTrailingNewline(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "short.txt")

	require.NoError(t, os.WriteFile(p, []byte(strings.Repeat("line\n", 10)+"last"), 0o600))

	r := render(t, p, nil)

	require.Len(t, r.Lines, 10)
	require.True(t, r.Truncated)

	require.NoError(t, os.WriteFile(p, []byte(strings.Repeat("line\n", 9)+"last"), 0o600))

	r = render(t, p, nil)

	require.Len(t, r.Lines, 10)
	require.False(t, r.Truncated)
}

func TestHexTruncated(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "blob")

	require.NoError(t, os.WriteFile(p, make([]byte, 16*11), 0o600))

	r := render(t, p, nil)

	require.Equal(t, preview.KindBinary, r.Kind)
	require.Len(t, r.Lines, 10)
	require.True(t, r.Truncated)
}

func TestExternalChildHoldingStdoutDoesNotBlock(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "doc.md")

	require.NoError(t, os.WriteFile(p, []byte("# Title\n"), 0o600))

	start := time.Now()
	r := render(t, p, []config.PreviewRule{{Match: []string{"*.md"}, Command: []string{"sh", "-c", "sleep 10 & echo hi"}}})

	require.Less(t, time.Since(start), 4*time.Second)
	require.Equal(t, "hi", stripANSI(r.Lines[0]))
}

// ihdrLen is the fixed IHDR payload size.
const ihdrLen = 13

// pngHeader returns a PNG signature and IHDR chunk declaring w×h RGBA pixels.
func pngHeader(w, h uint32) []byte {
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 6, 0, 0, 0)

	chunk := append([]byte("IHDR"), ihdr...)

	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, ihdrLen)
	out = append(out, chunk...)

	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(chunk))
}
