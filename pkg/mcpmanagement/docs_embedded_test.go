// adr: 20261123180000
package mcpmanagement

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmbeddedDocsFillWhatTheSiteLacks(t *testing.T) {
	live := []*docsPage{{slug: "networking", title: "Networking (live)", lines: []string{"live body"}}}
	embedded := func() []*docsPage {
		return []*docsPage{
			{slug: "networking", title: "Networking (embedded)", lines: []string{"old body"}},
			{slug: "redis", title: "Managed Redis", lines: []string{"valkey"}, capabilities: []string{"managed-redis"}},
			{slug: "retired", title: "Retired page", lines: []string{"gone from the site"}},
		}
	}
	pages := mergeDocsPages(live, embedded)
	if len(pages) != 2 || pages[0].title != "Networking (live)" || pages[1].slug != "redis" {
		t.Fatalf("merge = %+v; the live page must win, the missing preview page be added and a page the site retired stay out", pages)
	}
	corpus := buildDocsCorpus("https://docs.example", pages, time.Now())
	if got := corpus.pageURL(corpus.bySlug["redis"], ""); got != "" {
		t.Fatalf("preview page URL = %q; the site does not serve preview pages", got)
	}
	if got := corpus.pageURL(corpus.bySlug["networking"], "dns"); got != "https://docs.example/docs/networking#dns" {
		t.Fatalf("published page URL = %q", got)
	}
	for _, hit := range corpus.search("valkey", 3, nil) {
		if hit.Page == "redis" && (hit.URL != "" || !hit.Preview) {
			t.Fatalf("preview hit = %+v; want no URL and preview set", hit)
		}
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

// An unreachable docs site costs one fetch per cliDocsRetryAfter, not one per
// search, and a redirect off the docs host is refused, never cached as docs.
func TestLocalDocsBacksOffAndRefusesForeignRedirects(t *testing.T) {
	var foreign, fetches atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreign.Add(1)
		_, _ = w.Write([]byte("# Not Gregale\n"))
	}))
	defer other.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	defer site.Close()
	dir := t.TempDir()
	opts := LocalDocsOptions{CacheDir: dir, Site: site.URL}
	for range 3 {
		if _, err := SearchLocalDocs(t.Context(), "networking", 3, opts); err != nil {
			t.Fatalf("search must answer from the shipped index: %v", err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("site fetched %d times in a row; a failed fetch must back off", got)
	}
	if foreign.Load() != 0 {
		t.Fatal("followed a redirect off the docs host")
	}
	if _, err := os.Stat(filepath.Join(dir, cliDocsFullFile)); err == nil {
		t.Fatal("cached a failed fetch as docs")
	}
}
