package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// OutboundAITagUsageItem is one tag's UTC day (ADR-1436). DailyCostLimit is
// the tag's limit, or nil.
type OutboundAITagUsageItem struct {
	Tag            string
	Requests       int64
	ErrorRequests  int64
	InputTokens    int64
	OutputTokens   int64
	CostMillicents int64
	DailyCostLimit *int64
}

// OutboundAITagUsageRow is one tag's UTC day in an export (ADR-1438).
type OutboundAITagUsageRow struct {
	UsageDate string
	OutboundAITagUsageItem
}

// OutboundAITagUsage is one UTC day of per-tag rollups.
type OutboundAITagUsage struct {
	UsageDate   string
	TrackedTags int64
	Items       []OutboundAITagUsageItem
}

// OutboundAITagStore reads the per-tag rollups outboundd writes and owns an
// integration's tag limits (ADR-1436). An empty key lists every key's tags.
type OutboundAITagStore interface {
	GetOutboundAITagUsage(ctx context.Context, accountID, integrationID, usageDate, key string, limit int) (OutboundAITagUsage, error)
	ListOutboundAITagLimits(ctx context.Context, accountID, integrationID string) ([]api.OutboundAITagLimit, error)
	// SetOutboundAITagLimits replaces the limits of a managed AI
	// integration; an empty list removes them.
	SetOutboundAITagLimits(ctx context.Context, accountID, integrationID string, limits []api.OutboundAITagLimit) error
	// ExportOutboundAITagUsage visits every tag row of the UTC days from
	// through to, optionally of one key, ordered by day and tag (ADR-1438).
	// It returns ErrNotFound or ErrInvalidArgument before visiting any row.
	ExportOutboundAITagUsage(ctx context.Context, accountID, integrationID, from, to, key string, visit func(OutboundAITagUsageRow) error) error
}

// outboundAITagExportRange validates an export's inclusive day range and
// optional key.
func outboundAITagExportRange(from, to, key string, now time.Time) (string, string, bool) {
	if key != "" && !api.ValidOutboundAITag(key+"=x") {
		return "", "", false
	}
	return outboundAIUserExportRange(from, to, now)
}

// outboundAITagUsageQuery validates a per-tag read: a day inside the
// rollup retention, defaulting to today, and an optional valid key.
func outboundAITagUsageQuery(usageDate, key string, now time.Time) (string, bool) {
	if key != "" && !api.ValidOutboundAITag(key+"=x") {
		return "", false
	}
	return outboundAIUserUsageQuery(usageDate, "", now)
}

// sortedOutboundAITagLimits orders limits by tag.
func sortedOutboundAITagLimits(limits []api.OutboundAITagLimit) []api.OutboundAITagLimit {
	out := append([]api.OutboundAITagLimit{}, limits...)
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out
}

// outboundAITagKeyMatches reports a tag of the key, or any tag for "".
func outboundAITagKeyMatches(tag, key string) bool {
	return key == "" || strings.HasPrefix(tag, key+"=")
}
