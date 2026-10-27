package crawlerranges

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261022083601 — a good feed is stored masked and de-duplicated; a
// feed fetched within half the interval is skipped; a broken, oversized or
// failing feed keeps the previous ranges.
func TestRefresherKeepsPreviousRangesOnBadFeeds(t *testing.T) {
	var hits atomic.Int64
	bodies := map[string]string{
		"/good.json":  `{"creationTime":"2026-10-21T00:00:00.000000","prefixes":[{"ipv4Prefix":"192.0.2.7/24"},{"ipv4Prefix":"192.0.2.0/24"},{"ipv6Prefix":"2001:db8::/48"}]}`,
		"/broad.json": `{"prefixes":[{"ipv4Prefix":"0.0.0.0/0"}]}`,
		"/junk.json":  `{"prefixes":[{"ipv4Prefix":"not-a-prefix"}]}`,
		"/huge.json":  `{"prefixes":[],"pad":"` + strings.Repeat("x", api.EdgeCrawlerRangesMaxBodyBytes) + `"}`,
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("User-Agent") != UserAgent {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		body, ok := bodies[r.URL.Path]
		if !ok {
			http.Error(w, "gone", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	store := state.NewMemStore()
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	outcomes := map[string]string{}
	refresher := &Refresher{
		Store: store, Client: srv.Client(), Every: 12 * time.Hour,
		Now:     func() time.Time { return now },
		Observe: func(crawler, outcome string) { outcomes[crawler] = outcome },
	}
	feed := func(name, path string) api.EdgeCrawlerRangeFeed {
		return api.EdgeCrawlerRangeFeed{Crawler: name, URL: srv.URL + path}
	}
	refresher.Feeds = []api.EdgeCrawlerRangeFeed{feed("gptbot", "/good.json"), feed("bingbot", "/missing.json")}
	refresher.RefreshAll(context.Background())
	if outcomes["gptbot"] != OutcomeOK || outcomes["bingbot"] != OutcomeFetchError {
		t.Fatalf("outcomes = %v", outcomes)
	}
	got, err := store.ListEdgeCrawlerRanges(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("stored = %+v, %v", got, err)
	}
	if len(got[0].Prefixes) != 2 || got[0].Prefixes[0].String() != "192.0.2.0/24" || got[0].Prefixes[1].String() != "2001:db8::/48" ||
		got[0].SourceCreatedAt != "2026-10-21T00:00:00.000000" || !got[0].FetchedAt.Equal(now) {
		t.Fatalf("stored gptbot = %+v, want masked, de-duplicated prefixes", got[0])
	}

	// Within half the interval the stored feed is not fetched again.
	before := hits.Load()
	now = now.Add(5 * time.Hour)
	refresher.Feeds = []api.EdgeCrawlerRangeFeed{feed("gptbot", "/good.json")}
	refresher.RefreshAll(context.Background())
	if outcomes["gptbot"] != OutcomeFresh || hits.Load() != before {
		t.Fatalf("fresh feed: outcome %q, %d new fetches", outcomes["gptbot"], hits.Load()-before)
	}

	// Later, broken feeds are refused whole and the stored ranges stay.
	now = now.Add(2 * time.Hour)
	for _, path := range []string{"/broad.json", "/junk.json", "/huge.json"} {
		refresher.Feeds = []api.EdgeCrawlerRangeFeed{feed("gptbot", path)}
		refresher.RefreshAll(context.Background())
		if outcomes["gptbot"] == OutcomeOK {
			t.Fatalf("%s accepted", path)
		}
		if got, _ := store.ListEdgeCrawlerRanges(context.Background()); len(got) != 1 || len(got[0].Prefixes) != 2 {
			t.Fatalf("%s replaced the stored ranges: %+v", path, got)
		}
	}
}

// adr: 20261022083601 — the default client follows only https redirects.
func TestRefresherClientRefusesPlainRedirects(t *testing.T) {
	check := (&Refresher{}).client().CheckRedirect
	to := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	if err := check(to("https://www.perplexity.ai/perplexitybot.json"), []*http.Request{{}}); err != nil {
		t.Fatalf("https redirect refused: %v", err)
	}
	if err := check(to("http://example.com/feed.json"), []*http.Request{{}}); err == nil {
		t.Fatal("plain http redirect followed")
	}
	if err := check(to("https://example.com/feed.json"), make([]*http.Request, 3)); err == nil {
		t.Fatal("fourth redirect followed")
	}
}
