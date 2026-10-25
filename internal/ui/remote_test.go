package ui

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/mholt/archives"
	"github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/abunjevac/bfiles/internal/location"
	"github.com/abunjevac/bfiles/internal/sshcfg"
	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/sftpfs"
)

// memberName is the file inside the test archives.
const memberName = "x.txt"

func makeZip(t *testing.T, dst string, files map[string]string) {
	t.Helper()

	src := t.TempDir()
	writeFiles(t, src, files)

	ctx := context.Background()

	list, err := archives.FilesFromDisk(ctx, nil, map[string]string{src + "/": ""})
	require.NoError(t, err)

	out, err := os.Create(dst)
	require.NoError(t, err)

	require.NoError(t, archives.Zip{}.Archive(ctx, out, list))
	require.NoError(t, out.Close())
}

func TestArchiveBrowseAndCopy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dst := t.TempDir()

	makeZip(t, filepath.Join(dir, "bundle.zip"), map[string]string{"inner/note.txt": "inside", "top.txt": "top"})

	h := newHarness(t, dir, dst)

	h.m.panel().selectName("bundle.zip")
	h.keys("enter")
	h.waitFor(func() bool { return h.m.panel().fs.Kind() == vfs.KindArchive }, "archive opened")

	require.Contains(t, viewNames(h.m.panel()), "inner")
	require.Contains(t, h.screen(), "bundle.zip ▸ /")

	// copy a member out to the other panel
	h.m.panel().selectName("top.txt")
	h.keys("f5", "enter")
	h.waitFor(func() bool { return exists(filepath.Join(dst, "top.txt")) }, "member copied out")

	// copy a local file into the archive
	writeFiles(t, dst, map[string]string{"added.txt": "new"})
	h.keys("tab")
	h.waitFor(func() bool { return slicesContain(viewNames(h.m.panel()), "added.txt") }, "dst lists added file")
	h.m.panel().selectName("added.txt")
	h.keys("f5", "enter")
	h.waitFor(func() bool { return slicesContain(viewNames(h.m.panels[0]), "added.txt") }, "archive panel shows added file")
	h.waitFor(func() bool { return h.m.queue.Pending() == 0 }, "queue idle")

	// leaving the archive returns to its directory with the cursor on it
	h.keys("tab", "h")
	h.waitFor(func() bool { return h.m.panel().fs.Kind() == vfs.KindLocal }, "left archive")
	require.Equal(t, "bundle.zip", h.cursorName())
	require.Empty(t, h.m.refs, "archive filesystem must be released after leaving")
}

func slicesContain(list []string, s string) bool {
	return slices.Contains(list, s)
}

func topIs[T overlay](h *harness) bool {
	_, ok := h.m.top().(T)

	return ok
}

// startSFTP runs an in-process SSH server with an SFTP subsystem for user "test",
// password "secret".
func startSFTP(t *testing.T) int {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "test" && string(pass) == "secret" {
				return &ssh.Permissions{}, nil
			}

			return nil, errors.New("denied")
		},
	}
	cfg.AddHostKey(signer)

	var lc net.ListenConfig

	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go serveSFTP(conn, cfg)
		}
	}()

	addr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok)

	return addr.Port
}

func serveSFTP(conn net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}

	go ssh.DiscardRequests(reqs)

	for nc := range chans {
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}

		go func() {
			for req := range chReqs {
				ok := req.Type == "subsystem" && string(req.Payload[4:]) == "sftp"
				_ = req.Reply(ok, nil)

				if ok {
					if srv, err := sftp.NewServer(ch); err == nil {
						_ = srv.Serve()
					}

					_ = ch.Close()
				}
			}
		}()
	}
}

func TestSSHPanelWithPrompts(t *testing.T) {
	t.Parallel()

	port := startSFTP(t)
	local := t.TempDir()
	remote := t.TempDir()

	writeFiles(t, remote, map[string]string{"remote.txt": "from afar"})

	h := newHarness(t, local, local)

	// never touch the user's ssh files from tests
	h.m.sshc = &sshcfg.Config{}
	h.m.pool = sftpfs.NewPool(sftpfs.Options{
		KnownHosts:   filepath.Join(t.TempDir(), "known_hosts"),
		DefaultKeys:  []string{},
		DisableAgent: true,
	})

	spec := location.Spec{Kind: location.SSH, User: "test", Host: "127.0.0.1", Port: port, Path: remote}
	h.run(h.m.connectCmd(0, spec))

	h.waitFor(func() bool { return topIs[*confirmDialog](h) }, "host key prompt")
	require.Contains(t, h.screen(), "Unknown host")

	// a key arriving right after the prompt popped up must not answer it
	h.keys("y")
	require.True(t, topIs[*confirmDialog](h), "early key ignored")

	time.Sleep(armDelay)
	h.keys("y")

	h.waitFor(func() bool {
		d, ok := h.m.top().(*inputDialog)

		return ok && d.input.mask
	}, "password prompt")
	time.Sleep(armDelay)
	h.typeText("secret")
	h.keys("enter")

	h.waitFor(func() bool { return h.m.panels[0].fs.Kind() == vfs.KindSSH }, "ssh panel")
	require.Contains(t, viewNames(h.m.panels[0]), "remote.txt")
	require.Contains(t, h.m.locationText(h.m.panels[0]), "ssh://test@127.0.0.1:"+strconv.Itoa(port))
	require.NotContains(t, h.screen(), "secret", "password must never be shown")

	// download to the local panel
	h.m.panels[0].selectName("remote.txt")
	h.keys("f5", "enter")
	h.waitFor(func() bool { return exists(filepath.Join(local, "remote.txt")) }, "downloaded")

	// ctrl+w returns to the local directory shown before connecting
	h.m.active = 0
	h.keys("ctrl+w")
	h.waitFor(func() bool { return h.m.panels[0].fs.Kind() == vfs.KindLocal }, "disconnected")
	require.Equal(t, local, h.m.panels[0].dir)
	h.waitFor(func() bool { return h.m.queue.Pending() == 0 && len(h.m.refs) == 0 }, "connection released")

	hist, err := os.ReadFile(filepath.Join(h.m.bundle.Dir, "history.toml"))
	require.NoError(t, err)
	require.Contains(t, string(hist), "test@127.0.0.1:"+strconv.Itoa(port))
}

func TestCloseLocationLeavesArchive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	makeZip(t, filepath.Join(dir, "b.zip"), map[string]string{memberName: "x"})

	h := newHarness(t, dir, dir)

	h.m.panel().selectName("b.zip")
	h.keys("enter")
	h.waitFor(func() bool { return h.m.panel().fs.Kind() == vfs.KindArchive }, "archive opened")
	require.Contains(t, h.screen(), "⏏ C-w")

	h.keys("ctrl+w")
	h.waitFor(func() bool { return h.m.panel().fs.Kind() == vfs.KindLocal }, "archive closed")
	require.Equal(t, "b.zip", h.cursorName())
}

func TestInfoClosedEarlyReleasesArchive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	makeZip(t, filepath.Join(dir, "b.zip"), map[string]string{memberName: "x"})

	h := newHarness(t, dir, dir)

	h.m.panel().selectName("b.zip")
	h.keys("enter")
	h.waitFor(func() bool { return h.m.panel().fs.Kind() == vfs.KindArchive }, "archive opened")

	h.m.panel().selectName(memberName)
	h.update(keyMsg("i"))
	h.update(keyMsg("esc")) // close before the details arrive
	h.update(keyMsg("f3"))
	h.update(keyMsg("q"))
	h.settle()

	h.keys("ctrl+w")
	h.waitFor(func() bool { return h.m.panel().fs.Kind() == vfs.KindLocal }, "archive closed")
	require.Empty(t, h.m.refs, "no reference may outlive the closed dialogs")
}

func TestHistoryForgetsClosedArchive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	makeZip(t, filepath.Join(dir, "b.zip"), map[string]string{memberName: "x"})

	h := newHarness(t, dir, dir)

	h.m.panel().selectName("b.zip")
	h.keys("enter")
	h.waitFor(func() bool { return h.m.panel().fs.Kind() == vfs.KindArchive }, "archive opened")

	h.keys("ctrl+w")
	h.waitFor(func() bool { return h.m.panel().fs.Kind() == vfs.KindLocal }, "archive closed")

	for _, pl := range h.m.panel().back {
		require.NotEqual(t, vfs.KindArchive, pl.fs.Kind(), "closed archive must leave history")
	}

	h.keys("H")
	require.Equal(t, vfs.KindLocal, h.m.panel().fs.Kind())
}

func TestStaleArchiveOpenIsDiscarded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	makeZip(t, filepath.Join(dir, "b.zip"), map[string]string{memberName: "x"})
	writeFiles(t, dir, map[string]string{"sub/f": "f"})

	h := newHarness(t, dir, dir)

	open := h.m.openArchiveCmd(0, h.m.local, filepath.Join(dir, "b.zip"), "")

	// the user goes elsewhere before the archive finished opening
	h.run(h.m.navigate(0, h.m.local, filepath.Join(dir, "sub"), "", true))
	h.settle()
	h.run(open)
	h.settle()

	require.Equal(t, vfs.KindLocal, h.m.panels[0].fs.Kind())
	require.Equal(t, filepath.Join(dir, "sub"), h.m.panels[0].dir)
	require.Empty(t, h.m.refs)
}
