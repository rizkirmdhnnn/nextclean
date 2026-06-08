package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type appState int

const (
	stateScanning appState = iota
	stateSelecting
	stateDeleting
	stateDone
)

// deleteResult records the outcome of removing one folder.
type deleteResult struct {
	path string
	size int64
	err  error
}

// --- messages ---

type scanDoneMsg struct{ targets []target }
type deleteDoneMsg deleteResult

// --- styles ---

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	checkedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	sizeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	footerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	selectedStyle = lipgloss.NewStyle().Bold(true)
	onStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	offStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

type model struct {
	opts    options
	state   appState
	spinner spinner.Model
	scanned *atomic.Int64

	items      []listItem
	visible    []int // indices into items after filtering, in display order
	cursor     int   // index into visible
	offset     int   // scroll offset into visible
	sortBySize bool

	// filter state
	searching   bool
	search      textinput.Model
	kindEnabled map[artifactKind]bool

	// deletion progress
	queue   []listItem
	delIdx  int
	results []deleteResult
	freed   int64

	width  int
	height int

	aborted bool
}

func newModel(opts options) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = cursorStyle

	ti := textinput.New()
	ti.Prompt = "search: "
	ti.Placeholder = "filter by path"

	return model{
		opts:       opts,
		state:      stateScanning,
		spinner:    sp,
		scanned:    &atomic.Int64{},
		sortBySize: true, // biggest space hogs first by default
		search:     ti,
		kindEnabled: map[artifactKind]bool{
			kindNext:  true,
			kindOut:   true,
			kindCache: true,
		},
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.scanCmd())
}

// scanCmd runs the (blocking) filesystem scan off the UI loop.
func (m model) scanCmd() tea.Cmd {
	opts, scanned := m.opts, m.scanned
	return func() tea.Msg {
		return scanDoneMsg{targets: collectTargets(opts, scanned)}
	}
}

// deleteCmd removes a single folder and reports the result.
func deleteCmd(it listItem) tea.Cmd {
	return func() tea.Msg {
		err := os.RemoveAll(it.path)
		return deleteDoneMsg{path: it.path, size: it.size, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		if m.state == stateScanning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case scanDoneMsg:
		m.applySort(msg.targets)
		if len(m.items) == 0 {
			m.state = stateDone
			return m, nil
		}
		m.state = stateSelecting
		return m, nil

	case deleteDoneMsg:
		res := deleteResult(msg)
		m.results = append(m.results, res)
		if res.err == nil {
			m.freed += res.size
		}
		m.delIdx++
		if m.delIdx < len(m.queue) {
			return m, deleteCmd(m.queue[m.delIdx])
		}
		m.state = stateDone
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Keep the search input's cursor animated while in search mode.
	if m.state == stateSelecting && m.searching {
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateSelecting:
		if m.searching {
			return m.handleSearchKey(msg)
		}
		return m.handleNavKey(msg)
	case stateDone:
		return m, tea.Quit
	}
	// During scanning / deleting, allow ctrl+c to bail out.
	if msg.String() == "ctrl+c" {
		m.aborted = true
		return m, tea.Quit
	}
	return m, nil
}

func (m model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.searching = false
		m.search.Blur()
		return m, nil
	case tea.KeyEsc:
		m.searching = false
		m.search.Blur()
		m.search.SetValue("")
		m.recompute()
		return m, nil
	case tea.KeyCtrlC:
		m.aborted = true
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.recompute()
	return m, cmd
}

func (m model) handleNavKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		m.aborted = true
		return m, tea.Quit
	case "/":
		m.searching = true
		cmd := m.search.Focus()
		return m, cmd
	case "1":
		m.toggleKind(kindNext)
	case "2":
		m.toggleKind(kindOut)
	case "3":
		m.toggleKind(kindCache)
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		m.clampOffset()
	case "down", "j":
		if m.cursor < len(m.visible)-1 {
			m.cursor++
		}
		m.clampOffset()
	case " ":
		if len(m.visible) > 0 {
			idx := m.visible[m.cursor]
			m.items[idx].checked = !m.items[idx].checked
		}
	case "a":
		m.toggleAllVisible()
	case "s":
		m.sortBySize = !m.sortBySize
		m.resort()
	case "enter":
		m.queue = m.visibleCheckedItems()
		if len(m.queue) == 0 {
			return m, nil // nothing selected; stay put
		}
		m.state = stateDeleting
		m.delIdx = 0
		return m, deleteCmd(m.queue[0])
	}
	return m, nil
}

// --- filter / selection helpers ---

// recompute rebuilds the visible index list from the current filter and clamps
// the cursor/scroll to it.
func (m *model) recompute() {
	m.visible = applyFilter(m.items, m.search.Value(), m.kindEnabled)
	if m.cursor >= len(m.visible) {
		m.cursor = len(m.visible) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.clampOffset()
}

func (m *model) toggleKind(k artifactKind) {
	m.kindEnabled[k] = !m.kindEnabled[k]
	m.cursor = 0
	m.offset = 0
	m.recompute()
}

// toggleAllVisible checks every visible item, or unchecks them all if they are
// already all checked. Hidden items are untouched.
func (m *model) toggleAllVisible() {
	if len(m.visible) == 0 {
		return
	}
	allChecked := true
	for _, idx := range m.visible {
		if !m.items[idx].checked {
			allChecked = false
			break
		}
	}
	for _, idx := range m.visible {
		m.items[idx].checked = !allChecked
	}
}

// visibleCheckedItems returns checked items that currently pass the filter —
// the "what you see is what you delete" rule.
func (m model) visibleCheckedItems() []listItem {
	var out []listItem
	for _, idx := range m.visible {
		if m.items[idx].checked {
			out = append(out, m.items[idx])
		}
	}
	return out
}

// selection counts the visible-checked items and their total size.
func (m model) selection() (count int, size int64) {
	for _, idx := range m.visible {
		if m.items[idx].checked {
			count++
			size += m.items[idx].size
		}
	}
	return
}

// applySort builds the item list from raw targets in the current sort order.
func (m *model) applySort(targets []target) {
	m.items = make([]listItem, len(targets))
	for i, t := range targets {
		m.items[i] = listItem{target: t}
	}
	m.resort()
}

// resort reorders items, preserving checkbox state, then refreshes the filter.
func (m *model) resort() {
	if m.sortBySize {
		sort.SliceStable(m.items, func(i, j int) bool {
			return m.items[i].size > m.items[j].size
		})
	} else {
		sort.SliceStable(m.items, func(i, j int) bool {
			return m.items[i].path < m.items[j].path
		})
	}
	m.cursor = 0
	m.offset = 0
	m.recompute()
}

// --- view ---

func (m model) View() string {
	switch m.state {
	case stateScanning:
		return fmt.Sprintf("\n %s Scanning... %d directories checked\n",
			m.spinner.View(), m.scanned.Load())
	case stateSelecting:
		return m.selectView()
	case stateDeleting:
		return m.deleteView()
	case stateDone:
		return m.doneView()
	}
	return ""
}

func (m model) visibleRows() int {
	// Reserve lines for title, filter line, spacing, and footer.
	rows := m.height - 6
	if rows < 1 {
		rows = 10
	}
	return rows
}

func (m *model) clampOffset() {
	rows := m.visibleRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m model) selectView() string {
	var b strings.Builder
	selCount, selSize := m.selection()
	title := titleStyle.Render(fmt.Sprintf(
		"Select build artifacts to delete  —  %d/%d selected, %s",
		selCount, len(m.visible), humanSize(selSize)))
	b.WriteString("\n ")
	b.WriteString(title)
	b.WriteString("\n ")
	b.WriteString(m.filterLine())
	b.WriteString("\n\n")

	if len(m.visible) == 0 {
		b.WriteString(footerStyle.Render(" no artifacts match the current filter (press 1/2/3 or clear search)"))
		b.WriteString("\n")
		b.WriteString(m.footerLine())
		b.WriteString("\n")
		return b.String()
	}

	rows := m.visibleRows()
	end := m.offset + rows
	if end > len(m.visible) {
		end = len(m.visible)
	}
	for vi := m.offset; vi < end; vi++ {
		it := m.items[m.visible[vi]]
		cursor := "  "
		if vi == m.cursor {
			cursor = cursorStyle.Render("▌ ")
		}
		box := "[ ]"
		if it.checked {
			box = checkedStyle.Render("[x]")
		}
		line := fmt.Sprintf("%s %s  %s", box, it.path, sizeStyle.Render("("+humanSize(it.size)+")"))
		if vi == m.cursor {
			line = selectedStyle.Render(line)
		}
		b.WriteString(" ")
		b.WriteString(cursor)
		b.WriteString(line)
		b.WriteString("\n")
	}

	if len(m.visible) != len(m.items) {
		b.WriteString(footerStyle.Render(fmt.Sprintf("\n %d of %d shown (filtered)", len(m.visible), len(m.items))))
	}
	b.WriteString(m.footerLine())
	b.WriteString("\n")
	return b.String()
}

func (m model) filterLine() string {
	tag := func(k artifactKind, hotkey string) string {
		s := fmt.Sprintf("[%s·%s %s]", hotkey, k.String(), onOff(m.kindEnabled[k]))
		if m.kindEnabled[k] {
			return onStyle.Render(s)
		}
		return offStyle.Render(s)
	}
	types := "types: " + tag(kindNext, "1") + " " + tag(kindOut, "2") + " " + tag(kindCache, "3")
	if m.searching || m.search.Value() != "" {
		return types + "    " + m.search.View()
	}
	return types
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (m model) footerLine() string {
	if m.searching {
		return footerStyle.Render("\n type to filter • enter apply • esc clear")
	}
	sortLabel := "path"
	if m.sortBySize {
		sortLabel = "size"
	}
	return footerStyle.Render(fmt.Sprintf(
		"\n / search • 1/2/3 types • ↑/↓ move • space toggle • a all • s sort (%s) • enter delete • q quit",
		sortLabel))
}

func (m model) deleteView() string {
	var b strings.Builder
	b.WriteString("\n ")
	b.WriteString(titleStyle.Render(fmt.Sprintf("Deleting... %d/%d", m.delIdx, len(m.queue))))
	b.WriteString("\n\n")
	start := 0
	if rows := m.visibleRows(); len(m.results) > rows {
		start = len(m.results) - rows
	}
	for _, r := range m.results[start:] {
		if r.err != nil {
			b.WriteString(" ")
			b.WriteString(errStyle.Render(fmt.Sprintf("✗ failed %s: %v", r.path, r.err)))
		} else {
			b.WriteString(" ")
			b.WriteString(okStyle.Render(fmt.Sprintf("✓ deleted %s (%s)", r.path, humanSize(r.size))))
		}
		b.WriteString("\n")
	}
	b.WriteString(footerStyle.Render(fmt.Sprintf("\n freed ~%s so far", humanSize(m.freed))))
	b.WriteString("\n")
	return b.String()
}

func (m model) doneView() string {
	if len(m.items) == 0 {
		return "\n Nothing to clean. No build artifacts found.\n\n Press any key to exit.\n"
	}
	if len(m.results) == 0 {
		return "\n Aborted. Nothing was deleted.\n\n Press any key to exit.\n"
	}
	var deleted, failed int
	for _, r := range m.results {
		if r.err == nil {
			deleted++
		} else {
			failed++
		}
	}
	var b strings.Builder
	headline := fmt.Sprintf("Done. Removed %d folder(s), freed ~%s.", deleted, humanSize(m.freed))
	b.WriteString("\n ")
	if deleted == 0 && failed > 0 {
		b.WriteString(errStyle.Render(headline))
	} else {
		b.WriteString(okStyle.Render(headline))
	}
	b.WriteString("\n")
	if failed > 0 {
		b.WriteString(" ")
		b.WriteString(errStyle.Render(fmt.Sprintf("%d folder(s) failed to delete.", failed)))
		b.WriteString("\n")
	}
	b.WriteString(footerStyle.Render("\n Press any key to exit."))
	b.WriteString("\n")
	return b.String()
}

// runTUI launches the interactive selector and prints a persistent summary
// after it exits (the alt-screen contents disappear on quit).
func runTUI(opts options) {
	p := tea.NewProgram(newModel(opts), tea.WithAltScreen())
	res, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "TUI failed to start (%v); falling back to text mode.\n", err)
		runPlain(opts)
		return
	}
	final, ok := res.(model)
	if !ok {
		return
	}
	printSummary(final)
}

func printSummary(m model) {
	if len(m.items) == 0 {
		fmt.Println("Nothing to clean. No build artifacts found.")
		return
	}
	if len(m.results) == 0 {
		fmt.Println("Aborted. Nothing was deleted.")
		return
	}
	var deleted, failed int
	for _, r := range m.results {
		if r.err == nil {
			deleted++
		} else {
			failed++
			fmt.Fprintf(os.Stderr, "failed: %s -> %v\n", r.path, r.err)
		}
	}
	fmt.Printf("Done: removed %d folder(s), freed ~%s.\n", deleted, humanSize(m.freed))
	if failed > 0 {
		fmt.Printf("%d folder(s) failed to delete.\n", failed)
	}
}
