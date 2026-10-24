package sftpfs_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/abunjevac/bfiles/internal/sshcfg"
	"github.com/abunjevac/bfiles/internal/vfs/sftpfs"
)

const testPassword = "secret"

// startServer runs an in-process SSH server with an SFTP subsystem that accepts
// user "test" with password "secret", serving the real filesystem.
func startServer(t *testing.T) int {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "test" && string(pass) == testPassword {
				return &ssh.Permissions{}, nil
			}

			return nil, errors.New("denied")
		},
	}
	cfg.AddHostKey(signer)

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go serveConn(conn, cfg)
		}
	}()

	addr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok)

	return addr.Port
}

func serveConn(conn net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}

	go ssh.DiscardRequests(reqs)

	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "unsupported")

			continue
		}

		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}

		go func() {
			for req := range chReqs {
				ok := req.Type == "subsystem" && string(req.Payload[4:]) == "sftp"
				_ = req.Reply(ok, nil)

				if ok {
					srv, err := sftp.NewServer(ch)
					if err == nil {
						_ = srv.Serve()
					}

					_ = ch.Close()
				}
			}
		}()
	}
}

type prompter struct {
	password string
	trust    bool
	asked    atomic.Int32
}

func (p *prompter) Secret(context.Context, string) (string, error) { return p.password, nil }

func (p *prompter) Confirm(context.Context, string) (bool, error) {
	p.asked.Add(1)

	return p.trust, nil
}

func target(port int) sshcfg.Target {
	return sshcfg.Target{Alias: "localhost", User: "test", Host: "127.0.0.1", Port: port}
}

func options(t *testing.T) sftpfs.Options {
	t.Helper()

	return sftpfs.Options{
		KnownHosts:   filepath.Join(t.TempDir(), "known_hosts"),
		DefaultKeys:  []string{},
		DisableAgent: true,
	}
}

func TestDialAndOperate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	port := startServer(t)
	opts := options(t)
	p := &prompter{password: testPassword, trust: true}

	f, err := sftpfs.Dial(ctx, target(port), p, opts)
	require.NoError(t, err)

	defer f.Close()

	require.Equal(t, int32(1), p.asked.Load(), "unknown host must be confirmed")
	require.Equal(t, "ssh:test@127.0.0.1:"+strconv.Itoa(port), f.ID())

	dir := t.TempDir()

	w, err := f.Create(ctx, filepath.Join(dir, "a.txt"), 0o640)
	require.NoError(t, err)

	_, err = io.WriteString(w, "remote")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	require.NoError(t, f.MkdirAll(ctx, filepath.Join(dir, "x", "y"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("old"), 0o600))
	require.NoError(t, f.Rename(ctx, filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")))

	entries, err := f.ReadDir(ctx, dir)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	r, err := f.Open(ctx, filepath.Join(dir, "b.txt"))
	require.NoError(t, err)

	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, "remote", string(data))

	// second connection: host is now known, no question asked
	f2, err := sftpfs.Dial(ctx, target(port), p, opts)
	require.NoError(t, err)
	require.NoError(t, f2.Close())
	require.Equal(t, int32(1), p.asked.Load())
}

func TestRejectedHostKey(t *testing.T) {
	t.Parallel()

	port := startServer(t)

	_, err := sftpfs.Dial(context.Background(), target(port), &prompter{password: testPassword}, options(t))
	require.ErrorContains(t, err, "host key rejected")
}

func TestChangedHostKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	port := startServer(t)
	opts := options(t)

	// remember a different key for this address
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	other, err := ssh.NewSignerFromKey(otherPriv)
	require.NoError(t, err)

	line := "[127.0.0.1]:" + strconv.Itoa(port) + " " + string(ssh.MarshalAuthorizedKey(other.PublicKey()))

	require.NoError(t, os.WriteFile(opts.KnownHosts, []byte(line), 0o600))

	_, err = sftpfs.Dial(ctx, target(port), &prompter{password: testPassword, trust: true}, opts)
	require.ErrorIs(t, err, sftpfs.ErrHostKeyChanged)
}

func TestWrongPassword(t *testing.T) {
	t.Parallel()

	port := startServer(t)

	_, err := sftpfs.Dial(context.Background(), target(port), &prompter{password: "nope", trust: true}, options(t))
	require.Error(t, err)
}

func TestPoolSharesConnections(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	port := startServer(t)
	pool := sftpfs.NewPool(options(t))
	p := &prompter{password: testPassword, trust: true}

	a, err := pool.Get(ctx, target(port), p)
	require.NoError(t, err)

	b, err := pool.Get(ctx, target(port), p)
	require.NoError(t, err)

	require.Same(t, a, b)

	pool.Release(a)

	_, err = b.Stat(ctx, "/")
	require.NoError(t, err, "connection must stay open while referenced")

	pool.Release(b)

	_, err = b.Stat(ctx, "/")
	require.Error(t, err, "connection must close after last release")
}
