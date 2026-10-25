package search_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/search"
	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/local"
)

const (
	appLog   = "app.log"
	otherLog = "sub/other.LOG"
	notesTxt = "sub/deep/notes.txt"
	logGlob  = "*.log"
)

func fixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	files := map[string]string{
		appLog:                 "started\nERROR boom\n",
		otherLog:               "all fine\n",
		notesTxt:               "error in notes\n",
		".hidden/secret.log":   "x\n",
		"node_modules/dep.log": "y\n",
	}

	for name, content := range files {
		p := filepath.Join(dir, name)

		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}

	return dir
}

func run(t *testing.T, dir string, o search.Options) []string {
	t.Helper()

	var got []string

	err := search.Run(context.Background(), local.New(), dir, o, func(batch []vfs.Entry) {
		for _, e := range batch {
			got = append(got, e.Name)
		}
	})
	require.NoError(t, err)

	slices.Sort(got)

	return got
}

func TestSearch(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	skip := []string{"node_modules"}

	tests := []struct {
		name string
		o    search.Options
		want []string
	}{
		{"glob case-insensitive", search.Options{Pattern: logGlob, Skip: skip}, []string{appLog, otherLog}},
		{"glob case-sensitive", search.Options{Pattern: logGlob, CaseSensitive: true, Skip: skip}, []string{appLog}},
		{"substring", search.Options{Pattern: "note", Skip: skip}, []string{notesTxt}},
		{"regex", search.Options{Pattern: `re:^(app|other)\.`, Skip: skip}, []string{appLog, otherLog}},
		{"hidden and special", search.Options{Pattern: logGlob, Hidden: true}, []string{".hidden/secret.log", appLog, "node_modules/dep.log", otherLog}},
		{"content", search.Options{Content: "error", Skip: skip}, []string{appLog, notesTxt}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, run(t, dir, tc.o))
		})
	}
}

func TestSearchBadRegex(t *testing.T) {
	t.Parallel()

	err := search.Run(context.Background(), local.New(), t.TempDir(), search.Options{Pattern: "re:("}, func([]vfs.Entry) {})
	require.ErrorContains(t, err, "regular expression")
}

const splitTxt = "split.txt"

func TestContentAfterLongLine(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	long := strings.Repeat("x", 2<<20)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "long.txt"), []byte(long+"\nNeedle here\n"), 0o600))

	// a match straddling a chunk boundary, and one starting with a split rune
	edge := strings.Repeat("y", 64<<10-3) + "NEEDLE"
	split := strings.Repeat("y", 64<<10-1) + "äNEEDLE"

	require.NoError(t, os.WriteFile(filepath.Join(dir, "edge.txt"), []byte(edge), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, splitTxt), []byte(split), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "none.txt"), []byte(long), 0o600))

	got := run(t, dir, search.Options{Content: "needle"})
	require.Equal(t, []string{"edge.txt", "long.txt", splitTxt}, got)

	got = run(t, dir, search.Options{Content: "äneedle"})
	require.Equal(t, []string{splitTxt}, got)

	got = run(t, dir, search.Options{Content: "äNEEDLE", CaseSensitive: true})
	require.Equal(t, []string{splitTxt}, got)
}
