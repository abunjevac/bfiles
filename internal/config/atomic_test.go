package config_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/config"
)

const nordTheme = "nord"

func TestSaveKeepsSymlinkAndMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "config.toml")
	link := filepath.Join(dir, "config.toml")

	require.NoError(t, os.Mkdir(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte(config.DefaultTOML), 0o600))
	require.NoError(t, os.Symlink(target, link))

	require.NoError(t, config.UpdateFile(link, []config.Change{{Key: "theme", Value: nordTheme}}))

	cfg, err := config.Parse("")
	require.NoError(t, err)

	require.NoError(t, config.Save(link, cfg))

	fi, err := os.Lstat(link)
	require.NoError(t, err)

	require.Equal(t, os.ModeSymlink, fi.Mode()&os.ModeSymlink, "the symlink survives")

	fi, err = os.Stat(target)
	require.NoError(t, err)

	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	entries, err := os.ReadDir(filepath.Dir(target))
	require.NoError(t, err)

	require.Len(t, entries, 1, "no temp files are left behind")
}

func TestSaveHistoryPrivateAndConcurrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	var wg sync.WaitGroup

	errs := make(chan error, 8)

	for i := range 8 {
		wg.Go(func() {
			h := &config.History{}

			h.Touch("ssh://nas/~", time.Unix(int64(i), 0), 10)

			errs <- config.SaveHistory(dir, h)
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	fi, err := os.Stat(config.HistoryPath(dir))
	require.NoError(t, err)

	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	h, err := config.LoadHistory(dir)
	require.NoError(t, err)

	require.Len(t, h.Entries, 1)
}
