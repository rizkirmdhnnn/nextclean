# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

nextclean is a multi-file Go CLI tool that removes Next.js build artifacts (`.next`, `out`, `node_modules/.cache`) from a target directory, with optional recursive scanning.

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
