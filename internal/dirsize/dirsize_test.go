package dirsize_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/dirsize"
	"github.com/abunjevac/bfiles/internal/vfs/local"
)

func TestCalc(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "c"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "top"), make([]byte, 10), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "f"), make([]byte, 100), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "b", "g"), make([]byte, 1000), 0o600))
	require.NoError(t, os.Symlink(dir, filepath.Join(dir, "c", "loop")))

	r, err := dirsize.Calc(context.Background(), local.New(), dir, 4, false)
	require.NoError(t, err)

	require.Equal(t, int64(1110+len(dir)), r.Bytes)
	require.Equal(t, 4, r.Files)
	require.Equal(t, 4, r.Dirs)
}

func TestCalcCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := dirsize.Calc(ctx, local.New(), t.TempDir(), 2, false)
	require.ErrorIs(t, err, context.Canceled)
}
