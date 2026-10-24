package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/config"
)

func TestParseDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse("")
	require.NoError(t, err)

	require.Equal(t, "tokyonight", cfg.Theme)
	require.Equal(t, config.IconsNerd, cfg.Icons)
	require.Equal(t, 1, cfg.ParallelJobs)
	require.True(t, cfg.ConfirmDelete)
	require.True(t, cfg.Panels.ShowMtime)
	require.Contains(t, cfg.Panels.SpecialDirs, ".git")
	require.Contains(t, cfg.Panels.SpecialDirs, "node_modules")
	require.Equal(t, []string{"bterm", "-w", "{cwd}"}, cfg.Terminal)
	require.Len(t, cfg.Preview.Rules, 2)
}

func TestDefaultTOMLMatchesParseDefaults(t *testing.T) {
	t.Parallel()

	fromEmpty, err := config.Parse("")
	require.NoError(t, err)

	fromFile, err := config.Parse(config.DefaultTOML)
	require.NoError(t, err)

	require.Equal(t, fromEmpty, fromFile)
}

func TestParseExplicitValuesWin(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse("confirm_delete = false\nterminal = []\n[panels]\nspecial_dirs = []\n[preview]\nrule = []\n")
	require.NoError(t, err)

	require.False(t, cfg.ConfirmDelete)
	require.Empty(t, cfg.Terminal)
	require.Empty(t, cfg.Panels.SpecialDirs)
	require.Empty(t, cfg.Preview.Rules)
}

func TestParseRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{"unknown key", `nope = 1`, "unknown config keys"},
		{"bad icons", `icons = "emoji"`, "invalid icons"},
		{"bad sort", "[panels]\nsort = \"random\"", "invalid panels.sort"},
		{"parallel range", `parallel_jobs = 99`, "invalid parallel_jobs"},
		{"rule without command", "[[preview.rule]]\nmatch = [\"*.x\"]", "needs both"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Parse(tc.data)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestSaveRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")

	cfg, err := config.Parse("")
	require.NoError(t, err)

	cfg.Theme = "nord"
	cfg.Bookmarks["work"] = "/srv/work"

	require.NoError(t, config.Save(path, cfg))

	loaded, err := config.LoadFile(path)
	require.NoError(t, err)

	require.Equal(t, cfg, loaded)
}

func TestCloneIsDeep(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse("")
	require.NoError(t, err)

	c := cfg.Clone()
	c.Panels.SpecialDirs[0] = "changed"
	c.Preview.Rules[0].Match[0] = "changed"

	require.Equal(t, ".git", cfg.Panels.SpecialDirs[0])
	require.Equal(t, "*.md", cfg.Preview.Rules[0].Match[0])
}

func TestScaffoldKeepsExistingFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	require.NoError(t, config.Scaffold(dir))

	for _, f := range []string{"config.toml", "keymap.toml", "themes/tokyonight.toml"} {
		require.FileExists(t, filepath.Join(dir, f))
	}

	custom := []byte("theme = \"nord\"\n")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), custom, 0o600))
	require.NoError(t, config.Scaffold(dir))

	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	require.NoError(t, err)

	require.Equal(t, custom, data)

	b, err := config.Load(dir)
	require.NoError(t, err)

	require.Equal(t, "nord", b.Config.Theme)

	km, err := b.LoadKeymap()
	require.NoError(t, err)

	require.NotNil(t, km)
}

func TestHistory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	h, err := config.LoadHistory(dir)
	require.NoError(t, err)

	h.Touch("a@one", now, 3)
	h.Touch("b@two", now.Add(time.Minute), 3)
	h.Touch("a@one", now.Add(2*time.Minute), 3)
	h.Touch("c@three", now.Add(3*time.Minute), 3)
	h.Touch("d@four", now.Add(4*time.Minute), 3)

	targets := make([]string, 0, len(h.Entries))

	for _, e := range h.Entries {
		targets = append(targets, e.Target)
	}

	require.Equal(t, []string{"d@four", "c@three", "a@one"}, targets)

	require.NoError(t, config.SaveHistory(dir, h))

	loaded, err := config.LoadHistory(dir)
	require.NoError(t, err)

	require.Equal(t, h.Entries, loaded.Entries)

	loaded.Remove("c@three")

	require.Len(t, loaded.Entries, 2)
}
