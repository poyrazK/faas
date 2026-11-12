package frameworkprofile

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// countingFS records which directories a walk opened.
type countingFS struct {
	fstest.MapFS
	opened map[string]bool
}

func (c *countingFS) ReadDir(name string) ([]fs.DirEntry, error) {
	c.opened[name] = true
	return c.MapFS.ReadDir(name)
}

// TestAnalyzeDoesNotWalkAnUnknownTree: `gregale doctor` run from a home
// directory walked every folder under it for health routes and loopback
// binds, and stalled on macOS-protected folders in ~/Library.
func TestAnalyzeDoesNotWalkAnUnknownTree(t *testing.T) {
	tree := fstest.MapFS{}
	for i := range sourceWalkMaxDirs + 100 {
		tree[fmt.Sprintf("Library/c%05d/x.js", i)] = &fstest.MapFile{Data: []byte(`app.get("/healthz")`)}
	}
	fsys := &countingFS{MapFS: tree, opened: map[string]bool{}}
	profile, err := Analyze(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Framework != "unknown" {
		t.Fatalf("framework = %q, want unknown", profile.Framework)
	}
	if len(fsys.opened) > sourceWalkMaxDirs+1 {
		t.Fatalf("Analyze opened %d directories of an unknown tree, want at most %d", len(fsys.opened), sourceWalkMaxDirs+1)
	}
}

func TestCollectSourceFilesIsBounded(t *testing.T) {
	t.Run("stops at the file limit", func(t *testing.T) {
		tree := fstest.MapFS{}
		for i := range 50 {
			tree[fmt.Sprintf("d%02d/f.go", i)] = &fstest.MapFile{}
		}
		fsys := &countingFS{MapFS: tree, opened: map[string]bool{}}
		got := collectSourceFiles(fsys, 5, isSourceCodeFile)
		if len(got) != 5 {
			t.Fatalf("got %d files, want 5", len(got))
		}
		if len(fsys.opened) > 6 {
			t.Fatalf("walk kept opening directories after the limit: %d", len(fsys.opened))
		}
	})
	t.Run("skips deep, hidden and dependency directories", func(t *testing.T) {
		deep := strings.Repeat("a/", sourceWalkMaxDepth+1) + "deep.go"
		tree := fstest.MapFS{
			"main.go":                {},
			"internal/x.go":          {},
			deep:                     {},
			".cache/hidden.go":       {},
			"node_modules/m/i.js":    {},
			"vendor/github.com/v.go": {},
		}
		got := collectSourceFiles(tree, 512, isSourceCodeFile)
		want := []string{"internal/x.go", "main.go"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
	t.Run("stops after the directory budget", func(t *testing.T) {
		tree := fstest.MapFS{}
		for i := range sourceWalkMaxDirs + 100 {
			tree[fmt.Sprintf("d%05d/README", i)] = &fstest.MapFile{}
		}
		fsys := &countingFS{MapFS: tree, opened: map[string]bool{}}
		collectSourceFiles(fsys, 512, isSourceCodeFile)
		if len(fsys.opened) > sourceWalkMaxDirs+1 {
			t.Fatalf("opened %d directories, want at most %d", len(fsys.opened), sourceWalkMaxDirs+1)
		}
	})
}

// TestAnalyzeStillFindsHealthRoutesInAProject keeps health inference working
// for a real project after the walk was bounded.
func TestAnalyzeStillFindsHealthRoutesInAProject(t *testing.T) {
	tree := fstest.MapFS{
		"package.json":      {Data: []byte(`{"name":"x","dependencies":{"express":"4"},"scripts":{"start":"node src/server.js"}}`)},
		"src/server.js":     {Data: []byte(`app.get("/health", ok)`)},
		"src/routes/api.js": {Data: []byte(`app.get("/users", list)`)},
	}
	profile, err := Analyze(tree)
	if err != nil {
		t.Fatal(err)
	}
	if profile.HealthPath != "/health" {
		t.Fatalf("health path = %q, want /health", profile.HealthPath)
	}
}
