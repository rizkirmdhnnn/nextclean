// nextclean is a small CLI tool to remove Next.js build artifacts
// (.next, out, node_modules/.cache) from a target folder.
//
// Usage:
//
//	go build -o nextclean .
//	./nextclean                        # scan entire disk (default)
//	./nextclean <path>                 # clean a single Next.js project folder
//	./nextclean -r <path>              # recursively clean every Next.js project under <path>
//	./nextclean -cache                 # also remove node_modules/.cache
//	./nextclean -dry                   # show what would be deleted without deleting
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Folders that count as Next.js build artifacts.
var buildDirs = []string{".next", "out"}

// Extra cache dir (inside node_modules) — opt-in via -cache.
const cacheDir = "node_modules/.cache"

// Directories to skip during full-disk scan to avoid slow/irrelevant paths.
var skipDirs = map[string]bool{
	".Trash":            true,
	"Library":           true,
	"System":            true,
	"Applications":      true,
	"proc":              true,
	"sys":               true,
	"dev":               true,
	".git":              true,
	".docker":           true,
	"vendor":            true,
	"dist":              true,
	"build":             true,
	".cargo":            true,
	".rustup":           true,
	"go":                true,
}

type options struct {
	root      string
	recursive bool
	cache     bool
	dry       bool
	all       bool
}

func main() {
	opts := parseFlags()

	if opts.all {
		fmt.Println("Scanning entire disk for Next.js build artifacts...")
	}

	info, err := os.Stat(opts.root)
	if err != nil {
		exitf("cannot access %q: %v\n", opts.root, err)
	}
	if !info.IsDir() {
		exitf("%q is not a directory\n", opts.root)
	}

	targets := collectTargets(opts)
	if len(targets) == 0 {
		fmt.Println("Nothing to clean. Build folders not found.")
		return
	}

	sort.Strings(targets)
	var totalSize int64
	var totalDeleted int

	for _, p := range targets {
		size, _ := dirSize(p)
		totalSize += size

		if opts.dry {
			fmt.Printf("[dry-run] would delete %s (%s)\n", p, humanSize(size))
			continue
		}

		start := time.Now()
		if err := os.RemoveAll(p); err != nil {
			fmt.Fprintf(os.Stderr, "  failed: %s -> %v\n", p, err)
			continue
		}
		totalDeleted++
		fmt.Printf("deleted %s (%s) in %s\n", p, humanSize(size), time.Since(start).Round(time.Millisecond))
	}

	fmt.Println("------")
	if opts.dry {
		fmt.Printf("Dry run: %d folder(s), %s total.\n", len(targets), humanSize(totalSize))
	} else {
		fmt.Printf("Done: removed %d folder(s), freed ~%s.\n", totalDeleted, humanSize(totalSize))
	}
}

func parseFlags() options {
	var opts options
	flag.BoolVar(&opts.recursive, "r", false, "recursively scan subfolders for Next.js projects")
	flag.BoolVar(&opts.all, "all", false, "scan entire disk for Next.js build artifacts")
	flag.BoolVar(&opts.cache, "cache", false, "also delete node_modules/.cache")
	flag.BoolVar(&opts.dry, "dry", false, "print what would be deleted without deleting")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nextclean [flags] [path]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Without a path, scans the entire disk (same as -all).")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		flag.PrintDefaults()
	}
	flag.Parse()

	// If a path is given, use it; otherwise default to full-disk scan.
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

// collectTargets returns absolute paths of build folders to remove.
func collectTargets(opts options) []string {
	var targets []string
	seen := map[string]struct{}{}

	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		if _, ok := seen[abs]; ok {
			return
		}
		if st, err := os.Stat(abs); err == nil && st.IsDir() {
			seen[abs] = struct{}{}
			targets = append(targets, abs)
		}
	}

	if !opts.recursive {
		for _, d := range buildDirs {
			add(filepath.Join(opts.root, d))
		}
		if opts.cache {
			add(filepath.Join(opts.root, cacheDir))
		}
		return targets
	}

	// Recursive mode — walk and collect any .next / out / node_modules/.cache.
	_ = filepath.WalkDir(opts.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		name := d.Name()

		// Skip irrelevant directories during full-disk scan.
		if opts.all && skipDirs[name] {
			return fs.SkipDir
		}

		// Skip descending into node_modules — except to find .cache if requested.
		if name == "node_modules" {
			if opts.cache {
				add(filepath.Join(path, ".cache"))
			}
			return fs.SkipDir
		}

		if name == ".next" {
			add(path)
			return fs.SkipDir
		}

		// "out" is too generic for full-disk scan — only match if it looks
		// like a Next.js project (has next.config.* or .next sibling).
		if name == "out" {
			parent := filepath.Dir(path)
			if isNextProject(parent) {
				add(path)
			}
			return fs.SkipDir
		}
		return nil
	})
	return targets
}

// isNextProject checks whether dir looks like a Next.js project
// by looking for next.config.* or a .next sibling folder.
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
