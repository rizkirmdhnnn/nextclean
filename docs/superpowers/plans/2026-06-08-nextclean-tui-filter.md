# nextclean v2 (TUI + Filter) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild `nextclean` with a concurrent scanner, an interactive Bubble Tea TUI, and a filter (path search + independent `.next`/`out`/`cache` type toggles), mirroring the sibling `nodeclean` architecture.

**Architecture:** Split the single `main.go` into focused files — `scan.go` (concurrent discovery + sizing, artifact kinds), `filter.go` (pure filter function), `tui.go` (Bubble Tea model/views), `main.go` (flags + plain-text fallback). Scan all three artifact kinds by default; narrow them in the TUI. Plain text drives `-dry`/`-y`/non-TTY.

**Tech Stack:** Go 1.26, `github.com/charmbracelet/bubbletea`, `.../bubbles` (spinner + textinput), `.../lipgloss`.

**Reference spec:** `docs/superpowers/specs/2026-06-08-nextclean-tui-filter-design.md`

---

## Setup: feature branch

- [ ] **Step 1: Create a working branch** (we are on `main`, the default branch)

```bash
cd /Users/rizkirmdhn/Documents/Code/02-Personal-Projects/04-Tools-CLI/nextclean
git checkout -b feat/tui-filter
```

---

## Task 1: Concurrent scanner with artifact kinds (`scan.go`)

Extract scanning out of `main.go` into a concurrent two-phase scanner that tags each artifact with its kind. `main.go` is trimmed to flags + the plain-text path; the TUI arrives in Task 3.

**Files:**
- Create: `scan.go`
- Create: `scan_test.go`
- Modify (full rewrite): `main.go`

- [ ] **Step 1: Write the failing scanner test**

Create `scan_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
)

// makeTree builds a directory tree under root. Keys ending in a filename get
// content written; intermediate directories are created as needed.
func makeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCollectTargetsRecursive(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, map[string]string{
		// A real Next.js project: next.config.js + .next + out.
		"projA/next.config.js":     "module.exports = {}",
		"projA/.next/build/app.js": "x",
		"projA/out/index.html":     "<html>",
		// A project with .next but no out.
		"projB/.next/server/page.js": "y",
		// node_modules/.cache should be found.
		"projC/node_modules/.cache/webpack/0.pack": "z",
		// A bare "out" with no Next.js markers must NOT match.
		"random/out/data.txt": "not next",
	})

	var scanned atomic.Int64
	got := collectTargets(options{root: root, recursive: true}, &scanned)

	type pk struct {
		path string
		kind artifactKind
	}
	var gotPK []pk
	for _, tg := range got {
		gotPK = append(gotPK, pk{tg.path, tg.kind})
	}
	sort.Slice(gotPK, func(i, j int) bool { return gotPK[i].path < gotPK[j].path })

	want := []pk{
		{filepath.Join(root, "projA/.next"), kindNext},
		{filepath.Join(root, "projA/out"), kindOut},
		{filepath.Join(root, "projB/.next"), kindNext},
		{filepath.Join(root, "projC/node_modules/.cache"), kindCache},
	}
	sort.Slice(want, func(i, j int) bool { return want[i].path < want[j].path })

	if len(gotPK) != len(want) {
		t.Fatalf("found %d targets %v, want %d %v", len(gotPK), gotPK, len(want), want)
	}
	for i := range want {
		if gotPK[i] != want[i] {
			t.Errorf("target[%d] = %v, want %v", i, gotPK[i], want[i])
		}
	}
	if scanned.Load() == 0 {
		t.Error("expected scanned counter to be incremented")
	}
}

func TestCollectTargetsNonRecursive(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, map[string]string{
		".next/build/app.js":         "a",
		"out/index.html":             "b",
		"node_modules/.cache/x/file": "c",
		"sub/.next/page.js":          "d", // must NOT be found without -r
	})

	got := collectTargets(options{root: root, recursive: false}, nil)
	if len(got) != 3 {
		t.Fatalf("found %d targets, want 3: %v", len(got), got)
	}

	var kinds []artifactKind
	for _, tg := range got {
		kinds = append(kinds, tg.kind)
		if tg.size == 0 {
			t.Errorf("expected non-zero size for %s", tg.path)
		}
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	want := []artifactKind{kindNext, kindOut, kindCache}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("kind[%d] = %v, want %v", i, kinds[i], want[i])
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, c := range cases {
		if got := humanSize(c.in); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./... 2>&1 | head -20`
Expected: compile failure — `undefined: collectTargets`, `undefined: options`, `undefined: artifactKind`, etc. (because the old `main.go` still owns these and uses different signatures/types).

- [ ] **Step 3: Create `scan.go`**

```go
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
)

// skipDirs are directories skipped during a full-disk (-all) scan to avoid slow
// or irrelevant paths.
var skipDirs = map[string]bool{
	".Trash":       true,
	"Library":      true,
	"System":       true,
	"Applications": true,
	"proc":         true,
	"sys":          true,
	"dev":          true,
	".git":         true,
	".docker":      true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	".cargo":       true,
	".rustup":      true,
	"go":           true,
}

// artifactKind identifies which kind of build artifact a target is.
type artifactKind int

const (
	kindNext  artifactKind = iota // .next
	kindOut                       // out (only inside a Next.js project)
	kindCache                     // node_modules/.cache
)

func (k artifactKind) String() string {
	switch k {
	case kindNext:
		return ".next"
	case kindOut:
		return "out"
	case kindCache:
		return "cache"
	default:
		return "?"
	}
}

type options struct {
	root      string
	recursive bool
	dry       bool
	all       bool
	yes       bool
}

type target struct {
	path string
	size int64
	kind artifactKind
}

// collectTargets finds build artifacts and computes their sizes concurrently.
// scanned, if non-nil, is incremented per directory inspected for live progress.
func collectTargets(opts options, scanned *atomic.Int64) []target {
	var found []target
	if !opts.recursive {
		found = directTargets(opts.root)
	} else {
		found = concurrentWalk(opts, scanned)
	}
	if len(found) == 0 {
		return nil
	}

	// Phase 2: compute sizes in parallel.
	workers := min(runtime.NumCPU(), len(found))
	var wg sync.WaitGroup
	ch := make(chan int, len(found))
	for i := range found {
		ch <- i
	}
	close(ch)
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range ch {
				found[i].size, _ = dirSize(found[i].path)
			}
		}()
	}
	wg.Wait()
	return found
}

// directTargets checks the known artifact paths directly under root. Used for a
// non-recursive run where the user has explicitly pointed at a project folder.
func directTargets(root string) []target {
	candidates := []struct {
		rel  string
		kind artifactKind
	}{
		{".next", kindNext},
		{"out", kindOut},
		{filepath.Join("node_modules", ".cache"), kindCache},
	}
	var found []target
	for _, c := range candidates {
		abs, err := filepath.Abs(filepath.Join(root, c.rel))
		if err != nil {
			continue
		}
		if st, err := os.Stat(abs); err == nil && st.IsDir() {
			found = append(found, target{path: abs, kind: c.kind})
		}
	}
	return found
}

// concurrentWalk discovers .next / out / node_modules-.cache directories using
// parallel workers. Top-level directories under root are distributed across
// goroutines for speed.
func concurrentWalk(opts options, scanned *atomic.Int64) []target {
	entries, err := os.ReadDir(opts.root)
	if err != nil {
		return nil
	}

	var mu sync.Mutex
	var results []target

	add := func(path string, kind artifactKind) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		mu.Lock()
		results = append(results, target{path: abs, kind: kind})
		mu.Unlock()
	}

	// inspect applies the artifact match rules to one directory and reports
	// whether the walk should stop descending into it (fs.SkipDir).
	inspect := func(path, name string, all bool) (skip bool) {
		if all && skipDirs[name] {
			return true
		}
		switch name {
		case ".next":
			add(path, kindNext)
			return true
		case "out":
			// "out" is generic; only match inside a real Next.js project.
			if isNextProject(filepath.Dir(path)) {
				add(path, kindOut)
			}
			return true
		case "node_modules":
			// Grab .cache but never descend into node_modules.
			cache := filepath.Join(path, ".cache")
			if st, err := os.Stat(cache); err == nil && st.IsDir() {
				add(cache, kindCache)
			}
			return true
		}
		return false
	}

	// Partition top-level dirs: artifacts are recorded directly; the rest are
	// queued for a deep walk.
	var walkDirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		path := filepath.Join(opts.root, name)
		if inspect(path, name, opts.all) {
			continue
		}
		walkDirs = append(walkDirs, path)
	}

	workers := min(runtime.NumCPU(), len(walkDirs))
	var wg sync.WaitGroup
	ch := make(chan string, len(walkDirs))
	for _, d := range walkDirs {
		ch <- d
	}
	close(ch)

	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for dir := range ch {
				_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
					if err != nil || !d.IsDir() {
						return nil
					}
					if scanned != nil {
						scanned.Add(1)
					}
					if inspect(path, d.Name(), opts.all) {
						return fs.SkipDir
					}
					return nil
				})
			}
		}()
	}
	wg.Wait()
	return results
}

// isNextProject reports whether dir looks like a Next.js project (has a
// next.config.* file or a .next subdirectory).
func isNextProject(dir string) bool {
	for _, name := range []string{"next.config.js", "next.config.mjs", "next.config.ts"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	if info, err := os.Stat(filepath.Join(dir, ".next")); err == nil && info.IsDir() {
		return true
	}
	return false
}

func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return size, err
}

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func exitf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format, a...)
	os.Exit(1)
}
```

- [ ] **Step 4: Rewrite `main.go` (plain-text only for now; TUI added in Task 3)**

Replace the entire contents of `main.go` with:

```go
// nextclean finds and removes Next.js build artifacts (.next, out,
// node_modules/.cache), freeing disk space. By default it scans the whole disk
// and opens an interactive selector (TUI) to choose what to delete.
//
// Usage:
//
//	go build -o nextclean .
//	./nextclean                 # scan entire disk, then pick artifacts in the TUI
//	./nextclean <path>          # clean artifacts in a single project folder
//	./nextclean -r <path>       # recursively scan every project under <path>
//	./nextclean -dry            # show what would be deleted without deleting
//	./nextclean -y              # delete everything found without prompting
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"sync/atomic"
	"time"
)

func main() {
	opts := parseFlags()

	info, err := os.Stat(opts.root)
	if err != nil {
		exitf("cannot access %q: %v\n", opts.root, err)
	}
	if !info.IsDir() {
		exitf("%q is not a directory\n", opts.root)
	}

	// The interactive TUI is wired up in a later task; for now everything uses
	// the plain-text path.
	runPlain(opts)
}

func parseFlags() options {
	var opts options
	var deprecatedCache bool
	flag.BoolVar(&opts.recursive, "r", false, "recursively scan subfolders for Next.js build artifacts")
	flag.BoolVar(&opts.all, "all", false, "scan entire disk for Next.js build artifacts")
	flag.BoolVar(&opts.dry, "dry", false, "print what would be deleted without deleting")
	flag.BoolVar(&opts.yes, "y", false, "delete everything found without prompting (no TUI)")
	flag.BoolVar(&deprecatedCache, "cache", false, "(deprecated) node_modules/.cache is always scanned now")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nextclean [flags] [path]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Without a path, scans the entire disk and opens an interactive selector.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		flag.PrintDefaults()
	}
	flag.Parse()
	_ = deprecatedCache // accepted for backward compatibility; cache is always scanned

	if flag.NArg() >= 1 {
		opts.root = flag.Arg(0)
	} else {
		opts.all = true
		opts.recursive = true
		opts.root = "/"
	}

	if opts.all {
		opts.recursive = true
		opts.root = "/"
	}

	return opts
}

// runPlain handles the non-interactive paths: -dry, -y, and non-TTY output.
func runPlain(opts options) {
	if opts.all {
		fmt.Println("Scanning entire disk for Next.js build artifacts...")
	}

	start := time.Now()
	var scanned atomic.Int64
	targets := collectTargets(opts, &scanned)
	if len(targets) == 0 {
		fmt.Println("Nothing to clean. No build artifacts found.")
		return
	}

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].path < targets[j].path
	})

	var totalSize int64
	fmt.Printf("\nFound %d build artifact(s) in %s:\n\n",
		len(targets), time.Since(start).Round(time.Millisecond))
	for _, t := range targets {
		totalSize += t.size
		fmt.Printf("  %s (%s)\n", t.path, humanSize(t.size))
	}
	fmt.Println()
	fmt.Printf("Total: %d folder(s), %s\n\n", len(targets), humanSize(totalSize))

	if opts.dry {
		fmt.Println("Dry run complete. No files were deleted.")
		return
	}

	if !opts.yes {
		fmt.Println("Non-interactive output detected. Re-run with -y to delete all, or -dry to preview.")
		return
	}

	var totalDeleted int
	var freed int64
	for _, t := range targets {
		delStart := time.Now()
		if err := os.RemoveAll(t.path); err != nil {
			fmt.Fprintf(os.Stderr, "  failed: %s -> %v\n", t.path, err)
			continue
		}
		totalDeleted++
		freed += t.size
		fmt.Printf("deleted %s (%s) in %s\n", t.path, humanSize(t.size),
			time.Since(delStart).Round(time.Millisecond))
	}

	fmt.Println("------")
	fmt.Printf("Done: removed %d folder(s), freed ~%s.\n", totalDeleted, humanSize(freed))
}
```

- [ ] **Step 5: Run tests and build to verify they pass**

Run: `go build ./... && go test ./...`
Expected: build succeeds; `ok` for the package (3 tests pass).

- [ ] **Step 6: Commit**

```bash
git add scan.go scan_test.go main.go
git commit -m "$(cat <<'EOF'
Add concurrent scanner with artifact kinds

Split scanning into scan.go: a two-phase concurrent walk that tags
each artifact as .next/out/cache and computes sizes in parallel.
main.go keeps the plain-text path; TUI follows.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Pure filter function (`filter.go`)

A standalone, TUI-free function that maps `(items, query, enabled-kinds)` → visible indices. Lives in its own file so it can be unit-tested in isolation. Also defines `listItem` (used by the TUI in Task 3).

**Files:**
- Create: `filter.go`
- Create: `filter_test.go`

- [ ] **Step 1: Write the failing filter test**

Create `filter_test.go`:

```go
package main

import (
	"reflect"
	"testing"
)

func allKinds() map[artifactKind]bool {
	return map[artifactKind]bool{kindNext: true, kindOut: true, kindCache: true}
}

func filterItems() []listItem {
	return []listItem{
		{target: target{path: "/Users/me/myapp/.next", size: 100, kind: kindNext}},
		{target: target{path: "/Users/me/myapp/out", size: 50, kind: kindOut}},
		{target: target{path: "/Users/me/blog/.next", size: 200, kind: kindNext}},
		{target: target{path: "/Users/me/blog/node_modules/.cache", size: 30, kind: kindCache}},
	}
}

func TestApplyFilterEmptyQueryAllKinds(t *testing.T) {
	got := applyFilter(filterItems(), "", allKinds())
	want := []int{0, 1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestApplyFilterByQuery(t *testing.T) {
	got := applyFilter(filterItems(), "blog", allKinds())
	want := []int{2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestApplyFilterCaseInsensitive(t *testing.T) {
	got := applyFilter(filterItems(), "MYAPP", allKinds())
	want := []int{0, 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestApplyFilterByKind(t *testing.T) {
	kinds := map[artifactKind]bool{kindNext: true, kindOut: false, kindCache: false}
	got := applyFilter(filterItems(), "", kinds)
	want := []int{0, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestApplyFilterQueryAndKind(t *testing.T) {
	kinds := map[artifactKind]bool{kindNext: true, kindOut: false, kindCache: true}
	got := applyFilter(filterItems(), "blog", kinds)
	want := []int{2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestApplyFilterAllKindsOff(t *testing.T) {
	kinds := map[artifactKind]bool{kindNext: false, kindOut: false, kindCache: false}
	got := applyFilter(filterItems(), "", kinds)
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./... 2>&1 | head -20`
Expected: compile failure — `undefined: listItem`, `undefined: applyFilter`.

- [ ] **Step 3: Create `filter.go`**

```go
package main

import "strings"

// listItem is a discovered artifact plus its TUI checkbox state.
type listItem struct {
	target
	checked bool
}

// applyFilter returns the indices of items visible under the given path query
// and the set of enabled kinds, preserving input order. An empty query matches
// every path; a kind is visible only when kinds[kind] is true.
func applyFilter(items []listItem, query string, kinds map[artifactKind]bool) []int {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []int
	for i, it := range items {
		if !kinds[it.kind] {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(it.path), q) {
			continue
		}
		out = append(out, i)
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: `ok` — all filter + scan tests pass.

- [ ] **Step 5: Commit**

```bash
git add filter.go filter_test.go
git commit -m "$(cat <<'EOF'
Add pure path+kind filter function

applyFilter maps (items, query, enabled-kinds) to visible indices,
isolated from the TUI for direct unit testing. Defines listItem.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Interactive TUI with filter (`tui.go`)

Add the Charm dependencies and build the Bubble Tea model: scanning spinner, interactive selector with search (`/`) and type toggles (`1`/`2`/`3`), the safe "delete only visible-checked" rule, and live deletion progress. Wire `main.go` to launch it.

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `tui.go`
- Create: `tui_test.go`
- Modify: `main.go` (dispatch to TUI)

- [ ] **Step 1: Add the Charm dependencies**

Overwrite `go.mod` with (module path unchanged, dependency block copied from the sibling `nodeclean`):

```
module github.com/rizkirmdhnnn/nextclean

go 1.26.1

require (
	github.com/charmbracelet/bubbles v1.0.0
	github.com/charmbracelet/bubbletea v1.3.10
	github.com/charmbracelet/lipgloss v1.1.0
)

require (
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/colorprofile v0.4.1 // indirect
	github.com/charmbracelet/x/ansi v0.11.6 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.15 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.9.0 // indirect
	github.com/clipperhouse/stringish v0.1.1 // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/lucasb-eyer/go-colorful v1.3.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.3.8 // indirect
)
```

Copy the matching checksums from `nodeclean` (identical dependency set, modules already in the local cache):

```bash
cp /Users/rizkirmdhn/Documents/Code/02-Personal-Projects/04-Tools-CLI/nodeclean/go.sum ./go.sum
go mod verify
```

Expected: `all modules verified`. (Fallback if `go.sum` ever drifts: `go mod tidy`.)

- [ ] **Step 2: Write the failing TUI test**

Create `tui_test.go`:

```go
package main

import (
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
	m = send(m, key("a")) // select all visible
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
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./... 2>&1 | head -20`
Expected: compile failure — `undefined: model`, `undefined: newModel`, `undefined: scanDoneMsg`, etc.

- [ ] **Step 4: Create `tui.go`**

```go
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
		m.search.Focus()
		return m, textinput.Blink
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
	b.WriteString("\n ")
	b.WriteString(okStyle.Render(fmt.Sprintf("Done. Removed %d folder(s), freed ~%s.", deleted, humanSize(m.freed))))
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
```

- [ ] **Step 5: Wire `main.go` to launch the TUI**

In `main.go`, add the `isTTY` helper and replace the plain-only dispatch. Change the `main()` body from:

```go
	// The interactive TUI is wired up in a later task; for now everything uses
	// the plain-text path.
	runPlain(opts)
}
```

to:

```go
	// Interactive TUI is the default. Fall back to plain text for the
	// non-interactive modes (-dry / -y) or when stdout is not a terminal.
	if opts.dry || opts.yes || !isTTY() {
		runPlain(opts)
		return
	}
	runTUI(opts)
}

// isTTY reports whether stdout is connected to an interactive terminal.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
```

- [ ] **Step 6: Run tests and build to verify they pass**

Run: `go build ./... && go test ./...`
Expected: build succeeds; `ok` — all scan, filter, and TUI tests pass.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum tui.go tui_test.go main.go
git commit -m "$(cat <<'EOF'
Add interactive TUI with search and type-toggle filter

Bubble Tea selector with scanning spinner, path search (/),
independent .next/out/cache toggles (1/2/3), and the safe
"delete only visible-checked" rule. main launches it by default,
falling back to plain text for -dry/-y/non-TTY.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Documentation (`README.md`, `CLAUDE.md`)

**Files:**
- Modify (full rewrite): `README.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Rewrite `README.md`**

Overwrite `README.md` with:

```markdown
# nextclean

A fast CLI tool to find and remove Next.js build artifacts (`.next`, `out`,
`node_modules/.cache`) from your disk — with an interactive TUI to pick exactly
which ones to delete and a filter to narrow them down.

## Installation

### Quick install (macOS/Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/rizkirmdhnnn/nextclean/main/install.sh | sh
```

### With Go

```bash
go install github.com/rizkirmdhnnn/nextclean@latest
```

### From source

```bash
git clone https://github.com/rizkirmdhnnn/nextclean.git
cd nextclean
go build -o nextclean .
sudo mv nextclean /usr/local/bin/
```

## Usage

```bash
# Scan entire disk, then pick artifacts in the interactive selector (default)
nextclean

# Preview what would be deleted (no TUI)
nextclean -dry

# Clean a specific project folder
nextclean ./my-app

# Recursively scan a directory
nextclean -r ~/projects

# Delete everything found without prompting (scripts/CI)
nextclean -y -r ~/projects
```

## Interactive selector (TUI)

By default, after scanning, nextclean opens a terminal UI so you can choose
exactly which build artifacts to remove, filter the list, and toggle artifact
types:

```
 Select build artifacts to delete  —  2/3 selected, 1.4 GB
 types: [1·.next on] [2·out on] [3·cache off]    search: myapp

   ▌ [x] /Users/me/myapp/.next                 (820.0 MB)
     [ ] /Users/me/myapp/out                   ( 12.0 MB)
     [x] /Users/me/myapp/sub/.next             (  4.0 MB)

 3 of 8 shown (filtered)
 / search • 1/2/3 types • ↑/↓ move • space toggle • a all • s sort • enter delete • q quit
```

- **↑/↓** (or `j`/`k`) — move the cursor
- **space** — toggle the artifact under the cursor
- **a** — select all / none (of what's currently shown)
- **s** — sort by size or by path
- **/** — search: type to filter the list by path
- **1 / 2 / 3** — show or hide `.next` / `out` / `cache` artifacts
- **enter** — delete the selected artifacts (live progress)
- **q** / **esc** — quit without deleting

Nothing is selected by default, so you never delete anything by accident. The
filter is safe: **enter only deletes items that are currently visible**, so you
can never remove something a filter has hidden. The TUI is skipped automatically
with `-dry`, `-y`, or when output is piped.

## Flags

| Flag   | Description                                          |
|--------|------------------------------------------------------|
| `-all` | Scan entire disk (default when no path is given)      |
| `-r`   | Recursively scan subfolders for build artifacts       |
| `-dry` | Print what would be deleted without deleting           |
| `-y`   | Delete everything found without prompting (no TUI)     |

> `-cache` is deprecated and ignored — `node_modules/.cache` is always scanned
> now. Hide it interactively with the `3` toggle.

## What gets deleted

- **`.next`** — Next.js build output
- **`out`** — Next.js static export output (only inside a Next.js project)
- **`node_modules/.cache`** — bundler/loader caches (safe to delete; regenerated)

## What gets skipped

During a full-disk scan, nextclean skips irrelevant or slow directories
(`.git`, `Library`, `System`, `Applications`, `vendor`, `go`, …). A bare `out/`
folder is only treated as an artifact when it sits inside a real Next.js project
(one with a `next.config.*` file or a `.next` folder).

## How it works

1. Walks the filesystem using **concurrent goroutines** for fast scanning.
2. Tags each find as `.next`, `out`, or `cache`, then sizes them in parallel.
3. Opens the TUI so you can filter, select, and delete — or runs headless with
   `-dry` / `-y`.
```

- [ ] **Step 2: Update `CLAUDE.md`**

Replace the `## Build & Run` and `## Architecture` sections. Change from:

```markdown
## Build & Run

```bash
go build -o nextclean .
./nextclean <path>            # clean a single project
./nextclean -r <path>         # recursively find and clean all Next.js projects
./nextclean -r -cache <path>  # also remove node_modules/.cache
./nextclean -dry <path>       # preview what would be deleted
```

## Architecture

Single-file CLI (`main.go`) with no external dependencies. The flow is:
1. `parseFlags()` — CLI arg parsing via `flag` stdlib
2. `collectTargets()` — finds build artifact directories (non-recursive: checks known paths; recursive: uses `filepath.WalkDir`, skipping `node_modules` except for `.cache`)
3. Main loop — calculates sizes, deletes (or dry-runs), prints summary
```

to:

```markdown
## Build & Run

```bash
go build -o nextclean .
./nextclean                   # scan whole disk, pick artifacts in the TUI (default)
./nextclean <path>            # clean a single project
./nextclean -r <path>         # recursively find and clean all Next.js projects
./nextclean -dry <path>       # preview what would be deleted (no TUI)
./nextclean -y -r <path>      # delete everything found, no prompt (no TUI)
go test ./...                 # run the test suite
```

## Architecture

Multi-file CLI built on Bubble Tea (`bubbletea` + `bubbles` + `lipgloss`).
`.next`, `out`, and `node_modules/.cache` are all scanned by default and tagged
with an `artifactKind`.

- `main.go` — flag parsing, `isTTY`, and `runPlain` (the `-dry`/`-y`/non-TTY path)
- `scan.go` — `options`, `target`, and the two-phase concurrent scan
  (`concurrentWalk` discovers, then sizes are computed in parallel). A bare
  `out/` only matches inside a Next.js project (`isNextProject`).
- `filter.go` — `listItem` plus the pure `applyFilter(items, query, kinds)`
- `tui.go` — Bubble Tea `model`/views: scanning → selecting → deleting → done,
  with path search (`/`), type toggles (`1`/`2`/`3`), and the safe rule that
  `enter` deletes only items currently visible under the filter.

Tests: `scan_test.go`, `filter_test.go`, `tui_test.go`.
```

- [ ] **Step 3: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "$(cat <<'EOF'
Document TUI, filter, and multi-file architecture

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: End-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Format, vet, and test**

Run:
```bash
gofmt -l . && go vet ./... && go test ./...
```
Expected: `gofmt -l .` prints nothing (all formatted); `go vet` is silent; tests print `ok`.

- [ ] **Step 2: Build the binary**

Run: `go build -o nextclean . && ./nextclean -h`
Expected: usage text listing `-all`, `-r`, `-dry`, `-y`, `-cache (deprecated)`.

- [ ] **Step 3: Smoke-test the plain path against a temp tree**

Run:
```bash
TMP=$(mktemp -d)
mkdir -p "$TMP/app/.next/x" "$TMP/app/out" "$TMP/app/node_modules/.cache/y" "$TMP/random/out"
echo 'module.exports={}' > "$TMP/app/next.config.js"
echo data > "$TMP/app/.next/x/f"
echo data > "$TMP/app/out/index.html"
echo data > "$TMP/app/node_modules/.cache/y/f"
echo data > "$TMP/random/out/f"
./nextclean -dry -r "$TMP"
```
Expected: lists exactly three artifacts — `app/.next`, `app/out`, `app/node_modules/.cache` — and NOT `random/out` (no Next.js markers). Ends with "Dry run complete."

- [ ] **Step 4: Confirm deletion works headlessly**

Run:
```bash
./nextclean -y -r "$TMP"
ls "$TMP/app" 2>/dev/null
rm -rf "$TMP"
```
Expected: deletion lines for the three artifacts; afterward `app` no longer contains `.next`, `out`, or `node_modules/.cache` (the `node_modules` dir itself remains, only `.cache` is gone).

- [ ] **Step 5: Final commit (if any formatting changed)**

```bash
git status --short
# If gofmt changed files in Step 1:
# git add -A && git commit -m "Apply gofmt" (add the Co-Authored-By trailer)
```

---

## Self-Review Notes

- **Spec coverage:** concurrent two-phase scan (Task 1), artifact kinds + `isNextProject` guard on `out` (Task 1), pure `applyFilter` (Task 2), TUI states + search + `1/2/3` toggles + safe delete rule (Task 3), `-cache` deprecated no-op + plain fallback (Tasks 1 & 3), README/CLAUDE docs (Task 4), tests for all three layers (Tasks 1–3). All spec sections map to a task.
- **Type consistency:** `options`, `target{path,size,kind}`, `artifactKind` (`kindNext/kindOut/kindCache`), and `listItem{target,checked}` are defined once (scan.go / filter.go) and reused unchanged. `collectTargets(options, *atomic.Int64)`, `applyFilter([]listItem,string,map[artifactKind]bool) []int`, `newModel(options) model` signatures match every call site.
- **No placeholders:** every code step shows complete, compilable content.
