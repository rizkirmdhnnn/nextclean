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
