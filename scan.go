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
			// "out" is generic. Treat it as an artifact (and stop descending)
			// only inside a real Next.js project; otherwise keep walking into
			// it so a .next nested deeper isn't silently missed.
			if isNextProject(filepath.Dir(path)) {
				add(path, kindOut)
				return true
			}
			return false
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
