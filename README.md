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
(`.git`, `Library`, `System`, `Applications`, `vendor`, `go`, …). When you point
it at a specific directory with `-r`, those skips don't apply — everything under
the path you gave is scanned. A bare `out/` folder is only treated as an artifact
when it sits inside a real Next.js project (one with a `next.config.*` file or a
`.next` folder).

## How it works

1. Walks the filesystem using **concurrent goroutines** for fast scanning.
2. Tags each find as `.next`, `out`, or `cache`, then sizes them in parallel.
3. Opens the TUI so you can filter, select, and delete — or runs headless with
   `-dry` / `-y`.
