package api

import (
	"regexp"
	"strings"
	"time"
)

// ValidOutboundAICostLimits accepts optional daily and monthly cost limits
// in millicents. Either limit requires an AI provider (ADR-990).
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

// OutboundAIRoute tries its targets in order (ADR-990). Callers use
// $GREGALE_OUTBOUND_URL/r/{id}/{provider path}.
type OutboundAIRoute struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Format is "native" or "openai" (ADR-991).
	Format    string                  `json:"format"`
	Targets   []OutboundAIRouteTarget `json:"targets"`
	CreatedAt time.Time               `json:"created_at"`
	UpdatedAt time.Time               `json:"updated_at"`
}

type OutboundAIRouteList struct {
	Items []OutboundAIRoute `json:"items"`
}

// CreateOutboundAIRouteRequest names a route and its ordered targets.
type CreateOutboundAIRouteRequest struct {
	Name string `json:"name"`
	// Format defaults to "native"; "openai" translates (ADR-991).
	Format  string                  `json:"format,omitempty"`
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

// Route formats (ADR-991). A native route sends the caller's request to
// targets of one provider. An openai route accepts OpenAI chat completions
// and translates them for Anthropic and Gemini targets.
const (
	OutboundAIRouteFormatNative = "native"
	OutboundAIRouteFormatOpenAI = "openai"
)

// ValidOutboundAIRouteFormat accepts the empty value (native) and both formats.
func ValidOutboundAIRouteFormat(format string) bool {
	return format == "" || format == OutboundAIRouteFormatNative || format == OutboundAIRouteFormatOpenAI
}

// OutboundAIRouteFormatTargetProvider reports whether an integration of the
// given provider may serve a route of the given format, and whether that
// target must name its model.
func OutboundAIRouteFormatTargetProvider(format, provider string) (allowed, needsModel bool) {
	if format != OutboundAIRouteFormatOpenAI {
		return provider != "", false
	}
	switch provider {
	case OutboundAIProviderOpenAI:
		return true, false
	case OutboundAIProviderAzureOpenAI, OutboundAIProviderAnthropic, OutboundAIProviderGemini:
		return true, true
	}
	return false, false
}

// PutOutboundBindingAIGuardrailsRequest replaces an app's per-minute limits,
// model allow-list and output ceiling on one integration (ADR-991). Null
// fields remove that guardrail.
type PutOutboundBindingAIGuardrailsRequest struct {
	RequestsPerMinute *int64   `json:"requests_per_minute"`
	TokensPerMinute   *int64   `json:"tokens_per_minute"`
	AllowedModels     []string `json:"allowed_models"`
	MaxOutputTokens   *int64   `json:"max_output_tokens"`
}

// Valid checks every guardrail's range and the allow-list syntax.
func (r PutOutboundBindingAIGuardrailsRequest) Valid() bool {
	if r.RequestsPerMinute != nil && (*r.RequestsPerMinute < 1 || *r.RequestsPerMinute > MaxOutboundAIRequestsPerMinute) {
		return false
	}
	if r.TokensPerMinute != nil && (*r.TokensPerMinute < 1 || *r.TokensPerMinute > MaxOutboundAITokensPerMinute) {
		return false
	}
	if r.MaxOutputTokens != nil && (*r.MaxOutputTokens < 1 || *r.MaxOutputTokens > MaxOutboundAIMaxOutputTokens) {
		return false
	}
	return r.AllowedModels == nil || ValidOutboundAIAllowedModels(r.AllowedModels)
}

// ValidOutboundAIAllowedModels accepts 1 to OutboundAIAllowedModelsMax
// distinct exact model names or prefixes ending in a single "*".
func ValidOutboundAIAllowedModels(models []string) bool {
	if len(models) < 1 || len(models) > OutboundAIAllowedModelsMax {
		return false
	}
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		stem := strings.TrimSuffix(model, "*")
		if !validAIModelText(model) || stem == "" || strings.Contains(stem, "*") {
			return false
		}
		if _, dup := seen[model]; dup {
			return false
		}
		seen[model] = struct{}{}
	}
	return true
}

// OutboundAIModelAllowed reports whether model matches an allow-list entry.
// A nil list allows every model; an empty model never matches a list.
func OutboundAIModelAllowed(patterns []string, model string) bool {
	if patterns == nil {
		return true
	}
	if model == "" {
		return false
	}
	for _, pattern := range patterns {
		if stem, prefix := strings.CutSuffix(pattern, "*"); prefix {
			if strings.HasPrefix(model, stem) {
				return true
			}
		} else if pattern == model {
			return true
		}
	}
	return false
}

var outboundCredentialKeyLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

// ValidOutboundCredentialKeyLabel accepts DNS-label style key labels.
func ValidOutboundCredentialKeyLabel(label string) bool {
	return outboundCredentialKeyLabel.MatchString(label)
}

// OutboundCredentialKey describes one extra provider key (ADR-991). The
// sealed value is write-only and never returned.
type OutboundCredentialKey struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

type OutboundCredentialKeyList struct {
	Items []OutboundCredentialKey `json:"items"`
}

// CreateOutboundCredentialKeyRequest adds an extra key. Authorization has
// the same format as the integration's primary credential.
type CreateOutboundCredentialKeyRequest struct {
	Label         string `json:"label"`
	Authorization string `json:"authorization"`
}

// AI budget names reported by alerts (ADR-991).
const (
	OutboundAIBudgetDailyTokens    = "daily_tokens"
	OutboundAIBudgetAppDailyTokens = "app_daily_tokens"
	OutboundAIBudgetDailyCost      = "daily_cost"
	OutboundAIBudgetMonthlyCost    = "monthly_cost"
)

// OutboundAIBudgetAlert records the first settlement that reached a
// threshold of one budget in one period. Limit and Used are tokens for
// token budgets and millicents for cost budgets.
type OutboundAIBudgetAlert struct {
	ID               string    `json:"id"`
	IntegrationID    string    `json:"integration_id"`
	Budget           string    `json:"budget"`
	AppID            *string   `json:"app_id"`
	PeriodStart      string    `json:"period_start"`
	ThresholdPercent int32     `json:"threshold_percent"`
	Limit            int64     `json:"limit"`
	Used             int64     `json:"used"`
	CrossedAt        time.Time `json:"crossed_at"`
}

type OutboundAIBudgetAlertList struct {
	Items []OutboundAIBudgetAlert `json:"items"`
}

// OutboundAIBudgetThresholdWebhookPayload is the data of an
// outbound.ai_budget.threshold webhook. AlertID is stable across retries.
type OutboundAIBudgetThresholdWebhookPayload struct {
	AlertID          string    `json:"alert_id"`
	IntegrationID    string    `json:"integration_id"`
	IntegrationName  string    `json:"integration_name"`
	Budget           string    `json:"budget"`
	AppID            *string   `json:"app_id"`
	PeriodStart      string    `json:"period_start"`
	ThresholdPercent int32     `json:"threshold_percent"`
	Limit            int64     `json:"limit"`
	Used             int64     `json:"used"`
	CrossedAt        time.Time `json:"crossed_at"`
}

// OutboundAIRequestLogEntry is one settled AI call's metadata (ADR-991).
// Caller is an app ID or "runs". No request or response content is kept.
type OutboundAIRequestLogEntry struct {
	ID                string    `json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	Caller            string    `json:"caller"`
	RouteID           *string   `json:"route_id"`
	Model             string    `json:"model"`
	Status            int       `json:"status"`
	Outcome           string    `json:"outcome"`
	InputTokens       int64     `json:"input_tokens"`
	CachedInputTokens int64     `json:"cached_input_tokens"`
	CacheWriteTokens  int64     `json:"cache_write_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	CostMillicents    int64     `json:"cost_millicents"`
	DurationMS        int64     `json:"duration_ms"`
	TimeToFirstByteMS *int64    `json:"ttfb_ms"`
	Attempts          int       `json:"attempts"`
}

// OutboundAIRequestLogList is one newest-first page. NextBefore, when set,
// is the created_at cursor for the next page.
type OutboundAIRequestLogList struct {
	Items      []OutboundAIRequestLogEntry `json:"items"`
	NextBefore *time.Time                  `json:"next_before"`
}
