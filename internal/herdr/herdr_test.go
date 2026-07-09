package herdr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleSession = `{
  "workspaces": [
    {"id": "wC", "identity_cwd": "/Users/me/proj/api", "custom_name": null},
    {"id": "w2", "identity_cwd": "/Users/me/proj/web", "custom_name": "Frontend"},
    {"id": "w9", "identity_cwd": "", "custom_name": null,
      "tabs": [{"panes": {"1": {"cwd": "/Users/me/proj/infra"}}}]}
  ]
}`

func writeSession(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(dir, "herdr.sock"))
}

func TestOpenWorkspacesParsesIdentityAndPaneCwd(t *testing.T) {
	writeSession(t, sampleSession)
	ws, err := OpenWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 3 {
		t.Fatalf("want 3 workspaces, got %d: %+v", len(ws), ws)
	}
	if ws[0].ID != "wC" || ws[0].Cwd != "/Users/me/proj/api" {
		t.Fatalf("bad first workspace: %+v", ws[0])
	}
	if ws[1].Label != "Frontend" {
		t.Fatalf("custom_name not read: %+v", ws[1])
	}
	if ws[2].Cwd != "/Users/me/proj/infra" {
		t.Fatalf("pane cwd fallback failed: %+v", ws[2])
	}
}

func TestOpenWorkspacesMissingFileIsGraceful(t *testing.T) {
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(t.TempDir(), "herdr.sock"))
	ws, err := OpenWorkspaces()
	if err != nil {
		t.Fatalf("missing session.json should not error: %v", err)
	}
	if len(ws) != 0 {
		t.Fatalf("want no workspaces, got %+v", ws)
	}
}

func TestOpenCommandFocusesWhenAlreadyOpen(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", "herdr")
	open := []Workspace{{ID: "wC", Cwd: "/Users/me/proj/api"}}
	cmd := OpenCommand("/Users/me/proj/api", open)
	got := strings.Join(cmd.Args, " ")
	if !strings.Contains(got, "workspace focus wC") {
		t.Fatalf("expected focus for open workspace, got: %s", got)
	}
}

func TestOpenCommandCreatesWhenNotOpen(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", "herdr")
	cmd := OpenCommand("/Users/me/proj/new", nil)
	got := strings.Join(cmd.Args, " ")
	if !strings.Contains(got, "workspace create --cwd /Users/me/proj/new") {
		t.Fatalf("expected create, got: %s", got)
	}
	if !strings.Contains(got, "--label new") || !strings.Contains(got, "--focus") {
		t.Fatalf("create missing label/focus: %s", got)
	}
}

func TestOpenCommandMatchesAfterCleaning(t *testing.T) {
	open := []Workspace{{ID: "w2", Cwd: "/Users/me/proj/web"}}
	cmd := OpenCommand("/Users/me/proj/./web", open)
	if !strings.Contains(strings.Join(cmd.Args, " "), "focus w2") {
		t.Fatalf("path should match after cleaning: %v", cmd.Args)
	}
}
