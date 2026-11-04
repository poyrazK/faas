package state

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// EdgeCrawlerRanges is one crawler operator's published IP ranges as last
// fetched (ADR-20261022083601). It is platform data: apid writes it, every
// gateway reads it.
type EdgeCrawlerRanges struct {
	Crawler   string
	SourceURL string
	Prefixes  []netip.Prefix
	// SourceCreatedAt is the feed's own creationTime, verbatim ("" when
	// absent); operators format it differently.
	SourceCreatedAt string
	FetchedAt       time.Time
}

// EdgeCrawlerRangesReader is what gateways need: every crawler's ranges.
// It is an optional seam outside Store, implemented on pgCore so an edge
// store has it (ADR 20261021092138).
type EdgeCrawlerRangesReader interface {
	ListEdgeCrawlerRanges(ctx context.Context) ([]EdgeCrawlerRanges, error)
}

// EdgeCrawlerRangesStore adds the refresh job's write.
type EdgeCrawlerRangesStore interface {
	EdgeCrawlerRangesReader
	// UpsertEdgeCrawlerRanges replaces a crawler's ranges. Invalid input
	// (see ValidateEdgeCrawlerRanges) is refused and stores nothing.
	UpsertEdgeCrawlerRanges(ctx context.Context, r EdgeCrawlerRanges) error
}

// ErrInvalidEdgeCrawlerRanges wraps a refused feed.
var ErrInvalidEdgeCrawlerRanges = errors.New("state: invalid edge crawler ranges")

var edgeCrawlerNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidateEdgeCrawlerRanges checks a feed before it is stored: a known
// crawler name shape, an https source, 1 to EdgeCrawlerRangesMaxPrefixes
// prefixes, none broader than /16 (IPv4) or /32 (IPv6). One bad prefix
// refuses the whole feed, so a feed that went wrong never half-applies.
func ValidateEdgeCrawlerRanges(r EdgeCrawlerRanges) error {
	switch {
	case !edgeCrawlerNameRE.MatchString(r.Crawler):
		return fmt.Errorf("crawler %q is not a crawler name", r.Crawler)
	case !strings.HasPrefix(r.SourceURL, "https://") || len(r.SourceURL) > 512:
		return fmt.Errorf("crawler %s: source %q is not an https URL", r.Crawler, r.SourceURL)
	case len(r.Prefixes) == 0 || len(r.Prefixes) > api.EdgeCrawlerRangesMaxPrefixes:
		return fmt.Errorf("crawler %s: %d prefixes, want 1 to %d", r.Crawler, len(r.Prefixes), api.EdgeCrawlerRangesMaxPrefixes)
	case len(r.SourceCreatedAt) > 64:
		return fmt.Errorf("crawler %s: creation time longer than 64 bytes", r.Crawler)
	}
	for _, p := range r.Prefixes {
		minBits := api.EdgeCrawlerRangesMinIPv6Bits
		if p.Addr().Is4() {
			minBits = api.EdgeCrawlerRangesMinIPv4Bits
		}
		if !p.IsValid() || p.Bits() < minBits {
			return fmt.Errorf("crawler %s: prefix %s is broader than /%d", r.Crawler, p, minBits)
		}
	}
	return nil
}

// canonicalCrawlerPrefixes masks, sorts and de-duplicates prefixes.
func canonicalCrawlerPrefixes(in []netip.Prefix) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(in))
	seen := make(map[netip.Prefix]bool, len(in))
	for _, p := range in {
		p = p.Masked()
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if c := out[i].Addr().Compare(out[j].Addr()); c != 0 {
			return c < 0
		}
		return out[i].Bits() < out[j].Bits()
	})
	return out
}

func (s *pgCore) ListEdgeCrawlerRanges(ctx context.Context) ([]EdgeCrawlerRanges, error) {
	rows, err := s.pool.Query(ctx, `
		select crawler, source_url, prefixes, coalesce(source_created_at, ''), fetched_at
		from edge_crawler_ip_ranges order by crawler`)
	if err != nil {
		return nil, fmt.Errorf("state: list edge crawler ranges: %w", mapErr(err))
	}
	defer rows.Close()
	var out []EdgeCrawlerRanges
	for rows.Next() {
		var r EdgeCrawlerRanges
		var prefixes []string
		if err := rows.Scan(&r.Crawler, &r.SourceURL, &prefixes, &r.SourceCreatedAt, &r.FetchedAt); err != nil {
			return nil, fmt.Errorf("state: scan edge crawler ranges: %w", err)
		}
		r.Prefixes = make([]netip.Prefix, 0, len(prefixes))
		for _, raw := range prefixes {
			// The writer validated every prefix; a row edited by hand loses
			// only the entries that no longer parse.
			if p, perr := netip.ParsePrefix(raw); perr == nil {
				r.Prefixes = append(r.Prefixes, p)
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list edge crawler ranges: %w", err)
	}
	return out, nil
}

func (s *PgStore) UpsertEdgeCrawlerRanges(ctx context.Context, r EdgeCrawlerRanges) error {
	if err := ValidateEdgeCrawlerRanges(r); err != nil {
		return errors.Join(ErrInvalidEdgeCrawlerRanges, err)
	}
	prefixes := canonicalCrawlerPrefixes(r.Prefixes)
	text := make([]string, len(prefixes))
	for i, p := range prefixes {
		text[i] = p.String()
	}
	if _, err := s.pool.Exec(ctx, `
		insert into edge_crawler_ip_ranges (crawler, source_url, prefixes, source_created_at, fetched_at)
		values ($1, $2, $3, nullif($4, ''), $5)
		on conflict (crawler) do update set
			source_url = excluded.source_url,
			prefixes = excluded.prefixes,
			source_created_at = excluded.source_created_at,
			fetched_at = excluded.fetched_at`,
		r.Crawler, r.SourceURL, text, r.SourceCreatedAt, r.FetchedAt.UTC(),
	); err != nil {
		return fmt.Errorf("state: upsert edge crawler ranges %s: %w", r.Crawler, mapErr(err))
	}
	return nil
}

func (m *MemStore) ListEdgeCrawlerRanges(_ context.Context) ([]EdgeCrawlerRanges, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]EdgeCrawlerRanges, 0, len(m.edgeCrawlerRanges))
	for _, r := range m.edgeCrawlerRanges {
		r.Prefixes = append([]netip.Prefix(nil), r.Prefixes...)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Crawler < out[j].Crawler })
	return out, nil
}

func (m *MemStore) UpsertEdgeCrawlerRanges(_ context.Context, r EdgeCrawlerRanges) error {
	if err := ValidateEdgeCrawlerRanges(r); err != nil {
		return errors.Join(ErrInvalidEdgeCrawlerRanges, err)
	}
	r.Prefixes = canonicalCrawlerPrefixes(r.Prefixes)
	r.FetchedAt = r.FetchedAt.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.edgeCrawlerRanges == nil {
		m.edgeCrawlerRanges = map[string]EdgeCrawlerRanges{}
	}
	m.edgeCrawlerRanges[r.Crawler] = r
	return nil
}
