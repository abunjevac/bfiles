package sshcfg

import (
	"bytes"
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
type Config struct {
	cfg *ssh_config.Config
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

	cfg, err := ssh_config.Decode(bytes.NewReader(data))
	if err != nil {
		return &Config{}, fmt.Errorf("parse-ssh-config: %w", err)
	}

	return &Config{cfg: cfg}, nil
}

// DefaultPath returns ~/.ssh/config.
func DefaultPath() string {
	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".ssh", "config")
}

// Aliases returns the concrete host aliases defined in the file, in file order,
// skipping wildcard and negated patterns.
func (c *Config) Aliases() []string {
	if c.cfg == nil {
		return nil
	}

	var out []string

	for _, h := range c.cfg.Hosts {
		for _, p := range h.Patterns {
			s := p.String()

			if strings.ContainsAny(s, "*?!") || slices.Contains(out, s) {
				continue
			}

			out = append(out, s)
		}
	}

	return out
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
		t.IdentityFiles = append(t.IdentityFiles, expandTilde(f))
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

	v, _ := c.cfg.Get(alias, key)

	return v
}

func (c *Config) getAll(alias, key string) []string {
	if c.cfg == nil {
		return nil
	}

	v, _ := c.cfg.GetAll(alias, key)

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

	return h
}
