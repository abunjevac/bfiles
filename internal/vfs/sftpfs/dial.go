package sftpfs

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/abunjevac/bfiles/internal/sshcfg"
)

// Prompter asks the user for secrets and confirmations during a connection. It is
// called from the dialing goroutine and may block until the user answers.
type Prompter interface {
	// Secret asks for a password or passphrase; echo is false for secrets.
	Secret(ctx context.Context, prompt string) (string, error)
	// Confirm asks a yes/no question.
	Confirm(ctx context.Context, question string) (bool, error)
}

// Options controls how connections are made. Zero values use the user's defaults.
type Options struct {
	KnownHosts    string // default ~/.ssh/known_hosts
	DefaultKeys   []string
	DisableAgent  bool
	Keepalive     time.Duration
	Timeout       time.Duration
	ConfigForJump *sshcfg.Config // resolves ProxyJump aliases
}

func (o Options) withDefaults() Options {
	home, _ := os.UserHomeDir()

	if o.KnownHosts == "" {
		o.KnownHosts = filepath.Join(home, ".ssh", "known_hosts")
	}

	if o.DefaultKeys == nil {
		for _, k := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			o.DefaultKeys = append(o.DefaultKeys, filepath.Join(home, ".ssh", k))
		}
	}

	if o.Timeout == 0 {
		o.Timeout = 15 * time.Second
	}

	return o
}

// ErrHostKeyChanged means the server presented a key different from known_hosts.
var ErrHostKeyChanged = errors.New("REMOTE HOST KEY CHANGED — possible man-in-the-middle attack; fix ~/.ssh/known_hosts to continue")

// dialSSH connects and authenticates, through a single ProxyJump hop if configured.
func dialSSH(ctx context.Context, t sshcfg.Target, p Prompter, o Options) (*ssh.Client, error) {
	cfg, closeAgent, err := clientConfig(ctx, t, p, o)
	if err != nil {
		return nil, err
	}

	defer closeAgent()

	if t.ProxyJump != "" {
		return dialViaJump(ctx, t, cfg, p, o)
	}

	d := net.Dialer{Timeout: o.Timeout}

	conn, err := d.DialContext(ctx, "tcp", t.Addr())
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", t.Addr(), err)
	}

	return handshake(ctx, conn, t.Addr(), cfg)
}

func dialViaJump(ctx context.Context, t sshcfg.Target, cfg *ssh.ClientConfig, p Prompter, o Options) (*ssh.Client, error) {
	if strings.Contains(t.ProxyJump, ",") {
		return nil, fmt.Errorf("ProxyJump with several hops is not supported (%s)", t.ProxyJump)
	}

	jumpCfg := o.ConfigForJump
	if jumpCfg == nil {
		jumpCfg = &sshcfg.Config{}
	}

	user, host, port := splitJump(t.ProxyJump)
	jt := jumpCfg.Resolve(user, host, port)
	jt.ProxyJump = ""

	jump, err := dialSSH(ctx, jt, p, o)
	if err != nil {
		return nil, fmt.Errorf("jump host %s: %w", t.ProxyJump, err)
	}

	conn, err := jump.DialContext(ctx, "tcp", t.Addr())
	if err != nil {
		_ = jump.Close()

		return nil, fmt.Errorf("connect %s via %s: %w", t.Addr(), t.ProxyJump, err)
	}

	client, err := handshake(ctx, conn, t.Addr(), cfg)
	if err != nil {
		_ = jump.Close()

		return nil, err
	}

	// close the jump connection together with the target connection
	go func() {
		_ = client.Wait()
		_ = jump.Close()
	}()

	return client, nil
}

func splitJump(s string) (string, string, int) {
	var (
		user string
		port int
	)

	host := s

	if u, h, ok := strings.Cut(host, "@"); ok {
		user, host = u, h
	}

	if h, p, err := net.SplitHostPort(host); err == nil {
		host = h
		_, _ = fmt.Sscanf(p, "%d", &port)
	}

	return user, host, port
}

func handshake(ctx context.Context, conn net.Conn, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	// the handshake has no context support; closing the conn aborts it
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return nil, fmt.Errorf("ssh %s: %w", addr, err)
	}

	return ssh.NewClient(c, chans, reqs), nil
}

func clientConfig(ctx context.Context, t sshcfg.Target, p Prompter, o Options) (*ssh.ClientConfig, func(), error) {
	hostKey, check, err := hostKeyCallback(ctx, o.KnownHosts, p)
	if err != nil {
		return nil, nil, err
	}

	var (
		methods    []ssh.AuthMethod
		closeAgent = func() {}
	)

	if !o.DisableAgent {
		if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
			var d net.Dialer

			if conn, err := d.DialContext(ctx, "unix", sock); err == nil {
				methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
				closeAgent = func() { _ = conn.Close() }
			}
		}
	}

	keys := t.IdentityFiles
	if len(keys) == 0 {
		keys = o.DefaultKeys
	}

	methods = append(methods, ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		return loadSigners(ctx, keys, p), nil
	}))

	methods = append(methods, promptMethods(ctx, t, p)...)

	cfg := &ssh.ClientConfig{
		User:              t.User,
		Auth:              methods,
		HostKeyCallback:   hostKey,
		HostKeyAlgorithms: knownAlgorithms(check, t.Addr()),
		Timeout:           o.Timeout,
	}

	return cfg, closeAgent, nil
}

// promptMethods authenticates with answers from the user.
func promptMethods(ctx context.Context, t sshcfg.Target, p Prompter) []ssh.AuthMethod {
	return []ssh.AuthMethod{
		ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, len(questions))

			for i, q := range questions {
				prompt := strings.TrimSpace(instruction + " " + q)

				a, err := p.Secret(ctx, fmt.Sprintf("%s@%s %s", t.User, t.Host, prompt))
				if err != nil {
					return nil, err
				}

				answers[i] = a
			}

			return answers, nil
		}),
		ssh.PasswordCallback(func() (string, error) {
			return p.Secret(ctx, fmt.Sprintf("Password for %s@%s", t.User, t.Host))
		}),
	}
}

// loadSigners reads private keys, asking for passphrases of encrypted ones.
func loadSigners(ctx context.Context, files []string, p Prompter) []ssh.Signer {
	var signers []ssh.Signer

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}

		s, err := ssh.ParsePrivateKey(data)

		if _, ok := errors.AsType[*ssh.PassphraseMissingError](err); ok {
			pass, perr := p.Secret(ctx, "Passphrase for "+f)
			if perr != nil {
				continue
			}

			s, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(pass))
		}

		if err == nil {
			signers = append(signers, s)
		}
	}

	return signers
}

// knownAlgorithms returns the host key algorithms for the key types known_hosts has
// for addr, so the server is asked for a key that can be verified rather than one of
// another type, which would look like a changed key. It returns nil (the defaults)
// for an unknown host.
func knownAlgorithms(check ssh.HostKeyCallback, addr string) []string {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil
	}

	probe, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		return nil
	}

	// the probe key is fresh, so the check fails and lists the known keys
	keyErr, ok := errors.AsType[*knownhosts.KeyError](check(addr, &net.TCPAddr{IP: net.IPv4zero}, probe))
	if !ok {
		return nil
	}

	var algos []string

	for _, k := range keyErr.Want {
		for _, a := range algorithmsFor(k.Key.Type()) {
			if !slices.Contains(algos, a) {
				algos = append(algos, a)
			}
		}
	}

	return algos
}

// algorithmsFor maps a key type to the signature algorithms that verify it.
func algorithmsFor(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}

	return []string{keyType}
}

// hostKeyCallback verifies against known_hosts, asking the user about unknown hosts
// and remembering accepted keys. A changed key is always refused. The raw
// known_hosts check is returned too, for choosing host key algorithms.
func hostKeyCallback(ctx context.Context, path string, p Prompter) (ssh.HostKeyCallback, ssh.HostKeyCallback, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("known-hosts: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("known-hosts: %w", err)
	}

	_ = f.Close()

	check, err := knownhosts.New(path)
	if err != nil {
		return nil, nil, fmt.Errorf("known-hosts: %w", err)
	}

	return verifyHostKey(ctx, path, p, check), check, nil
}

// verifyHostKey wraps the known_hosts check with the user's decisions.
func verifyHostKey(ctx context.Context, path string, p Prompter, check ssh.HostKeyCallback) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := check(hostname, remote, key)
		if err == nil {
			return nil
		}

		var keyErr *knownhosts.KeyError

		if !errors.As(err, &keyErr) {
			return err
		}

		if len(keyErr.Want) > 0 {
			return knownKeyMismatch(hostname, key, keyErr.Want)
		}

		question := fmt.Sprintf("Unknown host %s\n%s key fingerprint %s\nTrust and remember it?",
			hostname, key.Type(), ssh.FingerprintSHA256(key))

		ok, err := p.Confirm(ctx, question)
		if err != nil {
			return err
		}

		if !ok {
			return errors.New("host key rejected")
		}

		return appendKnownHost(path, hostname, remote, key)
	}
}

// knownKeyMismatch explains why a key of a known host was refused: a different key
// of a known type has changed, while a key of another type cannot be verified.
func knownKeyMismatch(hostname string, key ssh.PublicKey, want []knownhosts.KnownKey) error {
	types := make([]string, 0, len(want))

	for _, k := range want {
		if k.Key.Type() == key.Type() {
			return ErrHostKeyChanged
		}

		types = append(types, k.Key.Type())
	}

	return fmt.Errorf("host %s offered a %s key, but known_hosts only has %s keys for it",
		hostname, key.Type(), strings.Join(types, ", "))
}

func appendKnownHost(path, hostname string, remote net.Addr, key ssh.PublicKey) error {
	addrs := []string{knownhosts.Normalize(hostname)}

	if remote != nil {
		if r := knownhosts.Normalize(remote.String()); r != addrs[0] {
			addrs = append(addrs, r)
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("known-hosts: %w", err)
	}

	defer f.Close()

	if _, err := fmt.Fprintln(f, knownhosts.Line(addrs, key)); err != nil {
		return fmt.Errorf("known-hosts: %w", err)
	}

	return nil
}
