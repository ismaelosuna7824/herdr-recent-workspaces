// Package ui renders the recent-workspaces picker: a fuzzy-filterable list of
// folders the user has opened as Herdr workspaces. Selecting one opens (or
// re-focuses) that workspace. A second mode — the folder browser — lets the user
// walk the filesystem to open a folder that isn't in the history yet; opening it
// records it for next time.
package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ismaelosuna7824/herdr-recent-workspaces/internal/finder"
	"github.com/ismaelosuna7824/herdr-recent-workspaces/internal/herdr"
	"github.com/ismaelosuna7824/herdr-recent-workspaces/internal/recents"
	"github.com/ismaelosuna7824/herdr-recent-workspaces/internal/update"
)

// version is set from main via SetVersion.
var version = "dev"

// SetVersion records the build version shown in the footer.
func SetVersion(v string) { version = v }

// mode is the active screen.
type mode int

const (
	modeList   mode = iota // the recents list
	modeBrowse             // the folder browser
)

// browsePrompt is the active inline prompt overlaying the folder browser.
type browsePrompt int

const (
	promptNone   browsePrompt = iota
	promptCreate              // typing a name for a new folder
	promptRename              // typing a new name for the highlighted folder
	promptDelete              // confirming deletion of the highlighted folder
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	selStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	pathStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	metaStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	openStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	dirStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
)

// item is one row in the recents list.
type item struct {
	entry   recents.Entry
	label   string // display label (custom name or basename)
	display string // string fed to the fuzzy matcher (label + path)
	openID  string // Herdr workspace id if currently open, else ""
}

// openedMsg is returned after attempting to open a workspace.
type openedMsg struct {
	err error
	out string
}

// closedMsg is returned after attempting to close a live workspace on forget.
type closedMsg struct {
	err   error
	out   string
	label string
}

// updateMsg carries a newer release tag (empty = up to date).
type updateMsg struct{ latest string }

// Model is the Bubbletea model for the picker.
type Model struct {
	store *recents.Store
	open  []herdr.Workspace
	now   int64
	mode  mode

	// recents list state
	items   []item
	byID    map[string]int
	matcher *finder.Matcher
	visible []int
	query   string
	cursor  int
	offset  int

	// folder browser state
	browseDir     string
	browseFilter  string
	browseNames   []string        // subdirectory names in browseDir
	browseMatcher *finder.Matcher // over browseNames
	browseVisible []int           // indices into browseNames, filtered
	browseCursor  int             // index into browseVisible
	browseOffset  int

	// inline create/rename/delete prompt over the browser
	browsePromptKind browsePrompt
	browseInput      string // text being typed for create/rename
	browseRenameFrom string // original name during a rename

	width, height int
	status        string
	statusErr     bool
	updateLatest  string // newer release tag, if one is available
	quitting      bool
}

// New builds the picker: it loads the recents history, folds in the currently
// open workspaces, and prepares the fuzzy matcher.
func New() Model {
	now := time.Now().Unix()
	store := recents.Load(recents.DefaultPath())

	open, _ := herdr.OpenWorkspaces()
	for _, w := range open {
		store.Seed(w.Cwd, w.Label, now)
	}
	store.PruneMissing()
	// Persist the seeded open workspaces now, while they are still open. Herdr's
	// native "Close" removes a workspace from session.json without notifying the
	// plugin, so if we only kept the seed in memory, a folder opened outside the
	// picker and later closed in Herdr would never make it into the history.
	_ = store.Save()

	m := Model{store: store, open: open, now: now, mode: modeList}
	m.rebuild()
	return m
}

// rebuild regenerates the recents item list and matcher from the store.
func (m *Model) rebuild() {
	openByCwd := make(map[string]string, len(m.open))
	for _, w := range m.open {
		openByCwd[w.Cwd] = w.ID
	}

	entries := m.store.Sorted()
	m.items = make([]item, 0, len(entries))
	m.byID = make(map[string]int, len(entries))
	displays := make([]string, 0, len(entries))
	for _, e := range entries {
		label := e.Label
		if label == "" {
			label = filepath.Base(e.Path)
		}
		display := label + " " + e.Path
		it := item{entry: e, label: label, display: display, openID: openByCwd[e.Path]}
		m.byID[display] = len(m.items)
		m.items = append(m.items, it)
		displays = append(displays, display)
	}
	m.matcher = finder.NewMatcher(displays)
	m.filter()
}

// filter recomputes the visible recents rows from the current query.
func (m *Model) filter() {
	cands := m.matcher.Match(m.query, 0)
	m.visible = make([]int, 0, len(cands))
	for _, c := range cands {
		if idx, ok := m.byID[c.Path]; ok {
			m.visible = append(m.visible, idx)
		}
	}
	m.cursor = clamp(m.cursor, len(m.visible))
	m.clampOffset(&m.cursor, &m.offset, len(m.visible))
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return checkUpdateCmd() }

// checkUpdateCmd asks GitHub for the latest release and reports it if it's newer
// than the running build. Best-effort: errors just yield "no update".
func checkUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		latest, err := update.Latest(context.Background())
		if err != nil || !update.IsNewer(latest, version) {
			return updateMsg{}
		}
		return updateMsg{latest: latest}
	}
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case openedMsg:
		if msg.err != nil {
			m.status = "could not open: " + strings.TrimSpace(msg.out)
			if strings.TrimSpace(msg.out) == "" {
				m.status = "could not open: " + msg.err.Error()
			}
			m.statusErr = true
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit

	case updateMsg:
		m.updateLatest = msg.latest
		return m, nil

	case closedMsg:
		if msg.err != nil {
			m.status = "forgot " + msg.label + ", but closing the workspace failed: " + strings.TrimSpace(msg.out)
			if strings.TrimSpace(msg.out) == "" {
				m.status = "forgot " + msg.label + ", but closing the workspace failed: " + msg.err.Error()
			}
			m.statusErr = true
		}
		return m, nil

	case tea.KeyMsg:
		if m.mode == modeBrowse {
			return m.handleBrowseKey(msg)
		}
		return m.handleListKey(msg)
	}
	return m, nil
}

// ---- recents list mode ----

func (m Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		m.quitting = true
		return m, tea.Quit

	case "up", "ctrl+p":
		if m.cursor > 0 {
			m.cursor--
			m.clampOffset(&m.cursor, &m.offset, len(m.visible))
		}
		return m, nil

	case "down", "ctrl+n":
		if m.cursor < len(m.visible)-1 {
			m.cursor++
			m.clampOffset(&m.cursor, &m.offset, len(m.visible))
		}
		return m, nil

	case "enter":
		return m.openSelected()

	case "ctrl+o":
		return m.enterBrowse(), nil

	case "ctrl+x":
		return m.removeSelected()

	case "ctrl+u":
		m.query = ""
		m.cursor = 0
		m.filter()
		return m, nil

	case "backspace":
		if m.query != "" {
			m.query = m.query[:len(m.query)-1]
			m.cursor = 0
			m.filter()
		}
		return m, nil
	}

	if len(msg.Runes) > 0 {
		m.query += string(msg.Runes)
		m.cursor = 0
		m.filter()
	}
	return m, nil
}

func (m Model) openSelected() (tea.Model, tea.Cmd) {
	it, ok := m.selected()
	if !ok {
		return m, nil
	}
	return m.openPath(it.entry.Path, it.label)
}

// removeSelected forgets the highlighted entry and, when that folder is a
// currently-open workspace, also closes it in Herdr.
func (m Model) removeSelected() (tea.Model, tea.Cmd) {
	it, ok := m.selected()
	if !ok {
		return m, nil
	}
	m.store.Remove(it.entry.Path)
	_ = m.store.Save()

	var cmd tea.Cmd
	if it.openID != "" {
		cmd = runClose(herdr.CloseCommand(it.openID), it.label)
		m.open = removeWorkspace(m.open, it.openID)
		m.status = "closed and forgot " + it.label
	} else {
		m.status = "removed " + it.label + " from recents"
	}
	m.statusErr = false
	m.rebuild()
	return m, cmd
}

func runClose(cmd *exec.Cmd, label string) tea.Cmd {
	return func() tea.Msg {
		out, err := cmd.CombinedOutput()
		return closedMsg{err: err, out: string(out), label: label}
	}
}

// removeWorkspace returns ws without the workspace whose id matches.
func removeWorkspace(ws []herdr.Workspace, id string) []herdr.Workspace {
	out := ws[:0]
	for _, w := range ws {
		if w.ID != id {
			out = append(out, w)
		}
	}
	return out
}

func (m Model) selected() (item, bool) {
	if m.cursor < 0 || m.cursor >= len(m.visible) {
		return item{}, false
	}
	return m.items[m.visible[m.cursor]], true
}

// ---- folder browser mode ----

func (m Model) enterBrowse() Model {
	m.mode = modeBrowse
	m.status = ""
	m.browseDir = browseStartDir()
	m.loadBrowseDir()
	return m
}

// browseStartDir is the directory the folder browser opens at. It defaults to
// ~/Documents — where projects usually live — so browsing always starts from a
// stable, shallow place instead of wherever the picker happened to be launched
// (which, deep in a tree, forces the user to climb back out every time). Set
// HERDR_RW_BROWSE_ROOT to start somewhere else. Falls back to the home dir, then
// the current directory.
func browseStartDir() string {
	if root := os.Getenv("HERDR_RW_BROWSE_ROOT"); root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return filepath.Clean(root)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if docs := filepath.Join(home, "Documents"); isDir(docs) {
			return docs
		}
		return home
	}
	return "."
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// loadBrowseDir reads the subdirectories of browseDir and resets the browser.
func (m *Model) loadBrowseDir() {
	m.browseNames = listSubdirs(m.browseDir)
	m.browseMatcher = finder.NewMatcher(m.browseNames)
	m.browseFilter = ""
	m.browseCursor = 0
	m.browseOffset = 0
	m.filterBrowse()
}

// filterBrowse recomputes the visible subdirectories from the browse filter.
func (m *Model) filterBrowse() {
	idxByName := make(map[string]int, len(m.browseNames))
	for i, n := range m.browseNames {
		idxByName[n] = i
	}
	cands := m.browseMatcher.Match(m.browseFilter, 0)
	m.browseVisible = make([]int, 0, len(cands))
	for _, c := range cands {
		if idx, ok := idxByName[c.Path]; ok {
			m.browseVisible = append(m.browseVisible, idx)
		}
	}
	m.browseCursor = clamp(m.browseCursor, len(m.browseVisible))
	m.clampOffset(&m.browseCursor, &m.browseOffset, len(m.browseVisible))
}

func (m Model) handleBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.browsePromptKind != promptNone {
		return m.handleBrowsePrompt(msg), nil
	}

	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "esc":
		m.mode = modeList
		m.status = ""
		return m, nil

	case "ctrl+a": // new folder in the current directory
		m.browsePromptKind = promptCreate
		m.browseInput = ""
		return m, nil

	case "ctrl+r": // rename the highlighted folder
		if name, ok := m.browseChild(); ok {
			m.browsePromptKind = promptRename
			m.browseRenameFrom = name
			m.browseInput = name
		}
		return m, nil

	case "ctrl+d": // delete the highlighted folder (with confirmation)
		if _, ok := m.browseChild(); ok {
			m.browsePromptKind = promptDelete
		}
		return m, nil

	case "up", "ctrl+p":
		if m.browseCursor > 0 {
			m.browseCursor--
			m.clampOffset(&m.browseCursor, &m.browseOffset, len(m.browseVisible))
		}
		return m, nil

	case "down", "ctrl+n":
		if m.browseCursor < len(m.browseVisible)-1 {
			m.browseCursor++
			m.clampOffset(&m.browseCursor, &m.browseOffset, len(m.browseVisible))
		}
		return m, nil

	case "right", "tab":
		if name, ok := m.browseChild(); ok {
			m.browseDir = filepath.Join(m.browseDir, name)
			m.loadBrowseDir()
		}
		return m, nil

	case "left":
		m.browseDir = filepath.Dir(m.browseDir)
		m.loadBrowseDir()
		return m, nil

	case "ctrl+o":
		// open the folder we're currently in, regardless of selection
		return m.openPath(m.browseDir, filepath.Base(m.browseDir))

	case "enter":
		target := m.browseEnterTarget()
		return m.openPath(target, filepath.Base(target))

	case "backspace":
		if m.browseFilter != "" {
			m.browseFilter = m.browseFilter[:len(m.browseFilter)-1]
			m.browseCursor = 0
			m.filterBrowse()
		} else {
			m.browseDir = filepath.Dir(m.browseDir)
			m.loadBrowseDir()
		}
		return m, nil
	}

	if len(msg.Runes) > 0 {
		m.browseFilter += string(msg.Runes)
		m.browseCursor = 0
		m.filterBrowse()
	}
	return m, nil
}

// browseChild returns the highlighted subdirectory name, if any.
func (m Model) browseChild() (string, bool) {
	if m.browseCursor < 0 || m.browseCursor >= len(m.browseVisible) {
		return "", false
	}
	return m.browseNames[m.browseVisible[m.browseCursor]], true
}

// browseEnterTarget is the directory enter opens: the highlighted subfolder, or
// the current folder when there are none to highlight.
func (m Model) browseEnterTarget() string {
	if name, ok := m.browseChild(); ok {
		return filepath.Join(m.browseDir, name)
	}
	return m.browseDir
}

// handleBrowsePrompt drives the create/rename/delete overlay.
func (m Model) handleBrowsePrompt(msg tea.KeyMsg) Model {
	if m.browsePromptKind == promptDelete {
		switch msg.String() {
		case "y", "Y":
			if name, ok := m.browseChild(); ok {
				m = m.doDelete(name)
			}
		}
		m.browsePromptKind = promptNone
		return m
	}

	// create / rename: a text field
	switch msg.String() {
	case "esc", "ctrl+c":
		m.browsePromptKind = promptNone
		m.browseInput = ""
		return m

	case "enter":
		name := m.browseInput
		if m.browsePromptKind == promptCreate {
			m = m.doCreate(name)
		} else {
			m = m.doRename(m.browseRenameFrom, name)
		}
		m.browsePromptKind = promptNone
		m.browseInput = ""
		return m

	case "backspace":
		if m.browseInput != "" {
			m.browseInput = m.browseInput[:len(m.browseInput)-1]
		}
		return m
	}

	if len(msg.Runes) > 0 {
		m.browseInput += string(msg.Runes)
	}
	return m
}

// doCreate makes a new folder in the current directory and selects it.
func (m Model) doCreate(name string) Model {
	name = strings.TrimSpace(name)
	if !validName(name) {
		m.status, m.statusErr = "invalid folder name", true
		return m
	}
	if err := os.Mkdir(filepath.Join(m.browseDir, name), 0o755); err != nil {
		m.status, m.statusErr = "create failed: "+err.Error(), true
		return m
	}
	m.status, m.statusErr = "created "+name, false
	m.loadBrowseDir()
	return m.selectBrowseName(name)
}

// doRename renames a subfolder, keeping the recents history consistent.
func (m Model) doRename(from, to string) Model {
	to = strings.TrimSpace(to)
	if !validName(to) {
		m.status, m.statusErr = "invalid folder name", true
		return m
	}
	if to == from {
		return m
	}
	src := filepath.Join(m.browseDir, from)
	dst := filepath.Join(m.browseDir, to)
	if _, err := os.Stat(dst); err == nil {
		m.status, m.statusErr = to+" already exists", true
		return m
	}
	if err := os.Rename(src, dst); err != nil {
		m.status, m.statusErr = "rename failed: "+err.Error(), true
		return m
	}
	if m.store.Remove(src) {
		_ = m.store.Save()
		m.rebuild()
	}
	m.status, m.statusErr = "renamed to "+to, false
	m.loadBrowseDir()
	return m.selectBrowseName(to)
}

// doDelete removes a subfolder and everything inside it.
func (m Model) doDelete(name string) Model {
	if !validName(name) {
		return m
	}
	target := filepath.Join(m.browseDir, name)
	if err := os.RemoveAll(target); err != nil {
		m.status, m.statusErr = "delete failed: "+err.Error(), true
		return m
	}
	if m.store.Remove(target) {
		_ = m.store.Save()
		m.rebuild()
	}
	m.status, m.statusErr = "deleted "+name, false
	m.loadBrowseDir()
	return m
}

// selectBrowseName moves the browser cursor onto the folder called name.
func (m Model) selectBrowseName(name string) Model {
	for k, idx := range m.browseVisible {
		if m.browseNames[idx] == name {
			m.browseCursor = k
			break
		}
	}
	m.clampOffset(&m.browseCursor, &m.browseOffset, len(m.browseVisible))
	return m
}

// validName rejects empty names, "." / "..", and anything with a path separator
// so operations stay inside the current directory.
func validName(n string) bool {
	n = strings.TrimSpace(n)
	if n == "" || n == "." || n == ".." {
		return false
	}
	return !strings.ContainsAny(n, `/\`)
}

// ---- shared open path ----

// openPath records target in the history and opens it as a workspace.
func (m Model) openPath(path, label string) (tea.Model, tea.Cmd) {
	if path == "" {
		return m, nil
	}
	m.store.Touch(path, label, m.now)
	_ = m.store.Save()
	cmd := herdr.OpenCommand(path, m.open)
	m.status = "opening " + label + "…"
	m.statusErr = false
	return m, runOpen(cmd)
}

func runOpen(cmd *exec.Cmd) tea.Cmd {
	return func() tea.Msg {
		out, err := cmd.CombinedOutput()
		return openedMsg{err: err, out: string(out)}
	}
}

// ---- view ----

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.mode == modeBrowse {
		return m.viewBrowse()
	}
	return m.viewList()
}

func (m Model) viewList() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Recent Workspaces"))
	b.WriteString(footerStyle.Render("  " + version))
	if m.updateLatest != "" {
		b.WriteString(openStyle.Render("   ⬆ " + m.updateLatest + " available (reinstall to update)"))
	}
	b.WriteByte('\n')
	b.WriteString(promptStyle.Render("❯ ") + m.query)
	b.WriteString("\n\n")

	if len(m.items) == 0 {
		b.WriteString(metaStyle.Render("No recent workspaces yet."))
		b.WriteByte('\n')
		b.WriteString(metaStyle.Render("Press ctrl+o to browse and open a folder."))
		return b.String() + m.footer("↑↓ move · enter open · ctrl+o open folder · esc quit")
	}
	if len(m.visible) == 0 {
		b.WriteString(metaStyle.Render("No match for \"" + m.query + "\""))
		b.WriteByte('\n')
		return b.String() + m.footer("↑↓ move · enter open · ctrl+o open folder · esc quit")
	}

	h := m.listHeight()
	end := min(m.offset+h, len(m.visible))
	for i := m.offset; i < end; i++ {
		b.WriteString(m.renderRow(i))
		b.WriteByte('\n')
	}
	return b.String() + m.footer("↑↓ move · enter open · ctrl+o open folder · ctrl+x forget/close · esc quit")
}

func (m Model) renderRow(i int) string {
	it := m.items[m.visible[i]]
	selected := i == m.cursor

	cursor := "  "
	label := it.label
	if selected {
		cursor = selStyle.Render("❯ ")
		label = selStyle.Render(label)
	}

	path := prettyPath(it.entry.Path)
	var meta string
	if it.openID != "" {
		meta = openStyle.Render("● open")
	} else {
		meta = metaStyle.Render(relTime(it.entry.LastOpened, m.now))
	}

	line := cursor + label + "  " + pathStyle.Render(path)
	if m.width > 0 {
		plain := "  " + it.label + "  " + path
		pad := m.width - lipgloss.Width(plain) - lipgloss.Width(meta) - 1
		if pad < 1 {
			pad = 1
		}
		line += strings.Repeat(" ", pad) + meta
	} else {
		line += "  " + meta
	}
	return line
}

func (m Model) viewBrowse() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Open a folder"))
	b.WriteByte('\n')
	b.WriteString(pathStyle.Render(prettyPath(m.browseDir)))
	b.WriteByte('\n')
	b.WriteString(m.browsePromptLine())
	b.WriteString("\n\n")

	rows := m.browseRows()
	h := m.listHeight()
	end := min(m.browseOffset+h, len(rows))
	for i := m.browseOffset; i < end; i++ {
		b.WriteString(rows[i])
		b.WriteByte('\n')
	}
	hints := "↑↓ move · →/tab into · ← up · enter open · ctrl+a new · ctrl+r rename · ctrl+d delete · esc back"
	if m.browsePromptKind != promptNone {
		hints = "enter confirm · esc cancel"
		if m.browsePromptKind == promptDelete {
			hints = "y delete · any other key cancels"
		}
	}
	return b.String() + m.footer(hints)
}

// browsePromptLine renders the filter box, or the active create/rename/delete
// prompt when one is open.
func (m Model) browsePromptLine() string {
	switch m.browsePromptKind {
	case promptCreate:
		return promptStyle.Render("New folder: ") + m.browseInput + "▏"
	case promptRename:
		return promptStyle.Render("Rename "+m.browseRenameFrom+" → ") + m.browseInput + "▏"
	case promptDelete:
		name, _ := m.browseChild()
		return errStyle.Render("Delete " + name + "/ and everything inside? (y/N)")
	default:
		return promptStyle.Render("❯ ") + m.browseFilter
	}
}

// browseRows builds one row per (filtered) subdirectory of the current folder.
func (m Model) browseRows() []string {
	if len(m.browseVisible) == 0 {
		return []string{"  " + metaStyle.Render("(no subfolders — ctrl+o opens this folder)")}
	}
	rows := make([]string, 0, len(m.browseVisible))
	for k, idx := range m.browseVisible {
		name := m.browseNames[idx]
		if k == m.browseCursor {
			rows = append(rows, selStyle.Render("❯ "+name+"/"))
		} else {
			rows = append(rows, "  "+dirStyle.Render(name+"/"))
		}
	}
	return rows
}

func (m Model) footer(hints string) string {
	out := "\n" + footerStyle.Render(hints)
	if m.status != "" {
		style := metaStyle
		if m.statusErr {
			style = errStyle
		}
		out += "\n" + style.Render(m.status)
	}
	return out
}

func (m Model) listHeight() int {
	h := m.height - 6 // title, prompt(s), blank, footer
	if h < 1 {
		return 1000 // unknown size (tests): don't clip
	}
	return h
}

func (m Model) clampOffset(cursor, offset *int, n int) {
	_ = n
	h := m.listHeight()
	if *cursor < *offset {
		*offset = *cursor
	}
	if *cursor >= *offset+h {
		*offset = *cursor - h + 1
	}
	if *offset < 0 {
		*offset = 0
	}
}

// listSubdirs returns the sorted directory names directly under dir.
func listSubdirs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func clamp(v, n int) int {
	if v >= n {
		v = n - 1
	}
	if v < 0 {
		v = 0
	}
	return v
}

// prettyPath contracts the home directory to ~ for readability.
func prettyPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + p[len(home):]
		}
	}
	return p
}

// relTime renders a compact "time since" label.
func relTime(then, now int64) string {
	d := now - then
	switch {
	case d < 0:
		return ""
	case d < 60:
		return "just now"
	case d < 3600:
		return fmt.Sprintf("%dm ago", d/60)
	case d < 86400:
		return fmt.Sprintf("%dh ago", d/3600)
	case d < 7*86400:
		return fmt.Sprintf("%dd ago", d/86400)
	default:
		return time.Unix(then, 0).Format("Jan 2")
	}
}
