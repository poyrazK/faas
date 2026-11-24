package docsindex

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/productcap"
)

func build(t *testing.T) []byte {
	t.Helper()
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	gz, err := Build(filepath.Join("..", "..", "docs"), catalog)
	if err != nil {
		t.Fatal(err)
	}
	return gz
}

// TestIndexIsCurrent keeps the shipped index in step with docs/. Run
// UPDATE_DOCS_INDEX=1 go test ./pkg/docsindex to rewrite it.
func TestIndexIsCurrent(t *testing.T) {
	gz := build(t)
	if os.Getenv("UPDATE_DOCS_INDEX") == "1" {
		if err := os.WriteFile("index.json.gz", gz, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !bytes.Equal(gz, indexGz) {
		t.Fatal("pkg/docsindex/index.json.gz is stale; run UPDATE_DOCS_INDEX=1 go test ./pkg/docsindex")
	}
}

func TestIndexHoldsPublishedAndUnlockablePreviewPagesOnly(t *testing.T) {
	pages, err := decode(build(t))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Page{}
	for _, p := range pages {
		by[p.Slug] = p
	}
	if p, ok := by["networking"]; !ok || p.Preview() || p.Title == "" || p.Summary == "" {
		t.Errorf("published page networking: %+v", p)
	}
	if p := by["redis"]; !slices.Contains(p.Capabilities, "managed-redis") {
		t.Errorf("preview page redis should be unlocked by managed-redis, got %v", p.Capabilities)
	}
	if p := by["workers"]; !slices.Contains(p.Capabilities, "worker-pools") {
		t.Errorf("preview page workers should be unlocked by worker-pools, got %v", p.Capabilities)
	}
	for _, internal := range []string{"log-search", "incidents", "api-hosting", "environment-branches"} {
		if _, ok := by[internal]; ok {
			t.Errorf("internal or dark page %s must not be in the index", internal)
		}
	}
	if _, ok := by[""]; ok {
		t.Error("the empty route must not be a page")
	}
	for _, p := range pages {
		if strings.HasPrefix(p.Body, "# ") {
			t.Errorf("%s: body keeps its title", p.Slug)
		}
	}
}

func TestSummaryIsTheFirstProseSentence(t *testing.T) {
	got := summary("> Preview. Only for some.\n\nGregale runs [Valkey](https://valkey.io) caches. More text.\n\n## Next")
	if got != "Gregale runs Valkey caches." {
		t.Errorf("summary = %q", got)
	}
}
