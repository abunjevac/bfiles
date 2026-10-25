package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/config"
)

const showHiddenKey = "panels.show_hidden"

func TestUpdateFilePreservesComments(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")

	require.NoError(t, os.WriteFile(path, []byte(config.DefaultTOML), 0o600))

	err := config.UpdateFile(path, []config.Change{
		{Key: "theme", Value: nordTheme},
		{Key: "parallel_jobs", Value: 3},
		{Key: "panels.special_dirs", Value: []string{".git", "build"}},
		{Key: showHiddenKey, Value: true},
		{Key: "bookmarks.my work", Value: "/srv/work"},
		{Key: "ssh.keepalive_sec", Value: 10},
	})
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	text := string(data)

	require.Contains(t, text, "# Theme name: a builtin")
	require.Contains(t, text, `theme = "nord"`)
	require.Contains(t, text, `special_dirs = [".git", "build"]`)
	require.NotContains(t, text, "node_modules", "the old multi-line array is replaced entirely")

	cfg, err := config.LoadFile(path)
	require.NoError(t, err)

	require.Equal(t, nordTheme, cfg.Theme)
	require.Equal(t, 3, cfg.ParallelJobs)
	require.True(t, cfg.Panels.ShowHidden)
	require.Equal(t, "/srv/work", cfg.Bookmarks["my work"])
	require.Equal(t, 10, cfg.SSH.KeepaliveSec)
	require.Len(t, cfg.Preview.Rules, 2, "array tables after edited sections stay intact")
}

func TestUpdateFileAddsMissingSectionAndRemoves(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")

	require.NoError(t, os.WriteFile(path, []byte("theme = \"nord\"\n"), 0o600))

	require.NoError(t, config.UpdateFile(path, []config.Change{{Key: "bookmarks.home", Value: "/home/x"}}))

	cfg, err := config.LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, "/home/x", cfg.Bookmarks["home"])

	require.NoError(t, config.UpdateFile(path, []config.Change{{Key: "bookmarks.home", Value: nil}}))

	cfg, err = config.LoadFile(path)
	require.NoError(t, err)
	require.Empty(t, cfg.Bookmarks)
}

func TestUpdateFileRejectsInvalid(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	orig := []byte("theme = \"nord\"\n")

	require.NoError(t, os.WriteFile(path, orig, 0o600))

	err := config.UpdateFile(path, []config.Change{{Key: "icons", Value: "emoji"}})
	require.ErrorContains(t, err, "invalid")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, orig, data)
}

func TestUpdateFileLooseHeadersAndQuotedKeys(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	orig := "theme = \"nord\"\n\n[ panels ] # view\nshow_hidden = false\n\n[bookmarks]\n\"a=b\" = \"/x\"\n'a\\b' = \"/y\"\n"

	require.NoError(t, os.WriteFile(path, []byte(orig), 0o600))

	require.NoError(t, config.UpdateFile(path, []config.Change{
		{Key: showHiddenKey, Value: true},
		{Key: "bookmarks.a=b", Value: "/z"},
		{Key: `bookmarks.a\b`, Value: nil},
	}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	require.Equal(t, 1, strings.Count(string(data), "show_hidden"), string(data))
	require.Equal(t, 1, strings.Count(string(data), "[bookmarks]"), string(data))

	cfg, err := config.LoadFile(path)
	require.NoError(t, err)

	require.True(t, cfg.Panels.ShowHidden)
	require.Equal(t, map[string]string{"a=b": "/z"}, cfg.Bookmarks)
}

func TestUpdateFileMultilineStrings(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	orig := "[bookmarks]\nnote = \"\"\"\n[panels]\nx = 1\n\"\"\"\nkeep = '''\n]]'''\n\n[panels]\nshow_hidden = false\n"

	require.NoError(t, os.WriteFile(path, []byte(orig), 0o600))

	require.NoError(t, config.UpdateFile(path, []config.Change{
		{Key: showHiddenKey, Value: true},
		{Key: "bookmarks.note", Value: nil},
	}))

	cfg, err := config.LoadFile(path)
	require.NoError(t, err)

	require.True(t, cfg.Panels.ShowHidden)
	require.Equal(t, map[string]string{"keep": "]]"}, cfg.Bookmarks)
}

func TestUpdateFileInsertsBeforeNextSectionComment(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	orig := "theme = \"nord\"\n\n# Panel settings.\n[panels]\nshow_hidden = true\n"

	require.NoError(t, os.WriteFile(path, []byte(orig), 0o600))

	require.NoError(t, config.UpdateFile(path, []config.Change{{Key: "parallel_jobs", Value: 3}}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	require.Equal(t, "theme = \"nord\"\nparallel_jobs = 3\n\n# Panel settings.\n[panels]\nshow_hidden = true\n", string(data))
}
