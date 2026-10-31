// Package crawlerranges keeps crawler operators' published IP ranges
// (ADR-20261022083601) in the store, where gateways read them to verify AI
// crawlers (verified_ai_crawler) and, without a DNS lookup, search crawlers
// (verified_bot). apid fetches; gateways never fetch on their own.
package crawlerranges

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// UserAgent identifies the refresh job to feed operators.
const UserAgent = "Gregale-CrawlerRanges/1.0 (+https://gregale.dev/docs/security#ai-crawlers)"

// Refresh outcomes, for the refresh counter.
const (
	OutcomeOK         = "ok"
	OutcomeFresh      = "fresh"
	OutcomeFetchError = "fetch_error"
	OutcomeInvalid    = "invalid"
	OutcomeStoreError = "store_error"
)

// Refresher fetches every feed on a fixed interval. Several apid nodes may
// run one: a feed another node fetched within half the interval is
// skipped, and writes are idempotent upserts.
type Refresher struct {
	Store  state.EdgeCrawlerRangesStore
	Logger *slog.Logger
	// Client nil uses a client with a 30 s timeout that follows only
	// https redirects.
	Client *http.Client
	// Feeds nil uses api.EdgeCrawlerRangeFeeds().
	Feeds []api.EdgeCrawlerRangeFeed
	// Every zero uses api.EdgeCrawlerRangesRefreshInterval.
	Every time.Duration
	// Observe, when set, counts each feed's outcome.
	Observe func(crawler, outcome string)
	Now     func() time.Time
}

// Run refreshes once at start and then every interval until ctx ends.
func (r *Refresher) Run(ctx context.Context) error {
	every := r.every()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		r.RefreshAll(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// RefreshAll refreshes every feed not fetched within half the interval.
// A failing feed is logged and keeps its previous ranges.
func (r *Refresher) RefreshAll(ctx context.Context) {
	fetched := map[string]time.Time{}
	if current, err := r.Store.ListEdgeCrawlerRanges(ctx); err == nil {
		for _, c := range current {
			fetched[c.Crawler] = c.FetchedAt
		}
	}
	for _, feed := range r.feeds() {
		if ctx.Err() != nil {
			return
		}
		if at, ok := fetched[feed.Crawler]; ok && r.now().Sub(at) < r.every()/2 {
			r.observe(feed.Crawler, OutcomeFresh)
			continue
		}
		outcome, err := r.refresh(ctx, feed)
		r.observe(feed.Crawler, outcome)
		if err != nil && r.Logger != nil {
			r.Logger.Warn("crawler ranges refresh failed; keeping the previous ranges",
				"crawler", feed.Crawler, "url", feed.URL, "outcome", outcome, "err", err)
		}
	}
}

// feedDocument is the shared format of every operator's feed:
// {"creationTime": "...", "prefixes": [{"ipv4Prefix": "..."}, {"ipv6Prefix": "..."}]}.
type feedDocument struct {
	CreationTime string `json:"creationTime"`
	Prefixes     []struct {
		IPv4 string `json:"ipv4Prefix"`
		IPv6 string `json:"ipv6Prefix"`
	} `json:"prefixes"`
}

func (r *Refresher) refresh(ctx context.Context, feed api.EdgeCrawlerRangeFeed) (string, error) {
	body, err := r.fetch(ctx, feed.URL)
	if err != nil {
		return OutcomeFetchError, err
	}
	ranges, err := ParseFeed(feed, body, r.now())
	if err != nil {
		return OutcomeInvalid, err
	}
	if err := r.Store.UpsertEdgeCrawlerRanges(ctx, ranges); err != nil {
		if errors.Is(err, state.ErrInvalidEdgeCrawlerRanges) {
			return OutcomeInvalid, err
		}
		return OutcomeStoreError, err
	}
	return OutcomeOK, nil
}

func (r *Refresher) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, api.EdgeCrawlerRangesMaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if len(body) > api.EdgeCrawlerRangesMaxBodyBytes {
		return nil, fmt.Errorf("read %s: larger than %d bytes", url, api.EdgeCrawlerRangesMaxBodyBytes)
	}
	return body, nil
}

// ParseFeed reads one feed document. Any unparseable or out-of-bounds
// prefix refuses the whole feed.
func ParseFeed(feed api.EdgeCrawlerRangeFeed, body []byte, now time.Time) (state.EdgeCrawlerRanges, error) {
	var doc feedDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return state.EdgeCrawlerRanges{}, fmt.Errorf("decode %s: %w", feed.URL, err)
	}
	out := state.EdgeCrawlerRanges{Crawler: feed.Crawler, SourceURL: feed.URL, FetchedAt: now}
	if len(doc.CreationTime) <= 64 {
		out.SourceCreatedAt = doc.CreationTime
	}
	for i, p := range doc.Prefixes {
		raw := p.IPv4
		if raw == "" {
			raw = p.IPv6
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return state.EdgeCrawlerRanges{}, fmt.Errorf("decode %s: prefix %d %q: %w", feed.URL, i, raw, err)
		}
		out.Prefixes = append(out.Prefixes, prefix)
	}
	if err := state.ValidateEdgeCrawlerRanges(out); err != nil {
		return state.EdgeCrawlerRanges{}, err
	}
	return out, nil
}

func (r *Refresher) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to %s is not https", req.URL.Redacted())
			}
			return nil
		},
	}
}

func (r *Refresher) feeds() []api.EdgeCrawlerRangeFeed {
	if r.Feeds != nil {
		return r.Feeds
	}
	return api.EdgeCrawlerRangeFeeds()
}

func (r *Refresher) every() time.Duration {
	if r.Every > 0 {
		return r.Every
	}
	return api.EdgeCrawlerRangesRefreshInterval
}

func (r *Refresher) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Refresher) observe(crawler, outcome string) {
	if r.Observe != nil {
		r.Observe(crawler, outcome)
	}
}
