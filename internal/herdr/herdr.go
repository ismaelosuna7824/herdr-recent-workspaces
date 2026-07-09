// Package herdr bridges the plugin to the Herdr runtime: it reads the set of
// currently-open workspaces (their directories are only available in Herdr's
// session.json — the socket API does not expose a workspace cwd) and builds the
// command that opens a folder as a workspace.
//
// Opening prefers reuse: if the folder is already an open workspace we focus it
// instead of creating a duplicate; otherwise we create a new focused workspace.
package herdr

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

// Workspace is a currently-open Herdr workspace.
type Workspace struct {
	ID    string // Herdr workspace id, e.g. "wC" (matches the socket API)
	Cwd   string // the workspace's directory (session.json "identity_cwd")
	Label string // user-assigned name, or "" when unnamed
}

// sessionFile mirrors the parts of Herdr's session.json we rely on.
type sessionFile struct {
	Workspaces []struct {
		ID          string `json:"id"`
		IdentityCwd string `json:"identity_cwd"`
		CustomName  string `json:"custom_name"`
		Tabs        []struct {
			Panes map[string]struct {
				Cwd string `json:"cwd"`
			} `json:"panes"`
		} `json:"tabs"`
	} `json:"workspaces"`
}

// Bin returns the Herdr CLI to invoke. Herdr injects HERDR_BIN_PATH into plugin
// processes; fall back to the name on PATH.
func Bin() string {
	if p := os.Getenv("HERDR_BIN_PATH"); p != "" {
		return p
	}
	return "herdr"
}

// SessionPath resolves Herdr's session.json. It sits next to the socket, so the
// socket path (injected as HERDR_SOCKET_PATH) gives us the config dir; fall back
// to the conventional location under the user's config dir.
func SessionPath() string {
	if sock := os.Getenv("HERDR_SOCKET_PATH"); sock != "" {
		return filepath.Join(filepath.Dir(sock), "session.json")
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "herdr", "session.json")
	}
	return ""
}

// OpenWorkspaces reads the currently-open workspaces from session.json. A
// missing or unpardable file yields no workspaces and no error — the plugin
// still works from its own recorded history.
func OpenWorkspaces() ([]Workspace, error) {
	path := SessionPath()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, nil
	}
	out := make([]Workspace, 0, len(sf.Workspaces))
	for _, w := range sf.Workspaces {
		cwd := w.IdentityCwd
		if cwd == "" {
			cwd = firstPaneCwd(w.Tabs)
		}
		if cwd == "" {
			continue
		}
		out = append(out, Workspace{ID: w.ID, Cwd: filepath.Clean(cwd), Label: w.CustomName})
	}
	return out, nil
}

// firstPaneCwd falls back to a tab's pane cwd when identity_cwd is absent.
func firstPaneCwd(tabs []struct {
	Panes map[string]struct {
		Cwd string `json:"cwd"`
	} `json:"panes"`
}) string {
	for _, t := range tabs {
		for _, p := range t.Panes {
			if p.Cwd != "" {
				return p.Cwd
			}
		}
	}
	return ""
}

// WorkspaceCwd returns the directory of the workspace the picker was launched
// from, read from Herdr's injected context JSON (flat "workspace_cwd" key). It
// is used as a sensible starting point for the folder browser. Returns "" when
// unavailable.
func WorkspaceCwd() string {
	raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")
	if raw == "" {
		return ""
	}
	var ctx map[string]any
	if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
		return ""
	}
	for _, key := range []string{"workspace_cwd", "focused_pane_cwd"} {
		if v, ok := ctx[key].(string); ok {
			if info, err := os.Stat(v); err == nil && info.IsDir() {
				return filepath.Clean(v)
			}
		}
	}
	return ""
}

// OpenCommand builds the command that opens path. If path is already an open
// workspace (matched by directory) it focuses that workspace; otherwise it
// creates a new focused workspace labelled with the folder's name.
func OpenCommand(path string, open []Workspace) *exec.Cmd {
	clean := filepath.Clean(path)
	for _, w := range open {
		if w.Cwd == clean && w.ID != "" {
			return exec.Command(Bin(), "workspace", "focus", w.ID)
		}
	}
	return exec.Command(Bin(), "workspace", "create", "--cwd", clean, "--label", filepath.Base(clean), "--focus")
}

// CloseCommand builds the command that closes the open workspace with id.
func CloseCommand(id string) *exec.Cmd {
	return exec.Command(Bin(), "workspace", "close", id)
}
