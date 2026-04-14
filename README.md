# nextclean

A fast CLI tool to find and remove Next.js build artifacts (`.next`, `out`, `node_modules/.cache`) from your disk.

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
# Scan entire disk (default — no arguments needed)
nextclean

# Preview what would be deleted
nextclean -dry

# Clean a specific project folder
nextclean ./my-next-app

# Recursively scan a directory
nextclean -r ~/projects

# Also remove node_modules/.cache
nextclean -cache

# Combine flags
nextclean -dry -cache -r ~/projects
```

## Flags

| Flag     | Description                                      |
|----------|--------------------------------------------------|
| `-all`   | Scan entire disk (default when no path is given)  |
| `-r`     | Recursively scan subfolders for Next.js projects  |
| `-cache` | Also delete `node_modules/.cache`                 |
| `-dry`   | Print what would be deleted without deleting       |

## What gets deleted

- **`.next`** — Next.js build output directory
- **`out`** — Next.js static export directory (only when the parent folder is a Next.js project, to avoid false positives)
- **`node_modules/.cache`** — Bundler/transpiler cache (opt-in with `-cache`)

## How it works

When scanning the entire disk, nextclean:

1. Walks the filesystem starting from `/`
2. Skips irrelevant directories (`.git`, `Library`, `System`, `node_modules`, etc.) for speed
3. Collects all `.next` directories found
4. Only collects `out` directories when the parent has a `next.config.*` file or a `.next` sibling — avoiding false positives from non-Next.js projects
5. Asks for confirmation before deleting
6. Reports total size freed after cleanup

## Uninstall

```bash
rm $(which nextclean)
```

## License

MIT
