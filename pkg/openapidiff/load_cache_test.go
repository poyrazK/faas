package openapidiff

import "testing"

// TestLoadParsesOnceAndHandsOutOwnMaps pins the Load cache: the embedded
// spec is parsed once, and a caller that adds or replaces paths, methods or
// components does not change what the next caller loads.
func TestLoadParsesOnceAndHandsOutOwnMaps(t *testing.T) {
	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("Load returned the same Spec twice")
	}
	if len(first.Paths) == 0 || len(first.Components) == 0 {
		t.Fatalf("embedded spec loaded %d paths and %d components", len(first.Paths), len(first.Components))
	}
	var path string
	for p, pi := range first.Paths {
		if pi != nil && len(pi.Methods) > 0 {
			path = p
			break
		}
	}
	if path == "" {
		t.Fatal("no path with methods in the embedded spec")
	}
	if first.Paths[path] == second.Paths[path] {
		t.Fatalf("path item %s is shared between Load calls", path)
	}
	if first.OpenAPIVersion() != second.OpenAPIVersion() || len(first.Raw) != len(second.Raw) {
		t.Fatal("copies differ from the embedded spec")
	}

	paths, components, methods := len(first.Paths), len(first.Components), len(first.Paths[path].Methods)
	first.Paths["/load-cache-test"] = &PathItem{}
	first.Paths[path].Methods["load-cache-test"] = &Operation{}
	first.Components["LoadCacheTest"] = &Schema{}

	third, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Paths) != paths || len(third.Components) != components || len(third.Paths[path].Methods) != methods {
		t.Fatal("a caller's edits leaked into the next Load")
	}
	if len(second.Paths) != paths || len(second.Paths[path].Methods) != methods {
		t.Fatal("a caller's edits leaked into another caller's Spec")
	}
}
