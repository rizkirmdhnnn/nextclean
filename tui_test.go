package main

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	switch s {
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// send drives the model through a sequence of messages and returns the result.
func send(m model, msgs ...tea.Msg) model {
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(model)
	}
	return m
}

func sampleTargets() []target {
	return []target{
		{path: "/Users/me/myapp/.next", size: 100, kind: kindNext},
		{path: "/Users/me/myapp/out", size: 50, kind: kindOut},
		{path: "/Users/me/blog/.next", size: 300, kind: kindNext},
		{path: "/Users/me/blog/node_modules/.cache", size: 30, kind: kindCache},
	}
}

func TestScanTransitionsToSelecting(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: sampleTargets()})
	if m.state != stateSelecting {
		t.Fatalf("state = %v, want selecting", m.state)
	}
	if len(m.items) != 4 {
		t.Fatalf("items = %d, want 4", len(m.items))
	}
	if len(m.visible) != 4 {
		t.Fatalf("visible = %d, want 4", len(m.visible))
	}
	if c, _ := m.selection(); c != 0 {
		t.Errorf("selected = %d, want 0 (unchecked by default)", c)
	}
}

func TestEmptyScanGoesToDone(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: nil})
	if m.state != stateDone {
		t.Fatalf("state = %v, want done", m.state)
	}
}

func TestDefaultsToSortBySize(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: sampleTargets()})
	if !m.sortBySize {
		t.Fatal("sortBySize should default to true")
	}
	if m.items[0].size != 300 || m.items[3].size != 30 {
		t.Errorf("not sorted by size desc: first=%d last=%d", m.items[0].size, m.items[3].size)
	}
}

func TestSortToggleToPath(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: sampleTargets()})
	m = send(m, key("s")) // size -> path asc
	if m.sortBySize {
		t.Fatal("sortBySize should be false after toggle")
	}
	if m.items[0].path != "/Users/me/blog/.next" || m.items[3].path != "/Users/me/myapp/out" {
		t.Errorf("not sorted by path asc: first=%q last=%q", m.items[0].path, m.items[3].path)
	}
}

func TestToggleAndSelectAll(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: sampleTargets()})

	m = send(m, key(" ")) // toggle item under cursor
	if c, _ := m.selection(); c != 1 {
		t.Errorf("after space: selected = %d, want 1", c)
	}
	m = send(m, key("a")) // all
	if c, _ := m.selection(); c != 4 {
		t.Errorf("after 'a': selected = %d, want 4", c)
	}
	m = send(m, key("a")) // none
	if c, _ := m.selection(); c != 0 {
		t.Errorf("after second 'a': selected = %d, want 0", c)
	}
}

func TestTypeToggleFiltersList(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: sampleTargets()})
	m = send(m, key("2")) // hide out
	if len(m.visible) != 3 {
		t.Fatalf("after hiding out: visible = %d, want 3", len(m.visible))
	}
	m = send(m, key("3")) // hide cache
	if len(m.visible) != 2 {
		t.Fatalf("after hiding cache: visible = %d, want 2", len(m.visible))
	}
	m = send(m, key("1")) // hide .next
	if len(m.visible) != 0 {
		t.Fatalf("after hiding .next: visible = %d, want 0", len(m.visible))
	}
}

func TestSearchModeFilters(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: sampleTargets()})
	m = send(m, key("/"))
	if !m.searching {
		t.Fatal("expected searching mode after '/'")
	}
	m = send(m, key("b"), key("l"), key("o"), key("g"))
	if got := m.search.Value(); got != "blog" {
		t.Fatalf("search value = %q, want %q", got, "blog")
	}
	if len(m.visible) != 2 {
		t.Fatalf("visible = %d, want 2 (blog matches)", len(m.visible))
	}
	m = send(m, key("esc")) // clear + exit search
	if m.searching {
		t.Fatal("expected search mode off after esc")
	}
	if m.search.Value() != "" {
		t.Fatalf("expected cleared query, got %q", m.search.Value())
	}
	if len(m.visible) != 4 {
		t.Fatalf("visible = %d, want 4 after clear", len(m.visible))
	}
}

func TestSelectAllActsOnVisibleOnly(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: sampleTargets()})
	m = send(m, key("2"), key("3")) // hide out + cache -> only .next visible
	if len(m.visible) != 2 {
		t.Fatalf("visible = %d, want 2", len(m.visible))
	}
	m = send(m, key("a"))           // select all visible
	m = send(m, key("2"), key("3")) // re-show everything
	total := 0
	for _, it := range m.items {
		if it.checked {
			total++
		}
	}
	if total != 2 {
		t.Errorf("total checked = %d, want 2 (only visible were selected)", total)
	}
}

func TestEnterDeletesOnlyVisibleChecked(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: sampleTargets()})
	m = send(m, key("a")) // check all 4
	if c, _ := m.selection(); c != 4 {
		t.Fatalf("precondition: selected = %d, want 4", c)
	}
	m = send(m, key("2"), key("3")) // hide out + cache (stay checked but invisible)
	if len(m.visible) != 2 {
		t.Fatalf("visible = %d, want 2", len(m.visible))
	}
	m = send(m, key("enter"))
	if m.state != stateDeleting {
		t.Fatalf("state = %v, want deleting", m.state)
	}
	if len(m.queue) != 2 {
		t.Fatalf("queue = %d, want 2 (only visible-checked)", len(m.queue))
	}
	for _, it := range m.queue {
		if it.kind != kindNext {
			t.Errorf("queued a non-visible item (kind %v)", it.kind)
		}
	}
}

func TestEnterWithNoSelectionStays(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: sampleTargets()})
	m = send(m, key("enter"))
	if m.state != stateSelecting {
		t.Errorf("state = %v, want selecting (nothing selected)", m.state)
	}
}

func TestEnterStartsDeleting(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: sampleTargets()})
	m = send(m, key("a"), key("enter"))
	if m.state != stateDeleting {
		t.Fatalf("state = %v, want deleting", m.state)
	}
	if len(m.queue) != 4 {
		t.Errorf("queue = %d, want 4", len(m.queue))
	}
}

func TestDeleteProgressAdvancesToDone(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: sampleTargets()})
	m = send(m, key("a"), key("enter"))
	for i := 0; i < len(m.queue); i++ {
		it := m.queue[i]
		m = send(m, deleteDoneMsg{path: it.path, size: it.size})
	}
	if m.state != stateDone {
		t.Fatalf("state = %v, want done", m.state)
	}
	if m.freed != 480 {
		t.Errorf("freed = %d, want 480", m.freed)
	}
}

func TestViewsRenderWithoutPanic(t *testing.T) {
	m := newModel(options{})
	m.width, m.height = 80, 24
	m.applySort(sampleTargets())
	for _, st := range []appState{stateScanning, stateSelecting, stateDeleting, stateDone} {
		m.state = st
		if got := m.View(); got == "" {
			t.Errorf("state %v rendered empty view", st)
		}
	}
}

func TestQuitAborts(t *testing.T) {
	m := newModel(options{})
	m = send(m, scanDoneMsg{targets: sampleTargets()})
	m = send(m, key("q"))
	if !m.aborted {
		t.Error("expected aborted = true after 'q'")
	}
}

func TestDoneViewAllFailed(t *testing.T) {
	m := newModel(options{})
	m.width, m.height = 80, 24
	m = send(m, scanDoneMsg{targets: sampleTargets()})
	m = send(m, key("a"), key("enter"))

	errBoom := errors.New("permission denied")
	for i := 0; i < len(m.queue); i++ {
		it := m.queue[i]
		m = send(m, deleteDoneMsg{path: it.path, size: it.size, err: errBoom})
	}

	if m.state != stateDone {
		t.Fatalf("state = %v, want done", m.state)
	}
	if m.freed != 0 {
		t.Errorf("freed = %d, want 0 (all deletions failed)", m.freed)
	}
	if view := m.doneView(); !strings.Contains(view, "failed to delete") {
		t.Errorf("done view missing failure notice: %q", view)
	}
}
