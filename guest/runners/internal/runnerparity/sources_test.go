package runnerparity

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// runnerMains returns every guest/runners/<runtime>/main.go keyed by runtime
// directory. Each runner hands requests to internal.Serve (ADR-999), so the
// structural pins below check the runners delegate and check the shared
// implementation once.
func runnerMains(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join(runnerRoot, "runners")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read runners dir: %v", err)
	}
	mains := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "internal" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, e.Name(), "main.go"))
		if err != nil {
			t.Errorf("read %s/main.go: %v", e.Name(), err)
			continue
		}
		mains[e.Name()] = string(body)
	}
	if len(mains) == 0 {
		t.Fatal("no runner dirs found under " + root + " — walk path is wrong")
	}
	return mains
}

// serveSource is the shared runner core every runner delegates to.
func serveSource(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(runnerRoot, "runners", "internal", "serve.go"))
	if err != nil {
		t.Fatalf("read internal/serve.go: %v", err)
	}
	return string(body)
}

// requireRunnersDelegate fails for any runner that serves requests itself
// instead of through internal.Serve, and returns the sorted runner names.
func requireRunnersDelegate(t *testing.T, mains map[string]string) []string {
	t.Helper()
	var names []string
	for name, src := range mains {
		names = append(names, name)
		if !strings.Contains(src, "internal.Serve(") {
			t.Errorf("%s: main.go does not delegate to internal.Serve — runner shape changed; update §4.9 and these pins", name)
		}
		if strings.Contains(src, "exec.Command") {
			t.Errorf("%s: main.go spawns the handler itself; the shared internal.Serve owns the subprocess path", name)
		}
	}
	sort.Strings(names)
	return names
}
