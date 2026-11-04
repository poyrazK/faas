package state_test

// ADR-20261022083601: crawler operators' published IP ranges. Both stores
// keep one row per crawler, store prefixes masked, sorted and de-duplicated,
// and refuse an invalid feed without touching the stored one.

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func crawlerRanges(crawler string, fetched time.Time, prefixes ...string) state.EdgeCrawlerRanges {
	r := state.EdgeCrawlerRanges{Crawler: crawler, SourceURL: "https://example.com/" + crawler + ".json", FetchedAt: fetched}
	for _, p := range prefixes {
		r.Prefixes = append(r.Prefixes, netip.MustParsePrefix(p))
	}
	return r
}

func runEdgeCrawlerRangesSteps(t *testing.T, s state.EdgeCrawlerRangesStore, ctx context.Context) {
	t.Helper()
	fetched := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	first := crawlerRanges("gptbot", fetched, "192.0.2.77/24", "2001:db8::/48", "192.0.2.0/24")
	first.SourceCreatedAt = "2026-10-21T00:00:00.000000"
	if err := s.UpsertEdgeCrawlerRanges(ctx, first); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.UpsertEdgeCrawlerRanges(ctx, crawlerRanges("bingbot", fetched, "157.55.39.0/24")); err != nil {
		t.Fatalf("upsert bingbot: %v", err)
	}
	got, err := s.ListEdgeCrawlerRanges(ctx)
	if err != nil || len(got) != 2 || got[0].Crawler != "bingbot" || got[1].Crawler != "gptbot" {
		t.Fatalf("list = %+v, %v; want bingbot then gptbot", got, err)
	}
	g := got[1]
	if len(g.Prefixes) != 2 || g.Prefixes[0].String() != "192.0.2.0/24" || g.Prefixes[1].String() != "2001:db8::/48" ||
		g.SourceCreatedAt != "2026-10-21T00:00:00.000000" || !g.FetchedAt.Equal(fetched) || g.SourceURL != first.SourceURL {
		t.Fatalf("gptbot = %+v, want masked, sorted, de-duplicated prefixes and the feed metadata", g)
	}

	// A newer fetch replaces the row.
	if err := s.UpsertEdgeCrawlerRanges(ctx, crawlerRanges("gptbot", fetched.Add(time.Hour), "198.51.100.0/24")); err != nil {
		t.Fatalf("replace: %v", err)
	}
	// Invalid feeds are refused and change nothing.
	for name, bad := range map[string]state.EdgeCrawlerRanges{
		"too broad":    crawlerRanges("gptbot", fetched, "0.0.0.0/0"),
		"broad ipv6":   crawlerRanges("gptbot", fetched, "2001::/16"),
		"no prefixes":  crawlerRanges("gptbot", fetched),
		"bad name":     crawlerRanges("GPT Bot", fetched, "192.0.2.0/24"),
		"plain http":   {Crawler: "gptbot", SourceURL: "http://example.com/x.json", Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, FetchedAt: fetched},
		"long created": {Crawler: "gptbot", SourceURL: "https://example.com/x.json", SourceCreatedAt: strings.Repeat("x", 65), Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, FetchedAt: fetched},
	} {
		if err := s.UpsertEdgeCrawlerRanges(ctx, bad); !errors.Is(err, state.ErrInvalidEdgeCrawlerRanges) {
			t.Errorf("%s: err = %v, want ErrInvalidEdgeCrawlerRanges", name, err)
		}
	}
	got, err = s.ListEdgeCrawlerRanges(ctx)
	if err != nil || len(got) != 2 || len(got[1].Prefixes) != 1 || got[1].Prefixes[0].String() != "198.51.100.0/24" ||
		!got[1].FetchedAt.Equal(fetched.Add(time.Hour)) {
		t.Fatalf("after replace and refusals = %+v, %v", got, err)
	}
}

func TestMemStore_EdgeCrawlerRanges(t *testing.T) {
	runEdgeCrawlerRangesSteps(t, state.NewMemStore(), context.Background())
}

func TestPgStore_EdgeCrawlerRanges(t *testing.T) {
	s, ctx := pgStore(t)
	runEdgeCrawlerRangesSteps(t, s, ctx)
}
