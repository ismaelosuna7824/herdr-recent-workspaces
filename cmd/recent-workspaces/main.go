// Command recent-workspaces is the terminal UI launched inside a Herdr pane. It
// lists the folders the user has recently opened as Herdr workspaces and, on
// selection, opens (or re-focuses) that workspace.
//
// Herdr keeps no "recent folders" history of its own, so the plugin maintains
// one: seeded from the currently-open workspaces (read from session.json) and
// bumped whenever a folder is opened through this picker.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ismaelosuna7824/herdr-recent-workspaces/internal/ui"
)

// version is injected at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	ui.SetVersion(version)

	p := tea.NewProgram(ui.New(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "recent-workspaces:", err)
		os.Exit(1)
	}
}
