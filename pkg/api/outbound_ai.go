package api

import (
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
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
	// Media prices (ADR-997). Image-token prices fall back to the input and
	// output prices. The per-image, per-audio-minute and per-character
	// prices are zero when absent.
	InputImageMillicentsPerMTok    *int64 `json:"input_image_millicents_per_mtok,omitempty"`
	OutputImageMillicentsPerMTok   *int64 `json:"output_image_millicents_per_mtok,omitempty"`
	MillicentsPerImage             *int64 `json:"millicents_per_image,omitempty"`
	MillicentsPerAudioMinute       *int64 `json:"millicents_per_audio_minute,omitempty"`
	MillicentsPerMillionCharacters *int64 `json:"millicents_per_million_characters,omitempty"`
	// MillicentsPerVideoSecond prices each second of generated video
	// (ADR-1432). A model prefix may name a resolution after "@", such as
	// "veo-3.1-generate-001@4k", which wins for calls at that resolution.
	MillicentsPerVideoSecond *int64 `json:"millicents_per_video_second,omitempty"`
	// Built-in tool prices (ADR-1433): per web search (a call, query or
	// grounded prompt, as the provider bills it), per file search, and per
	// GB of code-interpreter container memory per session.
	MillicentsPerWebSearch     *int64 `json:"millicents_per_web_search,omitempty"`
	MillicentsPerFileSearch    *int64 `json:"millicents_per_file_search,omitempty"`
	MillicentsPerCodeSessionGB *int64 `json:"millicents_per_code_session_gb,omitempty"`
	// Audio token prices per million (ADR 20261019072722). Each falls back
	// to the text price of the same kind: input, cached input or output.
	InputAudioMillicentsPerMTok       *int64 `json:"input_audio_millicents_per_mtok,omitempty"`
	CachedInputAudioMillicentsPerMTok *int64 `json:"cached_input_audio_millicents_per_mtok,omitempty"`
	OutputAudioMillicentsPerMTok      *int64 `json:"output_audio_millicents_per_mtok,omitempty"`
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
		for _, optional := range []*int64{entry.CachedInputMillicentsPerMTok, entry.CacheWriteMillicentsPerMTok,
			entry.InputImageMillicentsPerMTok, entry.OutputImageMillicentsPerMTok, entry.MillicentsPerMillionCharacters,
			entry.InputAudioMillicentsPerMTok, entry.CachedInputAudioMillicentsPerMTok, entry.OutputAudioMillicentsPerMTok} {
			if optional != nil && !validPrice(*optional) {
				return false
			}
		}
		if !optionalWithin(entry.MillicentsPerImage, MaxOutboundAIPriceMillicentsPerImage) ||
			!optionalWithin(entry.MillicentsPerAudioMinute, MaxOutboundAIPriceMillicentsPerAudioMinute) ||
			!optionalWithin(entry.MillicentsPerVideoSecond, MaxOutboundAIPriceMillicentsPerVideoSecond) ||
			!optionalWithin(entry.MillicentsPerWebSearch, MaxOutboundAIPriceMillicentsPerToolUse) ||
			!optionalWithin(entry.MillicentsPerFileSearch, MaxOutboundAIPriceMillicentsPerToolUse) ||
			!optionalWithin(entry.MillicentsPerCodeSessionGB, MaxOutboundAIPriceMillicentsPerToolUse) {
			return false
		}
		if _, dup := seen[entry.ModelPrefix]; dup {
			return false
		}
		seen[entry.ModelPrefix] = struct{}{}
	}
	return true
}

func optionalWithin(value *int64, maximum int64) bool {
	return value == nil || (*value >= 0 && *value <= maximum)
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

// Content guardrail actions for one detector (ADR-997).
const (
	OutboundAIContentOff    = "off"
	OutboundAIContentRedact = "redact"
	OutboundAIContentBlock  = "block"
)

// OutboundAIContentPolicy is an integration's content guardrails (ADR-997).
// Each detector is off, redacts its findings before the request reaches the
// provider, or blocks the request. A request that contains a blocked term,
// compared without case, is blocked. The gateway checks the string values
// of buffered JSON bodies and the text fields of multipart forms.
// Moderation adds a model-based check (ADR-1340), and Response checks the
// model's output (ADR-1435).
type OutboundAIContentPolicy struct {
	Email        string                   `json:"email"`
	PaymentCard  string                   `json:"payment_card"`
	IBAN         string                   `json:"iban"`
	Secret       string                   `json:"secret"`
	BlockedTerms []string                 `json:"blocked_terms"`
	Moderation   OutboundAIModeration     `json:"moderation"`
	Response     OutboundAIResponsePolicy `json:"response"`
}

// OutboundAIResponsePolicy checks the text a model sends back before the
// caller sees it (ADR-1435). The detectors redact or block as on requests;
// BlockedTerms blocks a response holding one of the policy's terms; and
// Moderation flags or blocks it with the policy's moderation check.
type OutboundAIResponsePolicy struct {
	Email        string `json:"email"`
	PaymentCard  string `json:"payment_card"`
	IBAN         string `json:"iban"`
	Secret       string `json:"secret"`
	BlockedTerms string `json:"blocked_terms"`
	Moderation   string `json:"moderation"`
}

// Normalized replaces empty actions with off.
func (p OutboundAIResponsePolicy) Normalized() OutboundAIResponsePolicy {
	for _, action := range []*string{&p.Email, &p.PaymentCard, &p.IBAN, &p.Secret, &p.BlockedTerms, &p.Moderation} {
		if *action == "" {
			*action = OutboundAIContentOff
		}
	}
	return p
}

// Active reports whether responses are checked.
func (p OutboundAIResponsePolicy) Active() bool {
	p = p.Normalized()
	for _, action := range []string{p.Email, p.PaymentCard, p.IBAN, p.Secret, p.BlockedTerms, p.Moderation} {
		if action != OutboundAIContentOff {
			return true
		}
	}
	return false
}

func (p OutboundAIResponsePolicy) valid() bool {
	p = p.Normalized()
	for _, action := range []string{p.Email, p.PaymentCard, p.IBAN, p.Secret} {
		if action != OutboundAIContentOff && action != OutboundAIContentRedact && action != OutboundAIContentBlock {
			return false
		}
	}
	return (p.BlockedTerms == OutboundAIContentOff || p.BlockedTerms == OutboundAIContentBlock) &&
		(p.Moderation == OutboundAIModerationOff || p.Moderation == OutboundAIModerationFlag || p.Moderation == OutboundAIModerationBlock)
}

// Normalized replaces empty actions with off and a nil term list with an
// empty one. The moderation check keeps its settings while responses use
// them (ADR-1435).
func (p OutboundAIContentPolicy) Normalized() OutboundAIContentPolicy {
	for _, action := range []*string{&p.Email, &p.PaymentCard, &p.IBAN, &p.Secret} {
		if *action == "" {
			*action = OutboundAIContentOff
		}
	}
	if p.BlockedTerms == nil {
		p.BlockedTerms = []string{}
	}
	p.Response = p.Response.Normalized()
	p.Moderation = p.Moderation.normalized(p.Response.Moderation != OutboundAIModerationOff)
	return p
}

// Active reports whether any detector or term is on. Moderation is
// separate: it runs after admission (ADR-1340).
func (p OutboundAIContentPolicy) Active() bool {
	p = p.Normalized()
	for _, action := range []string{p.Email, p.PaymentCard, p.IBAN, p.Secret} {
		if action != OutboundAIContentOff {
			return true
		}
	}
	return len(p.BlockedTerms) > 0
}

// Valid checks actions and the blocked terms: at most
// OutboundAIBlockedTermsMax printable, trimmed terms of at most
// OutboundAIBlockedTermMaxBytes, unique without case.
func (p OutboundAIContentPolicy) Valid() bool {
	p = p.Normalized()
	for _, action := range []string{p.Email, p.PaymentCard, p.IBAN, p.Secret} {
		if action != OutboundAIContentOff && action != OutboundAIContentRedact && action != OutboundAIContentBlock {
			return false
		}
	}
	if len(p.BlockedTerms) > OutboundAIBlockedTermsMax || !p.Response.valid() ||
		!p.Moderation.validFor(p.Response.Moderation != OutboundAIModerationOff) {
		return false
	}
	seen := make(map[string]struct{}, len(p.BlockedTerms))
	for _, term := range p.BlockedTerms {
		folded := strings.ToLower(term)
		if term == "" || len(term) > OutboundAIBlockedTermMaxBytes || strings.TrimSpace(term) != term || !utf8.ValidString(term) ||
			strings.IndexFunc(term, unicode.IsControl) >= 0 {
			return false
		}
		if _, dup := seen[folded]; dup {
			return false
		}
		seen[folded] = struct{}{}
	}
	return true
}

// OutboundAIPriceCatalogResponse is the built-in price catalog of one
// provider (ADR-994): list prices in Currency, the billing currency, rounded
// up (ADR 20261019123822). It prices the models an integration's own list
// does not name; a "*" entry in that list turns the catalog off for the
// integration.
type OutboundAIPriceCatalogResponse struct {
	AIProvider string `json:"ai_provider"`
	Source     string `json:"source"`
	Generated  string `json:"generated"`
	Currency   string `json:"currency"`
	// Deprecated: USDPerEUR is always "1" and FXSource names no rate since
	// the catalog moved to US dollars; released SDKs still require them.
	USDPerEUR string                 `json:"usd_per_eur"`
	FXSource  string                 `json:"fx_source"`
	Entries   []OutboundAIPriceEntry `json:"entries"`
}

// OutboundAIPriceCatalogFXSource is the deprecated fx_source of a US-dollar
// catalog (ADR 20261019123822).
const OutboundAIPriceCatalogFXSource = "none: list prices are in US dollars"

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
	// Weight is the target's share of first attempts on a weighted route,
	// from 0 to 100 (ADR-1434).
	Weight *int `json:"weight,omitempty"`
}

// Route strategies (ADR-1434): how a route picks the target a call tries
// first. The other targets follow as fallbacks in route order.
const (
	OutboundAIRouteStrategyOrdered  = "ordered"
	OutboundAIRouteStrategyWeighted = "weighted"
	OutboundAIRouteStrategyLatency  = "latency"
	OutboundAIRouteStrategyCost     = "cost"
)

// ValidOutboundAIRouteStrategy checks a strategy with its targets' weights
// and latency target. A weighted route weighs every target, at least one
// above zero; other strategies take no weights; only a cost route takes
// max_ttfb_ms.
func ValidOutboundAIRouteStrategy(strategy string, maxTTFBMS *int, targets []OutboundAIRouteTarget) bool {
	switch strategy {
	case "", OutboundAIRouteStrategyOrdered, OutboundAIRouteStrategyLatency, OutboundAIRouteStrategyCost:
		for _, target := range targets {
			if target.Weight != nil {
				return false
			}
		}
	case OutboundAIRouteStrategyWeighted:
		total := 0
		for _, target := range targets {
			if target.Weight == nil || *target.Weight < 0 || *target.Weight > OutboundAIRouteWeightMax {
				return false
			}
			total += *target.Weight
		}
		if total == 0 {
			return false
		}
	default:
		return false
	}
	return maxTTFBMS == nil || (strategy == OutboundAIRouteStrategyCost && *maxTTFBMS >= 1 && *maxTTFBMS <= OutboundAIRouteMaxTTFBMSMax)
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
	// Canary is the route's model canary, if it has one (ADR-1340).
	Canary *OutboundAIRouteCanary `json:"canary"`
	// Strategy picks the target each call tries first; MaxTTFBMS is a cost
	// route's latency target (ADR-1434).
	Strategy  string `json:"strategy"`
	MaxTTFBMS *int   `json:"max_ttfb_ms"`
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
	// Strategy defaults to "ordered"; MaxTTFBMS is a cost route's latency
	// target (ADR-1434).
	Strategy  string `json:"strategy,omitempty"`
	MaxTTFBMS *int   `json:"max_ttfb_ms,omitempty"`
}

// PutOutboundAIRouteTargetsRequest replaces a route's ordered targets and
// its strategy, which defaults to "ordered" (ADR-1434).
type PutOutboundAIRouteTargetsRequest struct {
	Targets   []OutboundAIRouteTarget `json:"targets"`
	Strategy  string                  `json:"strategy,omitempty"`
	MaxTTFBMS *int                    `json:"max_ttfb_ms,omitempty"`
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
// and embeddings and translates them for each target's provider (ADR-995).
const (
	OutboundAIRouteFormatNative = "native"
	OutboundAIRouteFormatOpenAI = "openai"
)

// OutboundVertexAnthropicVersion is the anthropic_version a translated
// Claude request on Vertex AI carries (ADR-995).
const OutboundVertexAnthropicVersion = "vertex-2023-10-16"

var outboundVertexBasePattern = regexp.MustCompile(`^/(v1|v1beta1)/projects/([^/]+)/locations/([^/]+)(/|$)`)

// OutboundVertexBasePath derives "/{version}/projects/{project}/locations/{location}"
// from a Vertex integration's allowed path prefixes. Translated routes need
// exactly one such base; prefixes naming none or several are refused.
func OutboundVertexBasePath(prefixes []string) (string, bool) {
	base := ""
	for _, prefix := range prefixes {
		match := outboundVertexBasePattern.FindStringSubmatch(prefix)
		if match == nil {
			continue
		}
		found := "/" + match[1] + "/projects/" + match[2] + "/locations/" + match[3]
		if base != "" && base != found {
			return "", false
		}
		base = found
	}
	return base, base != ""
}

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
	case OutboundAIProviderAzureOpenAI, OutboundAIProviderAnthropic, OutboundAIProviderGemini,
		OutboundAIProviderVertex, OutboundAIProviderBedrock:
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
	// Per-end-user UTC-day budgets (ADR-995); their alerts name EndUser.
	OutboundAIBudgetUserDailyTokens = "user_daily_tokens"
	OutboundAIBudgetUserDailyCost   = "user_daily_cost"
	// OutboundAIBudgetTagDailyCost is one tag's UTC-day cost limit
	// (ADR-1438); its alerts name Tag.
	OutboundAIBudgetTagDailyCost = "tag_daily_cost"
)

// OutboundAIBudgetIsPerUser reports whether a budget is one end user's.
func OutboundAIBudgetIsPerUser(budget string) bool {
	return budget == OutboundAIBudgetUserDailyTokens || budget == OutboundAIBudgetUserDailyCost
}

// OutboundAIBudgetAlert records the first settlement that reached a
// threshold of one budget in one period. Limit and Used are tokens for
// token budgets and millicents for cost budgets.
type OutboundAIBudgetAlert struct {
	ID               string    `json:"id"`
	IntegrationID    string    `json:"integration_id"`
	Budget           string    `json:"budget"`
	AppID            *string   `json:"app_id"`
	EndUser          *string   `json:"end_user"`
	PeriodStart      string    `json:"period_start"`
	ThresholdPercent int32     `json:"threshold_percent"`
	Limit            int64     `json:"limit"`
	Used             int64     `json:"used"`
	CrossedAt        time.Time `json:"crossed_at"`
	// Tag is the tag of a tag_daily_cost alert (ADR-1438).
	Tag *string `json:"tag"`
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
	EndUser          *string   `json:"end_user"`
	PeriodStart      string    `json:"period_start"`
	ThresholdPercent int32     `json:"threshold_percent"`
	Limit            int64     `json:"limit"`
	Used             int64     `json:"used"`
	CrossedAt        time.Time `json:"crossed_at"`
	// Tag is the tag whose daily cost limit crossed the threshold
	// (ADR-1438).
	Tag *string `json:"tag"`
}

// OutboundAIRequestLogEntry is one settled AI call's metadata (ADR-991).
// Caller is an app ID or "runs". Its bodies are kept only when the
// integration opted in to content capture (ADR-996).
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
	// EndUser is the call's X-Gregale-AI-User value, when sent (ADR-994).
	EndUser *string `json:"end_user"`
	// Tags are the call's X-Gregale-AI-Tags, sorted by key (ADR-1436).
	Tags []string `json:"tags"`
	// ContentCaptured reports that the call's bodies can be read (ADR-996).
	ContentCaptured bool `json:"content_captured"`
	// TraceID is the call's AI trace, and ToolCalls the tools its response
	// requested (ADR 20261020202035).
	TraceID   *string  `json:"trace_id,omitempty"`
	ToolCalls []string `json:"tool_calls,omitempty"`
	// QualityFlags mark what went wrong with the answer; Feedback is the
	// call's rating with its label; EvaluationScore is its online
	// evaluation's score from 1 to 5 (ADR 20261021171712).
	QualityFlags    []string `json:"quality_flags,omitempty"`
	Feedback        *string  `json:"feedback,omitempty"`
	FeedbackLabel   *string  `json:"feedback_label,omitempty"`
	EvaluationScore *int32   `json:"evaluation_score,omitempty"`
}

// Encodings of a captured body: text that is valid UTF-8, or base64.
const (
	OutboundAIContentEncodingUTF8   = "utf-8"
	OutboundAIContentEncodingBase64 = "base64"
)

// OutboundAIContentBody is the start of one captured body. Truncated
// reports that the body was longer than the capture keeps, or that the
// gateway streamed it without buffering.
type OutboundAIContentBody struct {
	Body        string  `json:"body"`
	Encoding    string  `json:"encoding"`
	Truncated   bool    `json:"truncated"`
	ContentType *string `json:"content_type"`
}

// OutboundAIRequestContent is one logged call's captured bodies as the
// gateway sent them to the provider and read them back, never headers
// (ADR-996).
type OutboundAIRequestContent struct {
	RequestID string    `json:"request_id"`
	CreatedAt time.Time `json:"created_at"`
	// RequestPath is the call's provider path; null for captures made
	// before ADR-997.
	RequestPath *string               `json:"request_path"`
	Request     OutboundAIContentBody `json:"request"`
	Response    OutboundAIContentBody `json:"response"`
}

// Per-end-user attribution and key affinity headers (ADR-994). An app sets
// them on calls through the gateway; they never reach the provider.
const (
	OutboundAIUserHeader    = "X-Gregale-AI-User"
	OutboundAISessionHeader = "X-Gregale-AI-Session"
	// OutboundAIUserOverflow aggregates end users beyond the per-day cap.
	// No header value can name it.
	OutboundAIUserOverflow = "~other"
)

// ValidOutboundAIUserID accepts 1 to 64 letters, digits and . _ : @ + = / |
// -, enough for opaque IDs, UUIDs, hashes and e-mail-like keys. The same
// rule bounds the session header.
func ValidOutboundAIUserID(id string) bool {
	if id == "" || len(id) > OutboundAIUserIDMaxBytes {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.IndexByte("._:@+=/|-", c) >= 0 {
			continue
		}
		return false
	}
	return true
}

// ValidOutboundAIUserLimits accepts optional per-end-user daily token and
// cost limits; either requires an AI provider (ADR-994).
func ValidOutboundAIUserLimits(provider string, tokens, cost *int64) bool {
	if tokens != nil && (provider == "" || *tokens < 1 || *tokens > MaxOutboundAITokensPerDay) {
		return false
	}
	return cost == nil || (provider != "" && *cost >= 1 && *cost <= MaxOutboundAICostMillicents)
}

// OutboundAIUserUsageItem is one end user's UTC-day usage on an
// integration. Tokens are input plus output, the amount the per-user token
// limit counts.
type OutboundAIUserUsageItem struct {
	EndUser        string `json:"end_user"`
	Requests       int64  `json:"requests"`
	ErrorRequests  int64  `json:"error_requests"`
	InputTokens    int64  `json:"input_tokens"`
	OutputTokens   int64  `json:"output_tokens"`
	Tokens         int64  `json:"tokens"`
	CostMillicents int64  `json:"cost_millicents"`
}

// OutboundAIUserUsageResponse lists one UTC day's end users by cost, then
// tokens (ADR-994). TrackedUsers counts the day's distinct end users,
// including the "~other" overflow row.
type OutboundAIUserUsageResponse struct {
	UsageDate                    string                    `json:"usage_date"`
	UserDailyTokenLimit          *int64                    `json:"user_daily_token_limit"`
	UserDailyCostLimitMillicents *int64                    `json:"user_daily_cost_limit_millicents"`
	TrackedUsers                 int64                     `json:"tracked_users"`
	Items                        []OutboundAIUserUsageItem `json:"items"`
}

// OutboundAIUserUsageExportRow is one NDJSON line of a per-end-user usage
// export (ADR-995); the CSV export has the same columns in this order.
type OutboundAIUserUsageExportRow struct {
	UsageDate string `json:"usage_date"`
	OutboundAIUserUsageItem
}

// OutboundAIUserUsageExportColumns names the CSV export's header row.
var OutboundAIUserUsageExportColumns = []string{"usage_date", "end_user", "requests", "error_requests",
	"input_tokens", "output_tokens", "tokens", "cost_millicents"}

// OutboundAIRequestLogList is one newest-first page. NextBefore, when set,
// is the created_at cursor for the next page.
type OutboundAIRequestLogList struct {
	Items      []OutboundAIRequestLogEntry `json:"items"`
	NextBefore *time.Time                  `json:"next_before"`
}

// Replay states (ADR-997).
const (
	OutboundAIReplayPending = "pending"
	OutboundAIReplayRunning = "running"
	OutboundAIReplayDone    = "done"
	OutboundAIReplayFailed  = "failed"
)

// OutboundAIReplayTarget is one integration a captured call is replayed on,
// with an optional model. A target whose provider differs from the
// captured call's needs a model.
type OutboundAIReplayTarget struct {
	IntegrationID string `json:"integration_id"`
	Model         string `json:"model,omitempty"`
}

// CreateOutboundAIReplayRequest replays a captured call on up to
// OutboundAIReplayTargetsMax targets (ADR-997).
type CreateOutboundAIReplayRequest struct {
	Targets []OutboundAIReplayTarget `json:"targets"`
}

// Valid checks the target count, IDs and model names.
func (r CreateOutboundAIReplayRequest) Valid() bool {
	if len(r.Targets) == 0 || len(r.Targets) > OutboundAIReplayTargetsMax {
		return false
	}
	for _, target := range r.Targets {
		if _, err := uuid.Parse(target.IntegrationID); err != nil {
			return false
		}
		if target.Model != "" && !validAIModelText(target.Model) {
			return false
		}
	}
	return true
}

// OutboundAIReplaySource summarizes the captured call a replay resends.
// OutputText is the text read from its captured response, when any.
type OutboundAIReplaySource struct {
	Model             string  `json:"model"`
	Status            int     `json:"status"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CostMillicents    int64   `json:"cost_millicents"`
	DurationMS        int64   `json:"duration_ms"`
	TimeToFirstByteMS *int64  `json:"ttfb_ms"`
	OutputText        *string `json:"output_text"`
}

// OutboundAIReplayResult is one target's replayed call. Status is null when
// the gateway sent no response, Outcome null when the call was refused
// before it reached the provider, and ErrorCode the problem code of a
// refused or failed call.
type OutboundAIReplayResult struct {
	IntegrationID     string                `json:"integration_id"`
	Model             *string               `json:"model"`
	Status            *int                  `json:"status"`
	Outcome           *string               `json:"outcome"`
	ErrorCode         *string               `json:"error_code"`
	InputTokens       int64                 `json:"input_tokens"`
	OutputTokens      int64                 `json:"output_tokens"`
	CostMillicents    int64                 `json:"cost_millicents"`
	DurationMS        int64                 `json:"duration_ms"`
	TimeToFirstByteMS *int64                `json:"ttfb_ms"`
	OutputText        string                `json:"output_text"`
	Response          OutboundAIContentBody `json:"response"`
}

// OutboundAIReplay is a replay of one captured call and, once done, its
// results in target order (ADR-997). Replayed calls settle under the
// "replay" caller and count against the targets' budgets.
type OutboundAIReplay struct {
	ID            string                   `json:"id"`
	IntegrationID string                   `json:"integration_id"`
	RequestID     string                   `json:"request_id"`
	State         string                   `json:"state"`
	ErrorCode     *string                  `json:"error_code"`
	CreatedAt     time.Time                `json:"created_at"`
	FinishedAt    *time.Time               `json:"finished_at"`
	Targets       []OutboundAIReplayTarget `json:"targets"`
	Source        OutboundAIReplaySource   `json:"source"`
	Results       []OutboundAIReplayResult `json:"results"`
}

// ErrOutboundAIReplayLimit is the problem for an integration that reached
// its replays for the UTC day.
func ErrOutboundAIReplayLimit(observed int64) *Problem {
	return NewProblem(http.StatusTooManyRequests, "outbound_ai_replay_limit", "AI replay limit reached",
		"An integration can replay at most 100 captured calls per UTC day.").
		WithLimit(OutboundAIReplaysPerDay, observed).
		WithDocs(docsBase + "/cli#outbound")
}
