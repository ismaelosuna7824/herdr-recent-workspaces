package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// browseTree builds a temp directory with known subfolders and points New()'s
// browser start dir at it via HERDR_RW_BROWSE_ROOT.
func browseTree(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	for _, d := range []string{"alpha", "beta", "alpha/inner"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// isolate recents/session (in a separate temp dir so it is not a subfolder
	// of base) so New() builds an empty list and the browser sees only alpha/beta
	cfg := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", cfg)
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(cfg, "herdr.sock"))
	t.Setenv("HERDR_RW_BROWSE_ROOT", base)
	return base
}

func key(m Model, s string) Model {
	var msg tea.KeyMsg
	switch s {
	case "ctrl+o":
		msg = tea.KeyMsg{Type: tea.KeyCtrlO}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		msg = tea.KeyMsg{Type: tea.KeyLeft}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestBrowseStartDirDefaultsToDocuments(t *testing.T) {
	home := t.TempDir()
	docs := filepath.Join(home, "Documents")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("HERDR_RW_BROWSE_ROOT", "") // no override → default kicks in

	if got := browseStartDir(); got != docs {
		t.Fatalf("want default %q, got %q", docs, got)
	}

	// With no Documents dir, it falls back to home rather than a deep cwd.
	if err := os.Remove(docs); err != nil {
		t.Fatal(err)
	}
	if got := browseStartDir(); got != home {
		t.Fatalf("want home fallback %q, got %q", home, got)
	}
}

func TestBrowseStartsAtBrowseRootAndListsDirs(t *testing.T) {
	base := browseTree(t)
	m := key(New(), "ctrl+o")
	if m.mode != modeBrowse {
		t.Fatal("ctrl+o should enter browse mode")
	}
	if m.browseDir != base {
		t.Fatalf("browser should start at browse root %q, got %q", base, m.browseDir)
	}
	if len(m.browseNames) != 2 { // alpha, beta
		t.Fatalf("want 2 subdirs, got %v", m.browseNames)
	}
}

func TestBrowseFilterAndEnterTarget(t *testing.T) {
	browseTree(t)
	m := key(New(), "ctrl+o")
	m = key(m, "beta") // filter
	if len(m.browseVisible) != 1 {
		t.Fatalf("filter should leave 1 dir, got %d", len(m.browseVisible))
	}
	// cursor sits on the first (only) child; enter opens it
	if got := filepath.Base(m.browseEnterTarget()); got != "beta" {
		t.Fatalf("enter target should be beta, got %q", m.browseEnterTarget())
	}
}

func TestBrowseForwardWorksFromFirstChild(t *testing.T) {
	base := browseTree(t)
	m := key(New(), "ctrl+o")
	// cursor starts on the first child (alpha) — → must descend without needing
	// to move down first (the bug this replaced).
	m = key(m, "tab")
	if m.browseDir != filepath.Join(base, "alpha") {
		t.Fatalf("tab from the first child should descend into alpha, got %q", m.browseDir)
	}
	if len(m.browseNames) != 1 || m.browseNames[0] != "inner" {
		t.Fatalf("alpha should contain inner, got %v", m.browseNames)
	}
	m = key(m, "left") // ascend
	if m.browseDir != base {
		t.Fatalf("left should ascend back to base, got %q", m.browseDir)
	}
}

func TestBrowseCtrlOOpensCurrentFolder(t *testing.T) {
	base := browseTree(t)
	m := key(New(), "ctrl+o") // enter browse
	// ctrl+o again opens the current folder; verify it was recorded to recents.
	_ = key(m, "ctrl+o")
	reloaded := New()
	found := false
	for _, it := range reloaded.items {
		if it.entry.Path == base {
			found = true
		}
	}
	if !found {
		t.Fatalf("ctrl+o should open+record the current folder %q", base)
	}
}

func TestEscLeavesBrowseBackToList(t *testing.T) {
	browseTree(t)
	m := key(New(), "ctrl+o")
	m = key(m, "esc")
	if m.mode != modeList {
		t.Fatal("esc in browse should return to the list, not quit")
	}
}
