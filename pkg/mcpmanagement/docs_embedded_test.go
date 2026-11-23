// adr: 20261123180000
package mcpmanagement

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
)

func TestEmbeddedDocsFillWhatTheSiteLacks(t *testing.T) {
	live := []*docsPage{{slug: "networking", title: "Networking (live)", lines: []string{"live body"}}}
	embedded := func() []*docsPage {
		return []*docsPage{
			{slug: "networking", title: "Networking (embedded)", lines: []string{"old body"}},
			{slug: "redis", title: "Managed Redis", lines: []string{"valkey"}, capabilities: []string{"managed-redis"}},
		}
	}
	pages := mergeDocsPages(live, embedded)
	if len(pages) != 2 || pages[0].title != "Networking (live)" || pages[1].slug != "redis" {
		t.Fatalf("merge = %+v; the live page must win and the missing page be added", pages)
	}
	if got := mergeDocsPages(live, nil); len(got) != 1 {
		t.Fatalf("no embedded source must add nothing, got %d pages", len(got))
	}
}

func TestPreviewPagesNeedAnAvailableCapability(t *testing.T) {
	published := &docsPage{slug: "networking"}
	redis := &docsPage{slug: "redis", capabilities: []string{"managed-redis"}}
	for _, tc := range []struct {
		name        string
		unavailable map[string]bool
		ok          bool
		want        bool
	}{
		{"available", map[string]bool{}, true, true},
		{"hidden for the account", map[string]bool{"managed-redis": true}, true, false},
		{"capabilities unknown", nil, false, false},
	} {
		visible := previewVisibility(tc.unavailable, tc.ok)
		if !visible(published) {
			t.Errorf("%s: a published page must always show", tc.name)
		}
		if visible(redis) != tc.want {
			t.Errorf("%s: redis visible = %v, want %v", tc.name, !tc.want, tc.want)
		}
	}
}

func TestSearchHidesPreviewPagesTheAccountLacks(t *testing.T) {
	corpus := buildDocsCorpus("https://gregale.dev", embeddedDocsPages(), time.Now())
	hidden := previewVisibility(map[string]bool{"managed-redis": true}, true)
	for _, hit := range corpus.search("managed redis valkey cache", 10, hidden) {
		if hit.Page == "redis" {
			t.Fatal("the redis preview page showed for an account without managed-redis")
		}
	}
}

// The questions agents asked in the 2026-11-23 eval (voting-app transcript)
// and failed to answer from the site: each must now find its page in the
// top five, with preview pages visible as for an account that has them.
func TestEmbeddedDocsAnswerTheEvalQuestions(t *testing.T) {
	corpus := buildDocsCorpus("https://gregale.dev", embeddedDocsPages(), time.Now())
	all := previewVisibility(map[string]bool{}, true)
	for _, tc := range []struct{ query, page string }{
		{"managed redis REDIS_URL format TLS password connect timeout wake parked cache", "redis"},
		{"gregale redis create bind app connect svc.gregale valkey", "redis"},
		{"background worker queue consumer no port", "workers"},
		{"postgres binding access migration read_write permission denied for schema public CREATE TABLE", "postgres"},
	} {
		var pages []string
		for _, hit := range corpus.search(tc.query, docsDefaultResults, all) {
			pages = append(pages, hit.Page)
		}
		if os.Getenv("DOCS_EVAL_VERBOSE") != "" {
			fmt.Printf("%q -> %v\n", tc.query, pages)
		}
		if !slices.Contains(pages, tc.page) {
			t.Errorf("%q: top results %v lack %s", tc.query, pages, tc.page)
		}
	}
}
