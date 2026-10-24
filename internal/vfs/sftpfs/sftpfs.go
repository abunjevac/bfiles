package sftpfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/abunjevac/bfiles/internal/sshcfg"
	"github.com/abunjevac/bfiles/internal/vfs"
)

// FS is a remote filesystem over one SSH connection.
type FS struct {
	target sshcfg.Target
	ssh    *ssh.Client
	client *sftp.Client
	home   string
	done   chan struct{}
}

var _ vfs.FS = (*FS)(nil)

// Dial connects to t and starts an SFTP session.
func Dial(ctx context.Context, t sshcfg.Target, p Prompter, o Options) (*FS, error) {
	o = o.withDefaults()

	sc, err := dialSSH(ctx, t, p, o)
	if err != nil {
		return nil, err
	}

	client, err := sftp.NewClient(sc, sftp.UseConcurrentReads(true), sftp.UseConcurrentWrites(true))
	if err != nil {
		_ = sc.Close()

		return nil, fmt.Errorf("sftp %s: %w", t.Addr(), err)
	}

	home, err := client.Getwd()
	if err != nil || home == "" {
		home = "/"
	}

	f := &FS{target: t, ssh: sc, client: client, home: home, done: make(chan struct{})}

	if o.Keepalive > 0 {
		go f.keepalive(o.Keepalive)
	}

	return f, nil
}

// Target returns the resolved destination.
func (f *FS) Target() sshcfg.Target { return f.target }

// Home returns the remote working directory at login.
func (f *FS) Home() string { return f.home }

// Wait blocks until the connection drops and returns why.
func (f *FS) Wait() error { return f.ssh.Wait() }

// ID implements vfs.FS.
func (f *FS) ID() string { return "ssh:" + f.target.Key() }

// Kind implements vfs.FS.
func (*FS) Kind() vfs.Kind { return vfs.KindSSH }

// Label implements vfs.FS.
func (f *FS) Label() string {
	s := "ssh://" + f.target.User + "@" + f.target.Alias

	if f.target.Port != 22 {
		s += ":" + strconv.Itoa(f.target.Port)
	}

	return s
}

// Caps implements vfs.FS.
func (*FS) Caps() vfs.Caps { return vfs.Caps{Write: true, Rename: true, Symlink: true} }

// Close implements vfs.FS.
func (f *FS) Close() error {
	select {
	case <-f.done:
		return nil
	default:
		close(f.done)
	}

	_ = f.client.Close()

	return f.ssh.Close()
}

// ReadDir implements vfs.FS. Symlinks are stat'ed individually to resolve IsDir.
func (f *FS) ReadDir(ctx context.Context, dir string) ([]vfs.Entry, error) {
	infos, err := f.client.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	out := make([]vfs.Entry, 0, len(infos))

	for _, fi := range infos {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		out = append(out, f.entry(path.Join(dir, fi.Name()), fi))
	}

	return out, nil
}

// Stat implements vfs.FS.
func (f *FS) Stat(_ context.Context, p string) (vfs.Entry, error) {
	fi, err := f.client.Lstat(p)
	if err != nil {
		return vfs.Entry{}, err
	}

	e := f.entry(p, fi)

	if p == "/" {
		e.Name = "/"
	}

	return e, nil
}

// Open implements vfs.FS.
func (f *FS) Open(_ context.Context, p string) (io.ReadCloser, error) {
	return f.client.Open(p)
}

// Create implements vfs.FS.
func (f *FS) Create(_ context.Context, p string, perm fs.FileMode) (io.WriteCloser, error) {
	file, err := f.client.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return nil, err
	}

	_ = file.Chmod(perm.Perm())

	return file, nil
}

// MkdirAll implements vfs.FS.
func (f *FS) MkdirAll(_ context.Context, p string, _ fs.FileMode) error {
	return f.client.MkdirAll(p)
}

// Remove implements vfs.FS.
func (f *FS) Remove(_ context.Context, p string) error {
	return f.client.Remove(p)
}

// Rename implements vfs.FS, replacing an existing target like a local rename does.
func (f *FS) Rename(_ context.Context, from, to string) error {
	if _, ok := f.client.HasExtension("posix-rename@openssh.com"); ok {
		return f.client.PosixRename(from, to)
	}

	if err := f.client.Rename(from, to); err == nil {
		return nil
	}

	// plain SFTP rename refuses to overwrite; emulate replace for files
	if fi, err := f.client.Lstat(to); err == nil && !fi.IsDir() {
		if err := f.client.Remove(to); err != nil {
			return err
		}
	}

	return f.client.Rename(from, to)
}

// Chtimes implements vfs.FS.
func (f *FS) Chtimes(_ context.Context, p string, mtime time.Time) error {
	return f.client.Chtimes(p, mtime, mtime)
}

// Chmod implements vfs.FS.
func (f *FS) Chmod(_ context.Context, p string, mode fs.FileMode) error {
	return f.client.Chmod(p, mode)
}

// Symlink implements vfs.FS.
func (f *FS) Symlink(_ context.Context, target, p string) error {
	return f.client.Symlink(target, p)
}

// Readlink returns the target of the symlink at p.
func (f *FS) Readlink(_ context.Context, p string) (string, error) {
	return f.client.ReadLink(p)
}

// Run executes a command on the remote host and returns its combined output.
func (f *FS) Run(ctx context.Context, cmd string) ([]byte, error) {
	s, err := f.ssh.NewSession()
	if err != nil {
		return nil, err
	}

	defer s.Close()

	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()

	return s.CombinedOutput(cmd)
}

func (f *FS) keepalive(every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()

	for {
		select {
		case <-f.done:
			return
		case <-tick.C:
			if _, _, err := f.ssh.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				return
			}
		}
	}
}

func (f *FS) entry(p string, fi fs.FileInfo) vfs.Entry {
	e := vfs.Entry{
		Name:    fi.Name(),
		Path:    p,
		Size:    fi.Size(),
		Disk:    fi.Size(),
		Mode:    fi.Mode(),
		ModTime: fi.ModTime(),
		IsDir:   fi.IsDir(),
	}

	if fi.Mode()&fs.ModeSymlink != 0 {
		e.IsLink = true
		e.Link, _ = f.client.ReadLink(p)

		if ti, err := f.client.Stat(p); err == nil {
			e.IsDir = ti.IsDir()
			e.Size = ti.Size()
			e.Mode = ti.Mode()
		}
	}

	return e
}
