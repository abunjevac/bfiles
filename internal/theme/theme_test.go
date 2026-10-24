package theme_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/theme"
)

func TestBuiltinsParse(t *testing.T) {
	t.Parallel()

	names := theme.BuiltinNames()

	require.Contains(t, names, theme.Fallback)
	require.Contains(t, names, "terminal")

	for _, name := range names {
		th, err := theme.Builtin(name)
		require.NoError(t, err, name)

		require.Equal(t, name, th.Name)
	}
}

func TestParseRejectsBadColor(t *testing.T) {
	t.Parallel()

	src, err := theme.BuiltinSource(theme.Fallback)
	require.NoError(t, err)

	bad := replaceLine(string(src), `accent = "#7aa2f7"`, `accent = "blue"`)

	_, err = theme.Parse("bad", bad)
	require.ErrorContains(t, err, "accent")
}

func TestParseRejectsMissingRole(t *testing.T) {
	t.Parallel()

	_, err := theme.Parse("empty", `dark = true`)
	require.Error(t, err)
}

func TestValidColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want bool
	}{
		{"#a1b2c3", true},
		{"#abc", false},
		{"0", true},
		{"255", true},
		{"256", false},
		{"-1", false},
		{"default", true},
		{"", false},
	}

	for _, tc := range tests {
		require.Equal(t, tc.want, theme.ValidColor(tc.in), tc.in)
	}
}

func TestLoadPrefersUserFileAndFallsBack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "themes"), 0o755))

	src, err := theme.BuiltinSource("nord")
	require.NoError(t, err)

	custom := replaceLine(string(src), `accent = "#88c0d0"`, `accent = "#123456"`)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "themes", "mine.toml"), []byte(custom), 0o600))

	th, err := theme.Load(dir, "mine")
	require.NoError(t, err)

	require.Equal(t, "#123456", th.Accent)

	th, err = theme.Load(dir, "does-not-exist")
	require.Error(t, err)

	require.Equal(t, theme.Fallback, th.Name)

	require.Contains(t, theme.List(dir), "mine")
}

func replaceLine(s, old, repl string) string {
	if !strings.Contains(s, old) {
		panic("line not found: " + old)
	}

	return strings.Replace(s, old, repl, 1)
}
