package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/BurntSushi/toml"
)

// History is the list of past SSH targets, most recent first.
type History struct {
	Entries []HistoryEntry `toml:"entry"`
}

// HistoryEntry is one remembered connection target, as a location string.
type HistoryEntry struct {
	Target string    `toml:"target"`
	Last   time.Time `toml:"last"`
}

// historyMode keeps the list of hosts and users private to the owner.
const historyMode = 0o600

// HistoryPath returns the history file in dir.
func HistoryPath(dir string) string {
	return filepath.Join(dir, "history.toml")
}

// LoadHistory reads history.toml; a missing file yields an empty history.
func LoadHistory(dir string) (*History, error) {
	var h History

	data, err := os.ReadFile(HistoryPath(dir))
	if os.IsNotExist(err) {
		return &h, nil
	}

	if err != nil {
		return &h, fmt.Errorf("read-history: %w", err)
	}

	if _, err := toml.Decode(string(data), &h); err != nil {
		return &History{}, fmt.Errorf("decode-history: %w", err)
	}

	return &h, nil
}

// SaveHistory writes history.toml atomically.
func SaveHistory(dir string, h *History) error {
	var buf bytes.Buffer

	if err := toml.NewEncoder(&buf).Encode(h); err != nil {
		return fmt.Errorf("encode-history: %w", err)
	}

	return writeFileAtomic(HistoryPath(dir), buf.Bytes(), historyMode)
}

// Touch moves target to the front (adding it if new) and trims to limit entries.
func (h *History) Touch(target string, now time.Time, limit int) {
	h.Remove(target)

	h.Entries = slices.Insert(h.Entries, 0, HistoryEntry{Target: target, Last: now})

	if limit > 0 && len(h.Entries) > limit {
		h.Entries = h.Entries[:limit]
	}
}

// Remove deletes target from the history.
func (h *History) Remove(target string) {
	h.Entries = slices.DeleteFunc(h.Entries, func(e HistoryEntry) bool {
		return e.Target == target
	})
}
