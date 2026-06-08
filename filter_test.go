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
