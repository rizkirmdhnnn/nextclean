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

func TestOutWithoutNextIsTraversed(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, map[string]string{
		// "out" here is NOT a Next.js export dir (its parent has no Next.js
		// markers), so it must be traversed — and a real .next nested inside
		// it must still be found.
		"out/inner/.next/app.js": "x",
	})

	var scanned atomic.Int64
	got := collectTargets(options{root: root, recursive: true}, &scanned)
	if len(got) != 1 {
		t.Fatalf("found %d targets, want 1: %v", len(got), got)
	}
	if got[0].kind != kindNext || got[0].path != filepath.Join(root, "out/inner/.next") {
		t.Errorf("got %v, want kindNext at %s", got[0], filepath.Join(root, "out/inner/.next"))
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
