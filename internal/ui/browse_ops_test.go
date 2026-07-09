package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func typeRunes(m Model, s string) Model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return next.(Model)
}

func press(m Model, t tea.KeyType) Model {
	next, _ := m.Update(tea.KeyMsg{Type: t})
	return next.(Model)
}

func TestValidName(t *testing.T) {
	for _, bad := range []string{"", " ", ".", "..", "a/b", `a\b`} {
		if validName(bad) {
			t.Errorf("validName(%q) should be false", bad)
		}
	}
	for _, ok := range []string{"proj", "my-app", "a.b"} {
		if !validName(ok) {
			t.Errorf("validName(%q) should be true", ok)
		}
	}
}

func TestBrowseCreateFolder(t *testing.T) {
	base := browseTree(t)
	m := key(New(), "ctrl+o")

	m = press(m, tea.KeyCtrlA) // open create prompt
	if m.browsePromptKind != promptCreate {
		t.Fatal("ctrl+a should open the create prompt")
	}
	m = typeRunes(m, "newproj")
	m = press(m, tea.KeyEnter)

	if _, err := os.Stat(filepath.Join(base, "newproj")); err != nil {
		t.Fatalf("folder was not created: %v", err)
	}
	if m.browsePromptKind != promptNone {
		t.Fatal("prompt should close after create")
	}
	// the browser should now list and select it
	if name, _ := m.browseChild(); name != "newproj" {
		t.Fatalf("new folder should be selected, got %q", name)
	}
}

func TestBrowseRenameFolder(t *testing.T) {
	base := browseTree(t) // has alpha, beta
	m := key(New(), "ctrl+o")
	// cursor starts on alpha; rename it
	m = press(m, tea.KeyCtrlR)
	if m.browsePromptKind != promptRename || m.browseRenameFrom != "alpha" {
		t.Fatalf("ctrl+r should start renaming alpha, got %+v/%q", m.browsePromptKind, m.browseRenameFrom)
	}
	// clear the prefilled name and type a new one
	for range "alpha" {
		m = press(m, tea.KeyBackspace)
	}
	m = typeRunes(m, "renamed")
	m = press(m, tea.KeyEnter)

	if _, err := os.Stat(filepath.Join(base, "renamed")); err != nil {
		t.Fatalf("rename target missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "alpha")); !os.IsNotExist(err) {
		t.Fatalf("old folder should be gone, stat err=%v", err)
	}
}

func TestBrowseDeleteFolderConfirmed(t *testing.T) {
	base := browseTree(t)
	// make alpha non-empty to prove recursive delete works
	if err := os.MkdirAll(filepath.Join(base, "alpha", "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := key(New(), "ctrl+o") // cursor on alpha

	m = press(m, tea.KeyCtrlD)
	if m.browsePromptKind != promptDelete {
		t.Fatal("ctrl+d should ask for delete confirmation")
	}
	m = typeRunes(m, "y") // confirm

	if _, err := os.Stat(filepath.Join(base, "alpha")); !os.IsNotExist(err) {
		t.Fatalf("folder should be deleted, stat err=%v", err)
	}
	if m.browsePromptKind != promptNone {
		t.Fatal("prompt should close after delete")
	}
}

func TestBrowseDeleteCancelled(t *testing.T) {
	base := browseTree(t)
	m := key(New(), "ctrl+o")
	m = press(m, tea.KeyCtrlD)
	m = typeRunes(m, "n") // anything but y cancels

	if _, err := os.Stat(filepath.Join(base, "alpha")); err != nil {
		t.Fatalf("cancelled delete must keep the folder: %v", err)
	}
	if m.browsePromptKind != promptNone {
		t.Fatal("prompt should close after cancel")
	}
}
