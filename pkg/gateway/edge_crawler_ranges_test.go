package gateway

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const gptbotUA = "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.1; +https://openai.com/gptbot)"

func crawlerRangesAt(now time.Time, rows ...state.EdgeCrawlerRanges) *CrawlerRanges {
	c := NewCrawlerRanges()
	c.now = func() time.Time { return now }
	c.Replace(rows)
	return c
}

func rangesRow(crawler string, fetched time.Time, prefixes ...string) state.EdgeCrawlerRanges {
	r := state.EdgeCrawlerRanges{Crawler: crawler, FetchedAt: fetched}
	for _, p := range prefixes {
		r.Prefixes = append(r.Prefixes, netip.MustParsePrefix(p))
	}
	return r
}

// adr: 20261022083601 — membership is per crawler, IPv4-mapped addresses
// match IPv4 ranges, and ranges older than the stale cutoff stop verifying.
func TestCrawlerRangesContains(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	c := crawlerRangesAt(now,
		rangesRow("gptbot", now.Add(-time.Hour), "192.0.2.0/24", "2001:db8::/48"),
		rangesRow("perplexitybot", now.Add(-api.EdgeCrawlerRangesStaleAfter-time.Minute), "198.51.100.0/24"))
	for _, tc := range []struct {
		crawler, ip string
		want        bool
	}{
		{"gptbot", "192.0.2.10", true},
		{"gptbot", "::ffff:192.0.2.10", true},
		{"gptbot", "2001:db8::1", true},
		{"gptbot", "203.0.113.1", false},
		{"claudebot", "192.0.2.10", false},
		{"perplexitybot", "198.51.100.7", false}, // stale
	} {
		if got := c.Contains(tc.crawler, net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("Contains(%s, %s) = %v, want %v", tc.crawler, tc.ip, got, tc.want)
		}
	}
	var none *CrawlerRanges
	if none.Contains("gptbot", net.ParseIP("192.0.2.10")) || NewCrawlerRanges().Contains("gptbot", net.ParseIP("192.0.2.10")) {
		t.Fatal("an empty snapshot verified a crawler")
	}
}

// adr: 20261022083601 — a search crawler inside its published ranges is
// verified without a DNS lookup; outside them the DNS check still runs.
// An AI crawler verifies only from its ranges and never looks up DNS.
func TestBotVerifierUsesPublishedRanges(t *testing.T) {
	now := time.Now()
	ranges := crawlerRangesAt(now,
		rangesRow("googlebot", now, "66.249.64.0/19"),
		rangesRow("gptbot", now, "192.0.2.0/24"))
	resolver := googlebotResolver()
	v := NewBotVerifier(resolver).WithCrawlerRanges(ranges)
	ctx := context.Background()

	if got := v.VerifiedBot(ctx, net.ParseIP("66.249.66.1"), googlebotUA); got != "googlebot" || resolver.ptrHits.Load() != 0 {
		t.Fatalf("in-range googlebot = %q after %d DNS lookups, want googlebot with none", got, resolver.ptrHits.Load())
	}
	resolver.ptr["203.0.113.9"] = []string{"crawl-x.googlebot.com."}
	resolver.fwd["crawl-x.googlebot.com"] = []string{"203.0.113.9"}
	if got := v.VerifiedBot(ctx, net.ParseIP("203.0.113.9"), googlebotUA); got != "googlebot" || resolver.ptrHits.Load() != 1 {
		t.Fatalf("out-of-range googlebot = %q after %d DNS lookups, want the DNS fallback", got, resolver.ptrHits.Load())
	}

	hits := resolver.ptrHits.Load()
	for _, tc := range []struct {
		ip, ua, want string
	}{
		{"192.0.2.44", gptbotUA, "gptbot"},
		{"203.0.113.9", gptbotUA, ""},                                 // impersonator
		{"192.0.2.44", "Mozilla/5.0 (compatible; ClaudeBot/1.0)", ""}, // no published ranges
		{"192.0.2.44", "Mozilla/5.0 Chrome/129.0", ""},
	} {
		if got := v.VerifiedAICrawler(net.ParseIP(tc.ip), tc.ua); got != tc.want {
			t.Errorf("VerifiedAICrawler(%s, %q) = %q, want %q", tc.ip, tc.ua, got, tc.want)
		}
	}
	if resolver.ptrHits.Load() != hits {
		t.Fatal("AI crawler verification looked up DNS")
	}
	var nilVerifier *BotVerifier
	if nilVerifier.VerifiedAICrawler(net.ParseIP("192.0.2.44"), gptbotUA) != "" {
		t.Fatal("nil verifier verified a crawler")
	}
}

type flakyRangesStore struct {
	mu   sync.Mutex
	rows []state.EdgeCrawlerRanges
	err  error
}

func (s *flakyRangesStore) ListEdgeCrawlerRanges(context.Context) ([]state.EdgeCrawlerRanges, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows, s.err
}

// adr: 20261022083601 — the loader installs the stored ranges and keeps
// the previous snapshot when a reread fails.
func TestCrawlerRangesRunKeepsSnapshotOnError(t *testing.T) {
	now := time.Now()
	store := &flakyRangesStore{rows: []state.EdgeCrawlerRanges{rangesRow("gptbot", now, "192.0.2.0/24")}}
	c := NewCrawlerRanges()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx, store, time.Millisecond, nil); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for !c.Contains("gptbot", net.ParseIP("192.0.2.1")) {
		if time.Now().After(deadline) {
			t.Fatal("ranges never loaded")
		}
		time.Sleep(time.Millisecond)
	}
	store.mu.Lock()
	store.err = errors.New("database unavailable")
	store.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	if !c.Contains("gptbot", net.ParseIP("192.0.2.1")) {
		t.Fatal("a failed reread dropped the snapshot")
	}
}
