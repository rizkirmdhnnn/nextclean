# nextclean v2 — Concurrent Scan + TUI + Filter

**Date:** 2026-06-08
**Status:** Approved (design)

## Summary

Bring `nextclean` up to the architecture of its sibling tool `nodeclean`: a
concurrent filesystem scanner and an interactive Bubble Tea TUI for picking
which build artifacts to delete. Add a **filter** capability that `nodeclean`
lacks — live path search plus independent artifact-type toggles — which is
especially valuable because `nextclean` deals with three artifact kinds
(`.next`, `out`, `node_modules/.cache`) rather than one.

Today `nextclean` is a single 275-line `main.go` with no dependencies: a
sequential `filepath.WalkDir` scan and a plain-text `[y/N]` prompt. After this
change it matches `nodeclean`'s structure and feature set, plus the filter.

## Goals

- Concurrent two-phase scan (discover, then size) with a live progress counter.
- Interactive TUI as the default, with the same states as `nodeclean`:
  `scanning → selecting → deleting → done`.
- **Filter**: type-to-search by path substring **and** independent visibility
  toggles for each artifact kind.
- Scan all three artifact kinds by default (`.next`, `out`,
  `node_modules/.cache`); narrow them in the TUI rather than via CLI flags.
- Preserve `nextclean`'s existing, smarter match rules (notably the
  `isNextProject` guard on the generic `out` directory).
- Plain-text fallback for `-dry`, `-y`, and non-TTY output.
- Tests covering scan, the pure filter function, and TUI behavior.

## Non-Goals

- No extra columns/metadata beyond `nodeclean` (no artifact-type tag column, no
  last-modified age, no project grouping). The artifact kind is already visible
  as the final path segment (`…/.next`, `…/out`, `…/node_modules/.cache`).
- No min-size filter (search + type toggles only).
- No new top-level dependency beyond the three `nodeclean` already uses.

## Architecture

Mirror `nodeclean`'s multi-file split. Dependencies: `github.com/charmbracelet/bubbletea`, `.../bubbles`, `.../lipgloss`. `bubbles` already provides both `spinner` and `textinput`, so no additional top-level module is required.

| File | Responsibility |
|---|---|
| `main.go` | flag parsing, `isTTY`, `runPlain` (the `-dry`/`-y`/non-TTY path), dispatch to TUI |
| `scan.go` | `options`, `target` (with a `kind` field), concurrent `collectTargets`/`concurrentWalk`, `isNextProject`, `dirSize`, `humanSize`, `skipDirs` |
| `filter.go` | `artifactKind` enum + pure `applyFilter(items, query, kinds) → []int` (visible indices) |
| `tui.go` | Bubble Tea `model`, search/toggle state, views |
| `scan_test.go` | recursive/non-recursive discovery, `humanSize` |
| `filter_test.go` | filter by query, by kind-set, and combined |
| `tui_test.go` | state transitions, search mode, type toggles, select-all-on-visible, safe delete rule |

### Data model

```go
type artifactKind int

const (
    kindNext  artifactKind = iota // .next
    kindOut                       // out (only inside a Next.js project)
    kindCache                     // node_modules/.cache
)

type target struct {
    path string
    size int64
    kind artifactKind
}
```

### Scanning (the nodeclean upgrade)

Replace the sequential walk with `nodeclean`'s two-phase concurrent model:

1. **Discover** — `concurrentWalk` distributes the top-level directories under
   `root` across `min(NumCPU, n)` workers. Each worker runs `filepath.WalkDir`
   and applies the match rules below. A shared, mutex-guarded slice collects
   results; an `atomic.Int64` "directories checked" counter is bumped per
   directory so the scanning spinner can show live progress.
2. **Size** — a second worker pool computes `dirSize` for each discovered
   artifact in parallel.

Match rules (ported from current `nextclean`, preserved exactly):

- `.next` → record as `kindNext`, then `fs.SkipDir` (don't descend).
- `out` → record as `kindOut` **only if** `isNextProject(parent)` (parent has a
  `next.config.{js,mjs,ts}` or a sibling `.next/`), then `fs.SkipDir`. This
  guard prevents matching unrelated `out/` directories during a full-disk scan.
- `node_modules` → record its `.cache` subdirectory as `kindCache` if present,
  then `fs.SkipDir` (never descend into `node_modules`).
- `skipDirs` entries (`.git`, `Library`, `System`, `node`, `vendor`, …) →
  `fs.SkipDir` during full-disk (`-all`) scans.

Non-recursive mode checks the known paths directly under `root`
(`.next`, `out`, `node_modules/.cache`) without walking.

## Filter

The `target.kind` field drives both the type toggles and the (kept-simple)
display. The selecting view adds two filter mechanisms:

- **Search** — press `/` to enter search mode; a `textinput` filters the list
  live by case-insensitive path substring. `enter` applies the query and
  returns to nav mode; `esc` clears the query and returns to nav mode.
- **Type toggles** — keys `1`/`2`/`3` independently toggle the visibility of
  `.next` / `out` / `cache`. Any combination is expressible (e.g. show `.next`
  and `out` but hide `cache`). Toggling all three off shows an empty list with
  a hint to re-enable a type.

Pure filter function (lives in `filter.go`, unit-tested without a TUI):

```go
// applyFilter returns the indices of items visible under the current query and
// the set of enabled kinds, preserving the input order.
func applyFilter(items []listItem, query string, kinds map[artifactKind]bool) []int
```

### Filter ↔ selection semantics — "what you see is what you delete"

This is the safety-critical rule (Decision A, confirmed):

- Checkbox state **persists** per item regardless of visibility.
- `a` (select all / none) acts on the **currently visible** set only.
- **`enter` deletes only checked items that currently pass the filter.** An item
  that is checked but hidden by the active filter is **not** deleted. You can
  never delete something you cannot see.
- The header's "selected count / size" reflects the **visible** selection.

## TUI

States, alt-screen usage, and the plain-text summary-on-exit behavior are
unchanged from `nodeclean`. The selecting view layout:

```
 Select build artifacts to delete  —  2/8 selected, 1.4 GB
 types: [1·.next on] [2·out on] [3·cache off]    search: myapp▏

   ▌ [x] /Users/me/myapp/.next                 (820.0 MB)
     [ ] /Users/me/myapp/out                   ( 12.0 MB)
     [x] /Users/me/blog/.next                  (612.0 MB)

 3 of 8 shown (filtered)
 / search • 1/2/3 types • ↑/↓ move • space toggle • a all • s sort • enter delete • q quit
```

- The `types:` line shows each kind's on/off state.
- The `search:` segment appears only in search mode or when a query is active.
- The `N of M shown (filtered)` line appears only when a filter is active.
- Cursor and scroll offset are computed against the **filtered** list and
  re-clamped whenever the filter changes.

### Keybindings

| Key | Action |
|---|---|
| `/` | enter search mode (type to filter by path) |
| `1` / `2` / `3` | toggle `.next` / `out` / `cache` visibility |
| `↑`/`↓`, `j`/`k` | move cursor (within filtered list) |
| `space` | toggle the item under the cursor |
| `a` | select all / none (visible set) |
| `s` | toggle sort (size desc / path asc) |
| `enter` | delete the visible-checked selection |
| `q` / `esc` | quit without deleting |

In **search mode**, printable keys edit the query; `enter`/`esc` leave search
mode (apply / clear respectively); the type-toggle and nav keys are inactive
until search mode is exited.

## CLI

Flags retained: `-r` (recursive), `-all` (full-disk; default when no path),
`-dry` (preview, no TUI), `-y` (delete all found, no TUI). Default invocation
with no path scans the whole disk and opens the TUI, matching `nodeclean`.

`-cache` becomes a **deprecated no-op**: cache is now always scanned, so the
flag is accepted (to avoid breaking existing invocations) but ignored. A one
-line deprecation note is acceptable but not required.

`runPlain` (non-interactive) previews/deletes **all three** kinds. There is no
`-no-cache`; interactive users hide cache via the `3` toggle.

## Error Handling

- Unreadable directories during the walk are skipped silently (per-entry `err`
  ignored), as in both current tools.
- Per-artifact delete failures are recorded and reported in the summary; the
  run continues to the next item.
- If the TUI fails to start, fall back to `runPlain` (as `nodeclean` does).

## Testing

- **scan_test.go** — recursive discovery tags kinds correctly; `out` is matched
  only inside a Next.js project and ignored otherwise; `.cache` under
  `node_modules` is found; non-recursive mode checks only direct paths;
  `humanSize` table test.
- **filter_test.go** — `applyFilter` by query substring (case-insensitive); by
  kind-set (each toggle combination); combined query + kinds; empty query and
  all-kinds-enabled returns every index in order.
- **tui_test.go** — scan→selecting transition with kinds; entering/leaving
  search mode; `1/2/3` toggle visibility and re-clamp the cursor; `a` selects
  only the visible set; `enter` queues only visible-checked items (the safe
  rule); delete progress advances to `done`; views render without panic.

## Rollout

Update `README.md` (TUI usage, filter keys, flag table, "what gets deleted")
and bump `go.mod` to require the three Charm dependencies. No release-workflow
or `install.sh` changes needed.
