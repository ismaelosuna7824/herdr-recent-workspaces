package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// setupEnv seeds a recents.json and a session.json in a temp env so New() builds
// a deterministic model. It returns the two workspace directories it created.
func setupEnv(t *testing.T) (dirA, dirB string) {
	t.Helper()
	base := t.TempDir()
	dirA = filepath.Join(base, "api")
	dirB = filepath.Join(base, "web")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := filepath.Join(base, "cfg")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	recents := `[
	  {"path": "` + dirA + `", "label": "api", "last_opened": 100},
	  {"path": "` + dirB + `", "label": "web", "last_opened": 200}
	]`
	if err := os.WriteFile(filepath.Join(cfg, "recents.json"), []byte(recents), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", cfg)

	// session.json marks dirA as an open workspace.
	sock := filepath.Join(base, "herdr.sock")
	session := map[string]any{
		"workspaces": []map[string]any{
			{"id": "wA", "identity_cwd": dirA, "custom_name": nil},
		},
	}
	data, _ := json.Marshal(session)
	if err := os.WriteFile(filepath.Join(base, "session.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_SOCKET_PATH", sock)
	return dirA, dirB
}

func TestNewBuildsSortedItemsWithOpenBadge(t *testing.T) {
	dirA, _ := setupEnv(t)
	m := New()
	if len(m.items) != 2 {
		t.Fatalf("want 2 items, got %d", len(m.items))
	}
	// web (200) is newer than api (100), so it sorts first.
	if m.items[0].label != "web" {
		t.Fatalf("want web first, got %q", m.items[0].label)
	}
	// dirA is open → should carry an open id.
	var apiItem item
	for _, it := range m.items {
		if it.entry.Path == dirA {
			apiItem = it
		}
	}
	if apiItem.openID != "wA" {
		t.Fatalf("expected api to be flagged open, got %+v", apiItem)
	}
}

func TestFilterNarrowsAndViewRenders(t *testing.T) {
	setupEnv(t)
	m := New()
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = sized.(Model)

	// type "web"
	for _, r := range "web" {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	if len(m.visible) != 1 {
		t.Fatalf("filter should leave 1 match, got %d", len(m.visible))
	}
	view := m.View()
	if !strings.Contains(view, "web") {
		t.Fatalf("view should show the matched row; got:\n%s", view)
	}
	if strings.Contains(view, " api ") {
		t.Fatalf("filtered-out row should not render:\n%s", view)
	}
}

func TestRemoveSelectedPersists(t *testing.T) {
	setupEnv(t)
	m := New()
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = sized.(Model)

	// top row is "web" (newest, not open): forget removes it, no close command.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("forgetting a non-open entry should not issue a close command")
	}
	if len(m.items) != 1 || m.items[0].label != "api" {
		t.Fatalf("remove left wrong state: %+v", m.items)
	}
	// reload from disk to confirm it persisted
	reloaded := New()
	if len(reloaded.items) != 1 || reloaded.items[0].label != "api" {
		t.Fatalf("removal not persisted: %+v", reloaded.items)
	}
}

func TestForgetOpenEntryClosesWorkspace(t *testing.T) {
	setupEnv(t) // dirA (api) is flagged open as workspace "wA"
	m := New()
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = sized.(Model)

	// move down to "api" (the open one) and forget it
	down, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = down.(Model)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = next.(Model)

	if cmd == nil {
		t.Fatal("forgetting an open entry must issue a close command")
	}
	if len(m.items) != 1 || m.items[0].label != "web" {
		t.Fatalf("api should be gone, got %+v", m.items)
	}
	for _, w := range m.open {
		if w.ID == "wA" {
			t.Fatal("closed workspace should be dropped from the open set")
		}
	}
}

// TestNewPersistsSeededOpenWorkspace proves that a workspace known only to Herdr
// (present in session.json, absent from recents.json) is written to recents.json
// at launch. Without this, closing it via Herdr's native menu — which the plugin
// cannot observe — would lose it from the history entirely.
func TestNewPersistsSeededOpenWorkspace(t *testing.T) {
	base := t.TempDir()
	openDir := filepath.Join(base, "service")
	if err := os.MkdirAll(openDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := filepath.Join(base, "cfg")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	recentsPath := filepath.Join(cfg, "recents.json")
	// recents.json starts empty — the open workspace is unknown to the plugin.
	if err := os.WriteFile(recentsPath, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", cfg)

	sock := filepath.Join(base, "herdr.sock")
	session := map[string]any{
		"workspaces": []map[string]any{
			{"id": "wS", "identity_cwd": openDir, "custom_name": "service"},
		},
	}
	data, _ := json.Marshal(session)
	if err := os.WriteFile(filepath.Join(base, "session.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_SOCKET_PATH", sock)

	New() // building the model must persist the seeded open workspace

	raw, err := os.ReadFile(recentsPath)
	if err != nil {
		t.Fatalf("recents.json should exist after New(): %v", err)
	}
	var entries []struct {
		Path  string `json:"path"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want the open workspace persisted, got %d entries: %s", len(entries), raw)
	}
	if entries[0].Path != openDir || entries[0].Label != "service" {
		t.Fatalf("persisted entry mismatch: %+v", entries[0])
	}
}

func TestEscQuits(t *testing.T) {
	setupEnv(t)
	m := New()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should return a quit command")
	}
}
