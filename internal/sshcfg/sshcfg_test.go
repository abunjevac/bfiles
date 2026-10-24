package sshcfg_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/sshcfg"
)

const (
	nasAlias  = "nas"
	piAtNas   = "pi@nas"
	bobTmp    = "bob@x.org:/tmp"
	bobOther  = "bob@x.org:/other"
	webAlias  = "web"
	web2Alias = "web2"
)

func load(t *testing.T) *sshcfg.Config {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config")

	require.NoError(t, os.WriteFile(path, []byte(sshConfig), 0o600))

	cfg, err := sshcfg.Load(path)
	require.NoError(t, err)

	return cfg
}

const sshConfig = "Host nas\n  HostName 10.0.0.5\n  User pi\n  Port 2222\n  IdentityFile ~/.ssh/nas\n\n" +
	"Host web web2\n  HostName example.org\n\nHost *.internal\n  User ops\n\nHost *\n  ServerAliveInterval 30\n"

func TestAliases(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{nasAlias, webAlias, web2Alias}, load(t).Aliases())
}

func TestResolve(t *testing.T) {
	t.Parallel()

	cfg := load(t)

	nas := cfg.Resolve("", nasAlias, 0)
	require.Equal(t, "pi", nas.User)
	require.Equal(t, "10.0.0.5", nas.Host)
	require.Equal(t, 2222, nas.Port)
	require.Len(t, nas.IdentityFiles, 1)

	explicit := cfg.Resolve("root", nasAlias, 22)
	require.Equal(t, "root", explicit.User)
	require.Equal(t, 22, explicit.Port)

	plain := cfg.Resolve("me", "other.host", 0)
	require.Equal(t, "other.host", plain.Host)
	require.Equal(t, 22, plain.Port)
	require.Equal(t, "me@other.host:22", plain.Key())
}

func TestMissingFile(t *testing.T) {
	t.Parallel()

	cfg, err := sshcfg.Load(filepath.Join(t.TempDir(), "none"))
	require.NoError(t, err)

	require.Empty(t, cfg.Aliases())
	require.Equal(t, 22, cfg.Resolve("u", "h", 0).Port)
}

func TestMerge(t *testing.T) {
	t.Parallel()

	now := time.Now()
	keys := map[string]string{
		piAtNas:           "pi@10.0.0.5:2222",
		nasAlias:          "pi@10.0.0.5:2222",
		webAlias:          "me@example.org:22",
		web2Alias:         "me@example.org:22",
		bobTmp:            "bob@x.org:22/tmp",
		bobOther:          "bob@x.org:22/other",
		"bob@x.org:/tmp/": "bob@x.org:22/tmp",
	}

	got := sshcfg.Merge(
		[]string{nasAlias, webAlias, web2Alias},
		[]sshcfg.HistoryItem{
			{Target: bobTmp, Last: now},
			{Target: piAtNas, Last: now.Add(-time.Hour)},
			{Target: bobOther, Last: now.Add(-2 * time.Hour)},
			{Target: "bob@x.org:/tmp/", Last: now.Add(-3 * time.Hour)},
		},
		func(text string) string { return keys[text] },
	)

	texts := make([]string, 0, len(got))

	for _, c := range got {
		texts = append(texts, c.Text)
	}

	require.Equal(t, []string{bobTmp, piAtNas, bobOther, webAlias}, texts)
	require.True(t, got[1].Alias)
}
