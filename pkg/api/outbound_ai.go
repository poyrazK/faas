package api

import (
	"regexp"
	"strings"
	"time"
)

// ValidOutboundAICostLimits accepts optional daily and monthly cost limits
// in millicents. Either limit requires an AI provider (ADR-983).
func ValidOutboundAICostLimits(provider string, daily, monthly *int64) bool {
	for _, limit := range []*int64{daily, monthly} {
		if limit == nil {
			continue
		}
		if provider == "" || *limit < 1 || *limit > MaxOutboundAICostMillicents {
			return false
		}
	}
	return true
}

// OutboundAIPriceEntry prices one model prefix in millicents per million
// tokens. "*" matches any model; the longest matching prefix wins. A nil
// cached-input or cache-write price uses the input price.
type OutboundAIPriceEntry struct {
	ModelPrefix                  string `json:"model_prefix"`
	InputMillicentsPerMTok       int64  `json:"input_millicents_per_mtok"`
	CachedInputMillicentsPerMTok *int64 `json:"cached_input_millicents_per_mtok,omitempty"`
	CacheWriteMillicentsPerMTok  *int64 `json:"cache_write_millicents_per_mtok,omitempty"`
	OutputMillicentsPerMTok      int64  `json:"output_millicents_per_mtok"`
}

// ValidOutboundAIPriceEntries checks one complete price list.
func ValidOutboundAIPriceEntries(entries []OutboundAIPriceEntry) bool {
	if len(entries) > OutboundAIPriceEntriesMax {
		return false
	}
	seen := make(map[string]struct{}, len(entries))
	validPrice := func(value int64) bool { return value >= 0 && value <= MaxOutboundAIPriceMillicentsPerMTok }
	for _, entry := range entries {
		if !validAIModelText(entry.ModelPrefix) || !validPrice(entry.InputMillicentsPerMTok) || !validPrice(entry.OutputMillicentsPerMTok) {
			return false
		}
		for _, optional := range []*int64{entry.CachedInputMillicentsPerMTok, entry.CacheWriteMillicentsPerMTok} {
			if optional != nil && !validPrice(*optional) {
				return false
			}
		}
		if _, dup := seen[entry.ModelPrefix]; dup {
			return false
		}
		seen[entry.ModelPrefix] = struct{}{}
	}
	return true
}

// validAIModelText bounds model names and prefixes to printable ASCII.
func validAIModelText(value string) bool {
	if value == "" || len(value) > OutboundAIModelNameMaxBytes || strings.TrimSpace(value) != value {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// PutOutboundAIPricesRequest replaces an integration's price list. The new
// list takes effect immediately; earlier lists remain for history.
type PutOutboundAIPricesRequest struct {
	Entries []OutboundAIPriceEntry `json:"entries"`
}

// OutboundAIPriceListResponse is the current price list. EffectiveFrom is
// null when no list has been set, in which case cost is zero.
type OutboundAIPriceListResponse struct {
	EffectiveFrom *time.Time             `json:"effective_from"`
	Entries       []OutboundAIPriceEntry `json:"entries"`
}

// PutOutboundBindingAIBudgetRequest sets or clears an app's daily AI token
// limit on one integration binding.
type PutOutboundBindingAIBudgetRequest struct {
	DailyTokenLimit *int64 `json:"daily_token_limit"`
}

var outboundAIRouteName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// OutboundAIRouteTarget is one fallback step: a managed AI integration and
// an optional model that replaces the request's "model".
type OutboundAIRouteTarget struct {
	IntegrationID string `json:"integration_id"`
	Model         string `json:"model,omitempty"`
}

// OutboundAIRoute tries its targets in order (ADR-983). Callers use
// $GREGALE_OUTBOUND_URL/r/{id}/{provider path}.
type OutboundAIRoute struct {
	ID        string                  `json:"id"`
	Name      string                  `json:"name"`
	Targets   []OutboundAIRouteTarget `json:"targets"`
	CreatedAt time.Time               `json:"created_at"`
	UpdatedAt time.Time               `json:"updated_at"`
}

type OutboundAIRouteList struct {
	Items []OutboundAIRoute `json:"items"`
}

// CreateOutboundAIRouteRequest names a route and its ordered targets.
type CreateOutboundAIRouteRequest struct {
	Name    string                  `json:"name"`
	Targets []OutboundAIRouteTarget `json:"targets"`
}

// PutOutboundAIRouteTargetsRequest replaces a route's ordered targets.
type PutOutboundAIRouteTargetsRequest struct {
	Targets []OutboundAIRouteTarget `json:"targets"`
}

// ValidOutboundAIRouteName accepts DNS-label style route names.
func ValidOutboundAIRouteName(name string) bool { return outboundAIRouteName.MatchString(name) }

// ValidOutboundAIRouteTargetShape checks counts and model text; ownership and
// provider agreement are checked against stored integrations.
func ValidOutboundAIRouteTargetShape(targets []OutboundAIRouteTarget) bool {
	if len(targets) < 1 || len(targets) > OutboundAIRouteTargetsMax {
		return false
	}
	for _, target := range targets {
		if target.IntegrationID == "" || (target.Model != "" && !validAIModelText(target.Model)) {
			return false
		}
	}
	return true
}
