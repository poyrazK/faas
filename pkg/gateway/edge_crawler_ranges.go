package gateway

// Published crawler IP ranges (ADR-20261022083601). apid stores them; every
// gateway rereads them on an interval into an immutable snapshot. A crawler
// whose ranges were last fetched more than EdgeCrawlerRangesStaleAfter ago
// stops verifying, so a feed that stopped refreshing cannot vouch for
// addresses its operator has since given up.

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// CrawlerRangesReader is the store read the loader needs.
type CrawlerRangesReader interface {
	ListEdgeCrawlerRanges(ctx context.Context) ([]state.EdgeCrawlerRanges, error)
}

type crawlerRangeEntry struct {
	prefixes  []netip.Prefix
	fetchedAt time.Time
}

// CrawlerRanges is the gateway's snapshot. The zero value and nil hold no
// ranges. It is safe for concurrent use.
type CrawlerRanges struct {
	set atomic.Pointer[map[string]crawlerRangeEntry]
	now func() time.Time
}

// NewCrawlerRanges returns an empty snapshot.
func NewCrawlerRanges() *CrawlerRanges {
	return &CrawlerRanges{now: time.Now}
}

// Replace installs rows as the snapshot.
func (c *CrawlerRanges) Replace(rows []state.EdgeCrawlerRanges) {
	next := make(map[string]crawlerRangeEntry, len(rows))
	for _, r := range rows {
		next[r.Crawler] = crawlerRangeEntry{prefixes: r.Prefixes, fetchedAt: r.FetchedAt}
	}
	c.set.Store(&next)
}

// Contains reports whether ip is in crawler's published ranges and those
// ranges are fresh.
func (c *CrawlerRanges) Contains(crawler string, ip net.IP) bool {
	if c == nil || ip == nil {
		return false
	}
	set := c.set.Load()
	if set == nil {
		return false
	}
	entry, ok := (*set)[crawler]
	if !ok || c.clock().Sub(entry.fetchedAt) > api.EdgeCrawlerRangesStaleAfter {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, p := range entry.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Run loads the ranges now and then every interval until ctx ends. A failed
// read keeps the previous snapshot.
func (c *CrawlerRanges) Run(ctx context.Context, store CrawlerRangesReader, every time.Duration, log *slog.Logger) {
	if every <= 0 {
		every = api.EdgeCrawlerRangesReload
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		rows, err := store.ListEdgeCrawlerRanges(ctx)
		switch {
		case err == nil:
			c.Replace(rows)
		case log != nil && ctx.Err() == nil:
			log.Warn("crawler ranges load failed; keeping the previous ranges", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *CrawlerRanges) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
