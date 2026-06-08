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
	targets := collectTargets(opts, nil)
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
