package api

import (
	"sort"
	"strings"
)

// OutboundAITagsHeader names an AI call's usage tags (ADR-1436):
// "key=value[,key=value...]".
const OutboundAITagsHeader = "X-Gregale-AI-Tags"

// OutboundAITagOverflowValue is the value of a key's overflow row, which
// aggregates the key's tags past the day's tracking cap. Tag values cannot
// contain "~", so no tag a caller sends lands in it by name.
const OutboundAITagOverflowValue = "~other"

// ParseOutboundAITags reads a tags header into "key=value" tags sorted by
// key. Items are separated by commas, may be padded with spaces, and keys
// must be distinct.
func ParseOutboundAITags(header string) ([]string, bool) {
	if strings.TrimSpace(header) == "" {
		return nil, false
	}
	items := strings.Split(header, ",")
	if len(items) > OutboundAITagsPerCall {
		return nil, false
	}
	tags := make([]string, 0, len(items))
	keys := make(map[string]struct{}, len(items))
	for _, item := range items {
		tag := strings.TrimSpace(item)
		if !ValidOutboundAITag(tag) {
			return nil, false
		}
		key := OutboundAITagKey(tag)
		if _, dup := keys[key]; dup {
			return nil, false
		}
		keys[key] = struct{}{}
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags, true
}

// ValidOutboundAITag checks one "key=value" tag: a key of 1 to 32
// lowercase letters, digits, "_", "." or "-" that starts with a letter, and
// a value of 1 to 64 letters, digits or ". _ : @ + / -".
func ValidOutboundAITag(tag string) bool {
	key, value, found := strings.Cut(tag, "=")
	if !found || len(key) < 1 || len(key) > OutboundAITagKeyMaxBytes || len(value) < 1 || len(value) > OutboundAITagValueMaxBytes ||
		key[0] < 'a' || key[0] > 'z' {
		return false
	}
	for i := 0; i < len(key); i++ {
		if c := key[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && strings.IndexByte("_.-", c) < 0 {
			return false
		}
	}
	for i := 0; i < len(value); i++ {
		if c := value[i]; (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && strings.IndexByte("._:@+/-", c) < 0 {
			return false
		}
	}
	return true
}

// OutboundAITagKey returns a tag's key.
func OutboundAITagKey(tag string) string {
	key, _, _ := strings.Cut(tag, "=")
	return key
}

// OutboundAITagLimit caps one tag's UTC-day cost (ADR-1436).
type OutboundAITagLimit struct {
	Tag                      string `json:"tag"`
	DailyCostLimitMillicents int64  `json:"daily_cost_limit_millicents"`
}

// PutOutboundAITagLimitsRequest replaces an integration's tag limits; an
// empty list removes them.
type PutOutboundAITagLimitsRequest struct {
	Limits []OutboundAITagLimit `json:"limits"`
}

// OutboundAITagLimits is an integration's tag limits, ordered by tag.
type OutboundAITagLimits struct {
	Limits []OutboundAITagLimit `json:"limits"`
}

// ValidOutboundAITagLimits checks at most OutboundAITagLimitsMax limits of
// distinct valid tags, each a positive cost within the cost bound.
func ValidOutboundAITagLimits(limits []OutboundAITagLimit) bool {
	if len(limits) > OutboundAITagLimitsMax {
		return false
	}
	seen := make(map[string]struct{}, len(limits))
	for _, limit := range limits {
		if _, dup := seen[limit.Tag]; dup || !ValidOutboundAITag(limit.Tag) || limit.DailyCostLimitMillicents < 1 ||
			limit.DailyCostLimitMillicents > MaxOutboundAICostMillicents {
			return false
		}
		seen[limit.Tag] = struct{}{}
	}
	return true
}

// OutboundAITagUsageItem is one tag's UTC day (ADR-1436).
type OutboundAITagUsageItem struct {
	Tag                      string `json:"tag"`
	Requests                 int64  `json:"requests"`
	ErrorRequests            int64  `json:"error_requests"`
	InputTokens              int64  `json:"input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	Tokens                   int64  `json:"tokens"`
	CostMillicents           int64  `json:"cost_millicents"`
	DailyCostLimitMillicents *int64 `json:"daily_cost_limit_millicents"`
}

// OutboundAITagUsageExportRow is one NDJSON line of a tag usage export
// (ADR-1438); the CSV export has the same columns in this order.
type OutboundAITagUsageExportRow struct {
	UsageDate      string `json:"usage_date"`
	Tag            string `json:"tag"`
	Requests       int64  `json:"requests"`
	ErrorRequests  int64  `json:"error_requests"`
	InputTokens    int64  `json:"input_tokens"`
	OutputTokens   int64  `json:"output_tokens"`
	Tokens         int64  `json:"tokens"`
	CostMillicents int64  `json:"cost_millicents"`
}

// OutboundAITagUsageExportColumns names the CSV export's header row.
var OutboundAITagUsageExportColumns = []string{"usage_date", "tag", "requests", "error_requests",
	"input_tokens", "output_tokens", "tokens", "cost_millicents"}

// OutboundAITagUsageResponse lists one UTC day's tags by cost, then tokens.
// TrackedTags counts the day's distinct tag rows, overflow rows included.
type OutboundAITagUsageResponse struct {
	UsageDate   string                   `json:"usage_date"`
	TrackedTags int64                    `json:"tracked_tags"`
	Items       []OutboundAITagUsageItem `json:"items"`
}
