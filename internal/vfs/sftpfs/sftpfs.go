package sftpfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
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

	closeOnce sync.Once
	closeErr  error
}

// maxLinkDepth bounds symlink resolution so a link loop cannot recurse forever.
const maxLinkDepth = 40

var (
	_ vfs.FS         = (*FS)(nil)
	_ vfs.Identifier = (*FS)(nil)
)

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
	f.closeOnce.Do(func() {
		close(f.done)

		_ = f.client.Close()

		f.closeErr = f.ssh.Close()
	})

	return f.closeErr
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

	return f.renameReplace(from, to)
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

// RealPath implements vfs.Identifier. Links are resolved on the client, because
// not every SFTP server resolves them in its realpath reply.
func (f *FS) RealPath(_ context.Context, p string) (string, error) {
	return f.resolve(p, 0)
}

// SameFile implements vfs.Identifier by comparing resolved paths; SFTP exposes no
// inode numbers, so bind mounts and hard links are not detected.
func (f *FS) SameFile(ctx context.Context, a, b string) bool {
	if _, err := f.client.Stat(a); err != nil {
		return false
	}

	if _, err := f.client.Stat(b); err != nil {
		return false
	}

	ra, err := f.RealPath(ctx, a)
	if err != nil {
		return false
	}

	rb, err := f.RealPath(ctx, b)

	return err == nil && ra == rb
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

// renameReplace emulates a replacing rename with plain SFTP renames, which refuse
// to overwrite. An existing file target is moved aside first and restored if the
// rename still fails, so the target is never lost.
func (f *FS) renameReplace(from, to string) error {
	if _, err := f.client.Lstat(from); err != nil {
		return err
	}

	err := f.client.Rename(from, to)
	if err == nil {
		return nil
	}

	fi, statErr := f.client.Lstat(to)
	if statErr != nil || fi.IsDir() {
		return err
	}

	aside := path.Join(path.Dir(to), "."+path.Base(to)+".bfiles-old-"+strconv.FormatInt(time.Now().UnixNano(), 36))

	if err := f.client.Rename(to, aside); err != nil {
		return err
	}

	if err := f.client.Rename(from, to); err != nil {
		if rerr := f.client.Rename(aside, to); rerr != nil {
			return fmt.Errorf("rename %s: %w (previous target kept as %s)", to, err, aside)
		}

		return err
	}

	return f.client.Remove(aside)
}

// resolve follows symlinks component by component; the part below the first
// missing component is appended unchanged.
func (f *FS) resolve(p string, depth int) (string, error) {
	if depth > maxLinkDepth {
		return "", fmt.Errorf("%s: too many levels of symbolic links", p)
	}

	parts := strings.Split(strings.Trim(path.Clean("/"+p), "/"), "/")
	cur := "/"

	for i, part := range parts {
		if part == "" {
			continue
		}

		next := path.Join(cur, part)

		fi, err := f.client.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			return path.Join(append([]string{next}, parts[i+1:]...)...), nil
		}

		if err != nil {
			return "", err
		}

		if fi.Mode()&fs.ModeSymlink == 0 {
			cur = next

			continue
		}

		target, err := f.client.ReadLink(next)
		if err != nil {
			return "", err
		}

		if !path.IsAbs(target) {
			target = path.Join(cur, target)
		}

		cur, err = f.resolve(target, depth+1)
		if err != nil {
			return "", err
		}
	}

	return cur, nil
}
