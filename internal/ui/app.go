// Package ui renders the recent-workspaces picker: a fuzzy-filterable list of
// folders the user has opened as Herdr workspaces. Selecting one opens (or
// re-focuses) that workspace. A second mode — the folder browser — lets the user
// walk the filesystem to open a folder that isn't in the history yet; opening it
// records it for next time.
package ui

import (
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
	browseCursor  int             // 0 = "open this folder"; k>0 = browseVisible[k-1]
	browseOffset  int

	width, height int
	status        string
	statusErr     bool
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
func (m Model) Init() tea.Cmd { return nil }

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
	start := herdr.WorkspaceCwd()
	if start == "" {
		if home, err := os.UserHomeDir(); err == nil {
			start = home
		} else {
			start = "."
		}
	}
	m.browseDir = start
	m.loadBrowseDir()
	return m
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
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "esc":
		m.mode = modeList
		m.status = ""
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
	b.WriteString(promptStyle.Render("❯ ") + m.browseFilter)
	b.WriteString("\n\n")

	rows := m.browseRows()
	h := m.listHeight()
	end := min(m.browseOffset+h, len(rows))
	for i := m.browseOffset; i < end; i++ {
		b.WriteString(rows[i])
		b.WriteByte('\n')
	}
	return b.String() + m.footer("↑↓ move · →/tab into · ← up · enter open · ctrl+o open this folder · esc back")
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
