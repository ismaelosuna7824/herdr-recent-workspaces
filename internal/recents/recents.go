// Package recents persists the list of folders the user has opened as Herdr
// workspaces. Herdr itself keeps no "recent folders" history (the socket API has
// no such endpoint), so this plugin maintains its own: a small JSON file that is
// seeded from the currently-open workspaces and bumped whenever the user opens a
// folder through the picker.
package recents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// maxEntries caps the history so the file never grows without bound. The oldest
// entries beyond this count are dropped on save.
const maxEntries = 100

// Entry is one remembered folder.
type Entry struct {
	Path       string `json:"path"`
	Label      string `json:"label,omitempty"`
	LastOpened int64  `json:"last_opened"` // unix seconds
}

// Store is the in-memory history plus the file it was loaded from.
type Store struct {
	path    string
	entries []Entry
}

// DefaultPath resolves where recents.json lives. Herdr gives each plugin a
// private config dir via HERDR_PLUGIN_CONFIG_DIR; fall back to the user's config
// dir when running outside a pane (e.g. tests, manual runs).
func DefaultPath() string {
	if dir := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "recents.json")
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "herdr-recent-workspaces", "recents.json")
	}
	return "recents.json"
}

// Load reads the history from path. A missing or unreadable file yields an empty
// store bound to that path, so a later Save creates it.
func Load(path string) *Store {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s.entries)
	return s
}

// Path returns the file this store is bound to.
func (s *Store) Path() string { return s.path }

// Touch records that path was just opened at time now: it upserts the entry to
// the front (most recent) and refreshes its label when one is given.
func (s *Store) Touch(path, label string, now int64) {
	path = normalize(path)
	if path == "" {
		return
	}
	for i := range s.entries {
		if s.entries[i].Path == path {
			s.entries[i].LastOpened = now
			if label != "" {
				s.entries[i].Label = label
			}
			return
		}
	}
	s.entries = append(s.entries, Entry{Path: path, Label: label, LastOpened: now})
}

// Seed adds path with timestamp ts only if it is not already known, leaving
// existing timestamps untouched. It is used to fold currently-open workspaces
// into the history without disturbing the real recency order.
func (s *Store) Seed(path, label string, ts int64) {
	path = normalize(path)
	if path == "" {
		return
	}
	for i := range s.entries {
		if s.entries[i].Path == path {
			if s.entries[i].Label == "" && label != "" {
				s.entries[i].Label = label
			}
			return
		}
	}
	s.entries = append(s.entries, Entry{Path: path, Label: label, LastOpened: ts})
}

// Remove deletes the entry for path, if present. Returns true when something
// was removed.
func (s *Store) Remove(path string) bool {
	path = normalize(path)
	for i := range s.entries {
		if s.entries[i].Path == path {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return true
		}
	}
	return false
}

// PruneMissing drops entries whose directory no longer exists on disk.
func (s *Store) PruneMissing() {
	kept := s.entries[:0]
	for _, e := range s.entries {
		if info, err := os.Stat(e.Path); err == nil && info.IsDir() {
			kept = append(kept, e)
		}
	}
	s.entries = kept
}

// Sorted returns the entries most-recent-first.
func (s *Store) Sorted() []Entry {
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastOpened > out[j].LastOpened
	})
	return out
}

// Save writes the history to disk, newest first and capped to maxEntries. The
// write is atomic (temp file + rename) so a crash never truncates the history.
func (s *Store) Save() error {
	if s.path == "" {
		return nil
	}
	entries := s.Sorted()
	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// normalize cleans a path for stable comparison. Empty stays empty.
func normalize(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}
