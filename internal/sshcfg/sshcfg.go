package sshcfg

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kevinburke/ssh_config"
)

// Config is a parsed ssh_config file. The zero value behaves like an empty file.
//
// Include directives are followed (relative paths resolve against the including
// file's directory) and host matching is case-insensitive. Match blocks are honoured
// only for "Match all" and "Match host <patterns>"; blocks with any other criteria
// (exec, user, canonical, ...) cannot be evaluated here and are ignored entirely.
type Config struct {
	cfg     *ssh_config.Config
	aliases []string
}

// Load reads an ssh_config file; a missing file yields an empty Config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}

	if err != nil {
		return &Config{}, fmt.Errorf("read-ssh-config: %w", err)
	}

	var p preprocessor

	if err = p.file(path, data, 0); err != nil {
		return &Config{}, fmt.Errorf("read-ssh-config: %w", err)
	}

	cfg, err := ssh_config.DecodeBytes(p.out.Bytes())
	if err != nil {
		return &Config{}, fmt.Errorf("parse-ssh-config: %w", err)
	}

	return &Config{cfg: cfg, aliases: p.aliases}, nil
}

// DefaultPath returns ~/.ssh/config.
func DefaultPath() string {
	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".ssh", "config")
}

// Aliases returns the concrete host aliases defined in the file and the files it
// includes, lower-cased, in file order, skipping wildcard and negated patterns.
func (c *Config) Aliases() []string {
	return slices.Clone(c.aliases)
}

// Target is a fully resolved SSH destination.
type Target struct {
	Alias         string // what the user typed or picked
	User          string
	Host          string // real host name
	Port          int
	IdentityFiles []string
	ProxyJump     string
}

// Addr returns "host:port".
func (t Target) Addr() string {
	return t.Host + ":" + strconv.Itoa(t.Port)
}

// Key identifies the destination independent of how it was named.
func (t Target) Key() string {
	return t.User + "@" + t.Addr()
}

// Resolve fills in a target from the ssh_config entry of host (an alias or a real
// host name). Explicit user and port win over the config; defaults are the current
// user and port 22.
func (c *Config) Resolve(userName, host string, port int) Target {
	t := Target{Alias: host, User: userName, Host: host, Port: port}

	if hn := c.get(host, "HostName"); hn != "" {
		t.Host = strings.ReplaceAll(hn, "%h", host)
	}

	t.User = c.resolveUser(host, t.User)
	t.Port = c.resolvePort(host, t.Port)

	for _, f := range c.getAll(host, "IdentityFile") {
		t.IdentityFiles = append(t.IdentityFiles, expandTokens(expandTilde(f), t))
	}

	if pj := c.get(host, "ProxyJump"); pj != "" && !strings.EqualFold(pj, "none") {
		t.ProxyJump = pj
	}

	return t
}

// resolveUser falls back from an explicit user to the config, then to the
// current user.
func (c *Config) resolveUser(host, userName string) string {
	if userName == "" {
		userName = c.get(host, "User")
	}

	if userName == "" {
		if u, err := user.Current(); err == nil {
			userName = u.Username
		}
	}

	return userName
}

// resolvePort falls back from an explicit port to the config, then to 22.
func (c *Config) resolvePort(host string, port int) int {
	if port == 0 {
		if p, err := strconv.Atoi(c.get(host, "Port")); err == nil && p > 0 {
			port = p
		}
	}

	if port == 0 {
		port = 22
	}

	return port
}

func (c *Config) get(alias, key string) string {
	if c.cfg == nil {
		return ""
	}

	v, _ := c.cfg.Get(strings.ToLower(alias), key)

	return v
}

func (c *Config) getAll(alias, key string) []string {
	if c.cfg == nil {
		return nil
	}

	v, _ := c.cfg.GetAll(strings.ToLower(alias), key)

	return v
}

func expandTilde(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}

	return filepath.Join(home, p[2:])
}

// expandTokens replaces the ssh_config percent tokens that make sense for an
// IdentityFile: %% %d (local home) %h (host name) %n (alias) %p (port) %r (remote
// user) %u (local user). Unknown tokens are kept as written.
func expandTokens(s string, t Target) string {
	if !strings.Contains(s, "%") {
		return s
	}

	var sb strings.Builder

	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+1 == len(s) {
			sb.WriteByte(s[i])

			continue
		}

		if v, ok := tokenValue(s[i+1], t); ok {
			sb.WriteString(v)

			i++

			continue
		}

		sb.WriteByte(s[i])
	}

	return sb.String()
}

func tokenValue(c byte, t Target) (string, bool) {
	switch c {
	case '%':
		return "%", true
	case 'd':
		home, err := os.UserHomeDir()

		return home, err == nil
	case 'h':
		return t.Host, true
	case 'n':
		return t.Alias, true
	case 'p':
		return strconv.Itoa(t.Port), true
	case 'r':
		return t.User, true
	case 'u':
		u, err := user.Current()
		if err != nil {
			return "", false
		}

		return u.Username, true
	default:
		return "", false
	}
}

// Candidate is one row of the connect dialog.
type Candidate struct {
	Text       string // what gets connected to, e.g. "nas" or "pi@nas:/srv"
	Alias      bool   // defined in ssh_config
	LastUsed   time.Time
	ResolvedAs string // "user@host:port", shown dimmed
}

// HistoryItem is the subset of a history entry Merge needs.
type HistoryItem struct {
	Target string
	Last   time.Time
}

// Merge combines history (most recent first) and ssh_config aliases. Entries that
// resolve to the same destination are shown once, keeping the most recent spelling.
// resolve maps a candidate text to its destination key (host, port, user and path).
func Merge(aliases []string, history []HistoryItem, resolve func(text string) string) []Candidate {
	seen := map[string]bool{}
	out := make([]Candidate, 0, len(aliases)+len(history))

	add := func(c Candidate) {
		key := resolve(c.Text)

		if key != "" && seen[key] {
			return
		}

		seen[key] = true
		c.ResolvedAs = key
		out = append(out, c)
	}

	for _, h := range history {
		add(Candidate{Text: h.Target, LastUsed: h.Last, Alias: slices.Contains(aliases, hostOf(h.Target))})
	}

	for _, a := range aliases {
		add(Candidate{Text: a, Alias: true})
	}

	return out
}

// hostOf extracts the host from "user@host:path" style text.
func hostOf(text string) string {
	text = strings.TrimPrefix(text, "ssh://")
	text, _, _ = strings.Cut(text, "/")

	if _, h, ok := strings.Cut(text, "@"); ok {
		text = h
	}

	h, _, _ := strings.Cut(text, ":")

	return strings.ToLower(h)
}
