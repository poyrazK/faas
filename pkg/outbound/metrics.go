package outbound

import (
	"errors"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
)

// Metrics is the bounded observability surface for outboundd. Operator IDs
// are configuration-owned; customer-created integrations share a fixed label
// so customer growth cannot create unbounded metric cardinality.
type Metrics struct {
	admissions             *prometheus.CounterVec
	rejections             *prometheus.CounterVec
	inFlight               *prometheus.GaugeVec
	upstreamRequests       *prometheus.CounterVec
	upstreamLatency        *prometheus.HistogramVec
	cacheRequests          *prometheus.CounterVec
	circuitEvents          *prometheus.CounterVec
	retryBudgetEvents      *prometheus.CounterVec
	providerCooldownEvents *prometheus.CounterVec
	aiRequests             *prometheus.CounterVec
	aiTokens               *prometheus.CounterVec
	aiTimeToFirstByte      *prometheus.HistogramVec
	aiUsageRecordEvents    *prometheus.CounterVec
	relayConnections       *prometheus.CounterVec
	aiCost                 *prometheus.CounterVec
	aiRouteFallbacks       *prometheus.CounterVec
	aiTranslations         *prometheus.CounterVec
	aiBudgetAlerts         *prometheus.CounterVec
	credentialKeyEvents    *prometheus.CounterVec
	aiContentFindings      *prometheus.CounterVec
	aiMedia                *prometheus.CounterVec
	aiReplays              *prometheus.CounterVec
	aiEvaluations          *prometheus.CounterVec
	aiBatches              *prometheus.CounterVec
	aiUnmetered            *prometheus.CounterVec
	aiToolUses             *prometheus.CounterVec
	aiRouteCalls           *prometheus.CounterVec
	aiCanaryCalls          *prometheus.CounterVec
	aiCanaryRollbacks      *prometheus.CounterVec
	aiModeration           *prometheus.CounterVec
	aiLogExports           *prometheus.CounterVec
	aiQualityFlags         *prometheus.CounterVec
	aiFeedback             *prometheus.CounterVec
	aiOnlineEvaluations    *prometheus.CounterVec
	aiResponseChecks       *prometheus.CounterVec
	aiRealtimeSessions     *prometheus.CounterVec
	aiPromptPrefixShared   *prometheus.CounterVec
	aiRealtimeActive       prometheus.Gauge
	aiResponseFindings     *prometheus.CounterVec
	aiPromptCacheMarks     *prometheus.CounterVec
	aiResponseCacheSaved   *prometheus.CounterVec
}

// NewMetrics registers the outbound gateway metric families against reg.
// Callers normally pass the daemon's private OpsMetrics registry so the
// metrics are exposed on the existing operator-only /metrics listener.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	if reg == nil {
		return nil, errors.New("outbound: nil prometheus registerer")
	}
	m := &Metrics{
		admissions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_admissions_total",
			Help: "Outbound gateway admission attempts by integration and outcome (granted, rejected, or error).",
		}, []string{"integration_id", "outcome"}),
		rejections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_rejections_total",
			Help: "Outbound gateway requests rejected by integration and bounded reason.",
		}, []string{"integration_id", "reason"}),
		inFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "outbound_in_flight",
			Help: "Outbound requests currently admitted by this gateway process, by integration.",
		}, []string{"integration_id"}),
		upstreamRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_upstream_requests_total",
			Help: "Outbound provider request outcomes by integration and HTTP status class.",
		}, []string{"integration_id", "outcome"}),
		upstreamLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "outbound_upstream_latency_seconds",
			Help:    "Outbound provider request latency by integration.",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		}, []string{"integration_id"}),
		cacheRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_response_cache_requests_total",
			Help: "Outbound response-cache lookups by integration and outcome (hit or miss).",
		}, []string{"integration_id", "outcome"}),
		circuitEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_circuit_breaker_events_total",
			Help: "Outbound circuit-breaker checks and outcomes by integration and bounded event.",
		}, []string{"integration_id", "event"}),
		retryBudgetEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_retry_budget_events_total",
			Help: "Shared outbound retry-budget checks by integration and bounded event.",
		}, []string{"integration_id", "event"}),
		providerCooldownEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_provider_cooldown_events_total",
			Help: "Shared provider-directed outbound cooldown checks by integration and bounded event.",
		}, []string{"integration_id", "event"}),
		aiRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_requests_total",
			Help: "Settled AI gateway calls by integration and usage outcome (known, unknown, or error).",
		}, []string{"integration_id", "outcome"}),
		aiTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_tokens_total",
			Help: "Provider-reported AI tokens by integration and kind (input, cached_input, cache_write, or output). Input includes cached and cache-write tokens.",
		}, []string{"integration_id", "kind"}),
		aiTimeToFirstByte: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "outbound_ai_time_to_first_byte_seconds",
			Help:    "Time from the AI provider request to the first response body byte, by integration.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2, 4, 8, 15, 30, 60, 120},
		}, []string{"integration_id"}),
		aiUsageRecordEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_usage_record_events_total",
			Help: "AI usage settlement writes by integration and bounded event (recorded, retried, state_error after the last attempt, or unavailable).",
		}, []string{"integration_id", "event"}),
		relayConnections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_relay_connections_total",
			Help: "App relay connections from vmmd by bounded outcome (accepted, rejected, or overload).",
		}, []string{"outcome"}),
		aiCost: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_cost_millicents_total",
			Help: "Customer-priced AI cost settled by integration, in millicents rounded up per call.",
		}, []string{"integration_id"}),
		aiRouteFallbacks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_route_fallbacks_total",
			Help: "AI route calls that moved past a target, by bounded reason (rejected, provider_status, or transport).",
		}, []string{"reason"}),
		aiTranslations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_translations_total",
			Help: "OpenAI-format route attempts translated for another provider, by target provider and outcome (translated or unsupported).",
		}, []string{"provider", "outcome"}),
		aiBudgetAlerts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_budget_alerts_total",
			Help: "AI budget threshold alerts recorded at settlement, by budget and threshold percent.",
		}, []string{"budget", "threshold"}),
		credentialKeyEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_credential_key_events_total",
			Help: "Provider key pool events by integration and bounded event (cooldown, rotated, or exhausted).",
		}, []string{"integration_id", "event"}),
		aiContentFindings: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_content_findings_total",
			Help: "AI content policy findings by integration, detector (email, payment_card, iban, secret, or blocked_term) and action (redacted or blocked).",
		}, []string{"integration_id", "kind", "action"}),
		aiMedia: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_media_total",
			Help: "Settled AI media usage by integration and kind (images, audio_seconds, characters, or video_seconds) (ADR-997, ADR-1432).",
		}, []string{"integration_id", "kind"}),
		aiReplays: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_replays_total",
			Help: "AI replays the worker finished, by state (done or failed) (ADR-997).",
		}, []string{"state"}),
		aiEvaluations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_evaluations_total",
			Help: "AI evaluations the worker finished, by result (pass, fail, or failed) (ADR-1431).",
		}, []string{"result"}),
		aiBatches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_batches_settled_total",
			Help: "Provider batches the worker settled, by outcome (known, unknown, or error) (ADR-1431).",
		}, []string{"outcome"}),
		aiUnmetered: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_unmetered_calls_total",
			Help: "Writes on AI integrations let through unmetered because the AI policy allows them, by integration (ADR-1432).",
		}, []string{"integration_id"}),
		aiToolUses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_tool_uses_total",
			Help: "Settled built-in tool uses by integration and tool (web_searches, file_searches, or code_session_gb) (ADR-1433).",
		}, []string{"integration_id", "tool"}),
		aiRouteCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_route_calls_total",
			Help: "AI route calls by strategy and how the first target was picked (ordered, weighted, best, explore, or canary) (ADR-1434).",
		}, []string{"strategy", "pick"}),
		aiCanaryCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_route_canary_calls_total",
			Help: "Calls of routes with an active canary, by the target tried first (canary or baseline) (ADR-1340).",
		}, []string{"target"}),
		aiCanaryRollbacks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_route_canary_rollbacks_total",
			Help: "Model canaries outboundd rolled back, by reason (error_rate or latency) (ADR-1340).",
		}, []string{"reason"}),
		aiResponseCacheSaved: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_response_cache_saved_millicents_total",
			Help: "Provider cost the response cache avoided, priced from cached responses' usage, by integration (ADR-1440).",
		}, []string{"integration_id"}),
		aiPromptCacheMarks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_prompt_cache_marks_total",
			Help: "Prompt-cache breakpoints the gateway added to Claude requests by integration and mark (prefix or conversation) (ADR-1437).",
		}, []string{"integration_id", "mark"}),
		aiResponseChecks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_response_checks_total",
			Help: "AI response checks by integration and result (passed, redacted, flagged, error, unchecked, or blocked) (ADR-1435).",
		}, []string{"integration_id", "result"}),
		aiResponseFindings: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_response_findings_total",
			Help: "AI response check findings by integration, detector (email, payment_card, iban, secret, or blocked_term) and action (redacted or blocked) (ADR-1435).",
		}, []string{"integration_id", "kind", "action"}),
		aiModeration: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_moderation_total",
			Help: "AI moderation checks by integration and result (passed, flagged, blocked, or error) (ADR-1340).",
		}, []string{"integration_id", "result"}),
		aiLogExports: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_log_exports_total",
			Help: "Settled AI calls written to the log-drain export outbox, by integration (ADR-1340).",
		}, []string{"integration_id"}),
		aiQualityFlags: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_quality_flags_total",
			Help: "Settled AI calls by integration and quality flag: truncated, refused, empty, invalid_json or tool_error (ADR 20261021171712).",
		}, []string{"integration_id", "flag"}),
		aiFeedback: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_feedback_total",
			Help: "Ratings apps stored for their AI calls, by integration and rating (ADR 20261021171712).",
		}, []string{"integration_id", "rating"}),
		aiOnlineEvaluations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_online_evaluations_total",
			Help: "Online evaluations by result: scored or judge_error (ADR 20261021171712).",
		}, []string{"result"}),
		aiRealtimeSessions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_realtime_sessions_total",
			Help: "AI realtime sessions by event: opened, unmetered, and closed by the client, the provider, a cap, the duration bound, unreadable usage or an error (ADR 20261019072722).",
		}, []string{"event"}),
		aiPromptPrefixShared: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_ai_prompt_cache_shared_total",
			Help: "Shared prompt-prefix lookups by integration and result: repeated, first, refreshed or error (ADR 20261019132139).",
		}, []string{"integration_id", "result"}),
		aiRealtimeActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "outbound_ai_realtime_sessions_active",
			Help: "AI realtime sessions open on this outboundd (ADR 20261019072722).",
		}),
	}
	// The alert rules read these by reason or outcome alone. A series that
	// starts at zero lets increase() see a process's first event.
	for _, reason := range []string{api.OutboundAIRouteCanaryReasonErrorRate, api.OutboundAIRouteCanaryReasonLatency} {
		m.aiCanaryRollbacks.WithLabelValues(reason)
	}
	for _, outcome := range []AIUsageOutcome{AIUsageOutcomeKnown, AIUsageOutcomeUnknown, AIUsageOutcomeError} {
		m.aiBatches.WithLabelValues(string(outcome))
	}
	for _, event := range aiRealtimeSessionEvents {
		m.aiRealtimeSessions.WithLabelValues(event)
	}
	collectors := []prometheus.Collector{
		m.admissions,
		m.rejections,
		m.inFlight,
		m.upstreamRequests,
		m.upstreamLatency,
		m.cacheRequests,
		m.circuitEvents,
		m.retryBudgetEvents,
		m.providerCooldownEvents,
		m.aiRequests,
		m.aiTokens,
		m.aiTimeToFirstByte,
		m.aiUsageRecordEvents,
		m.relayConnections,
		m.aiCost,
		m.aiRouteFallbacks,
		m.aiTranslations,
		m.aiBudgetAlerts,
		m.credentialKeyEvents,
		m.aiContentFindings,
		m.aiMedia,
		m.aiReplays,
		m.aiEvaluations,
		m.aiBatches,
		m.aiUnmetered,
		m.aiToolUses,
		m.aiRouteCalls,
		m.aiCanaryCalls,
		m.aiCanaryRollbacks,
		m.aiModeration,
		m.aiLogExports,
		m.aiQualityFlags,
		m.aiFeedback,
		m.aiOnlineEvaluations,
		m.aiResponseChecks,
		m.aiResponseFindings,
		m.aiPromptCacheMarks,
		m.aiResponseCacheSaved,
		m.aiRealtimeSessions,
		m.aiRealtimeActive,
		m.aiPromptPrefixShared,
	}
	for _, collector := range collectors {
		if err := reg.Register(collector); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Metrics) ObserveAdmission(integrationID, outcome string) {
	if m == nil || m.admissions == nil {
		return
	}
	m.admissions.WithLabelValues(integrationID, outcome).Inc()
}

func (m *Metrics) ObserveRejection(integrationID, reason string) {
	if m == nil || m.rejections == nil {
		return
	}
	m.rejections.WithLabelValues(integrationID, reason).Inc()
}

func (m *Metrics) IncInFlight(integrationID string) {
	if m == nil || m.inFlight == nil {
		return
	}
	m.inFlight.WithLabelValues(integrationID).Inc()
}

func (m *Metrics) DecInFlight(integrationID string) {
	if m == nil || m.inFlight == nil {
		return
	}
	m.inFlight.WithLabelValues(integrationID).Dec()
}

func (m *Metrics) ObserveUpstream(integrationID string, status int, latency time.Duration) {
	if m == nil {
		return
	}
	outcome := "other"
	switch {
	case status >= 200 && status < 300:
		outcome = "2xx"
	case status >= 300 && status < 400:
		outcome = "3xx"
	case status >= 400 && status < 500:
		outcome = "4xx"
	case status >= 500 && status < 600:
		outcome = "5xx"
	}
	m.upstreamRequests.WithLabelValues(integrationID, outcome).Inc()
	if latency >= 0 {
		m.upstreamLatency.WithLabelValues(integrationID).Observe(latency.Seconds())
	}
}

func (m *Metrics) ObserveUpstreamError(integrationID string, latency time.Duration) {
	if m == nil {
		return
	}
	m.upstreamRequests.WithLabelValues(integrationID, "error").Inc()
	if latency >= 0 {
		m.upstreamLatency.WithLabelValues(integrationID).Observe(latency.Seconds())
	}
}

func (m *Metrics) ObserveCache(integrationID, outcome string) {
	if m == nil || m.cacheRequests == nil {
		return
	}
	m.cacheRequests.WithLabelValues(integrationID, outcome).Inc()
}

func (m *Metrics) ObserveCircuit(integrationID, event string) {
	if m == nil || m.circuitEvents == nil {
		return
	}
	m.circuitEvents.WithLabelValues(integrationID, event).Inc()
}

func (m *Metrics) ObserveRetryBudget(integrationID, event string) {
	if m == nil || m.retryBudgetEvents == nil {
		return
	}
	m.retryBudgetEvents.WithLabelValues(integrationID, event).Inc()
}

func (m *Metrics) ObserveProviderCooldown(integrationID, event string) {
	if m == nil || m.providerCooldownEvents == nil {
		return
	}
	m.providerCooldownEvents.WithLabelValues(integrationID, event).Inc()
}

// ObserveAIUsage records one settled AI call. Model names are deliberately
// not labels: they are provider-controlled and would be unbounded.
func (m *Metrics) ObserveAIUsage(integrationID string, outcome AIUsageOutcome, usage AIUsage, timeToFirstByte time.Duration) {
	if m == nil || m.aiRequests == nil {
		return
	}
	m.aiRequests.WithLabelValues(integrationID, string(outcome)).Inc()
	if timeToFirstByte > 0 {
		m.aiTimeToFirstByte.WithLabelValues(integrationID).Observe(timeToFirstByte.Seconds())
	}
	if outcome != AIUsageOutcomeKnown {
		return
	}
	for kind, value := range map[string]int64{
		"input": usage.InputTokens, "cached_input": usage.CachedInputTokens,
		"cache_write": usage.CacheWriteTokens, "output": usage.OutputTokens,
	} {
		if value > 0 {
			m.aiTokens.WithLabelValues(integrationID, kind).Add(float64(value))
		}
	}
	if m.aiMedia == nil {
		return
	}
	for kind, value := range map[string]int64{"images": usage.Images, "audio_seconds": usage.AudioSeconds, "characters": usage.Characters,
		"video_seconds": usage.VideoSeconds} {
		if value > 0 {
			m.aiMedia.WithLabelValues(integrationID, kind).Add(float64(value))
		}
	}
	if m.aiToolUses == nil {
		return
	}
	for tool, value := range map[string]int64{"web_searches": usage.WebSearches, "file_searches": usage.FileSearches,
		"code_session_gb": usage.CodeSessionGB} {
		if value > 0 {
			m.aiToolUses.WithLabelValues(integrationID, tool).Add(float64(value))
		}
	}
}

// ObserveAIReplay counts one finished replay.
func (m *Metrics) ObserveAIReplay(state string) {
	if m == nil || m.aiReplays == nil {
		return
	}
	m.aiReplays.WithLabelValues(state).Inc()
}

// ObserveAIEvaluation counts one finished evaluation.
func (m *Metrics) ObserveAIEvaluation(result string) {
	if m == nil || m.aiEvaluations == nil {
		return
	}
	m.aiEvaluations.WithLabelValues(result).Inc()
}

// ObserveAIUnmetered counts one write let through unmetered.
func (m *Metrics) ObserveAIUnmetered(integrationID string) {
	if m == nil || m.aiUnmetered == nil {
		return
	}
	m.aiUnmetered.WithLabelValues(integrationID).Inc()
}

// ObserveAIBatchSettled counts one settled provider batch.
func (m *Metrics) ObserveAIBatchSettled(outcome AIUsageOutcome) {
	if m == nil || m.aiBatches == nil {
		return
	}
	m.aiBatches.WithLabelValues(string(outcome)).Inc()
}

func (m *Metrics) ObserveAICost(integrationID string, millicents int64) {
	if m == nil || m.aiCost == nil || millicents <= 0 {
		return
	}
	m.aiCost.WithLabelValues(integrationID).Add(float64(millicents))
}

// ObserveAIRouteCall counts one route call by its strategy and how its
// first target was picked.
func (m *Metrics) ObserveAIRouteCall(strategy, pick string) {
	if m == nil || m.aiRouteCalls == nil {
		return
	}
	if strategy == "" {
		strategy = api.OutboundAIRouteStrategyOrdered
	}
	m.aiRouteCalls.WithLabelValues(strategy, pick).Inc()
}

// ObserveAIRouteCanaryCall counts a call of a route with an active canary
// by the target it tried first.
func (m *Metrics) ObserveAIRouteCanaryCall(canary bool) {
	if m == nil || m.aiCanaryCalls == nil {
		return
	}
	target := "baseline"
	if canary {
		target = "canary"
	}
	m.aiCanaryCalls.WithLabelValues(target).Inc()
}

// ObserveAIRealtimeSession counts one realtime session event
// (ADR 20261019072722).
func (m *Metrics) ObserveAIRealtimeSession(event string) {
	if m == nil || m.aiRealtimeSessions == nil {
		return
	}
	m.aiRealtimeSessions.WithLabelValues(event).Inc()
}

// ObserveAIPromptPrefixShared counts one shared prompt-prefix lookup
// (ADR 20261019132139).
func (m *Metrics) ObserveAIPromptPrefixShared(integrationID, result string) {
	if m == nil || m.aiPromptPrefixShared == nil {
		return
	}
	m.aiPromptPrefixShared.WithLabelValues(integrationID, result).Inc()
}

// AddAIRealtimeActive moves the open realtime session gauge.
func (m *Metrics) AddAIRealtimeActive(delta float64) {
	if m == nil || m.aiRealtimeActive == nil {
		return
	}
	m.aiRealtimeActive.Add(delta)
}

func (m *Metrics) ObserveAIRouteCanaryRollback(reason string) {
	if m == nil || m.aiCanaryRollbacks == nil {
		return
	}
	m.aiCanaryRollbacks.WithLabelValues(reason).Inc()
}

// ObserveAIResponseCacheSaved counts the provider cost one cache hit
// avoided (ADR-1440).
func (m *Metrics) ObserveAIResponseCacheSaved(integrationID string, millicents int64) {
	if m == nil || m.aiResponseCacheSaved == nil || millicents <= 0 {
		return
	}
	m.aiResponseCacheSaved.WithLabelValues(integrationID).Add(float64(millicents))
}

// ObserveAIPromptCacheMark counts one breakpoint the gateway added
// (ADR-1437).
func (m *Metrics) ObserveAIPromptCacheMark(integrationID, mark string) {
	if m == nil || m.aiPromptCacheMarks == nil {
		return
	}
	m.aiPromptCacheMarks.WithLabelValues(integrationID, mark).Inc()
}

// ObserveAIResponseCheck counts one response check's result (ADR-1435).
func (m *Metrics) ObserveAIResponseCheck(integrationID, result string) {
	if m == nil || m.aiResponseChecks == nil {
		return
	}
	m.aiResponseChecks.WithLabelValues(integrationID, result).Inc()
}

// ObserveAIResponseFindings counts a response check's findings of one kind.
func (m *Metrics) ObserveAIResponseFindings(integrationID, kind, action string, count int) {
	if m == nil || m.aiResponseFindings == nil || count <= 0 {
		return
	}
	m.aiResponseFindings.WithLabelValues(integrationID, kind, action).Add(float64(count))
}

func (m *Metrics) ObserveAIModeration(integrationID, result string) {
	if m == nil || m.aiModeration == nil {
		return
	}
	m.aiModeration.WithLabelValues(integrationID, result).Inc()
}

func (m *Metrics) ObserveAILogExport(integrationID string) {
	if m == nil || m.aiLogExports == nil {
		return
	}
	m.aiLogExports.WithLabelValues(integrationID).Inc()
}

// ObserveAIQualityFlag counts one settled call's quality flag
// (ADR 20261021171712).
func (m *Metrics) ObserveAIQualityFlag(integrationID, flag string) {
	if m == nil || m.aiQualityFlags == nil {
		return
	}
	m.aiQualityFlags.WithLabelValues(integrationID, flag).Inc()
}

// ObserveAIFeedback counts one stored rating (ADR 20261021171712).
func (m *Metrics) ObserveAIFeedback(integrationID, rating string) {
	if m == nil || m.aiFeedback == nil {
		return
	}
	m.aiFeedback.WithLabelValues(integrationID, rating).Inc()
}

// ObserveAIOnlineEvaluation counts one finished online evaluation; a zero
// score is a judge error (ADR 20261021171712).
func (m *Metrics) ObserveAIOnlineEvaluation(score int) {
	if m == nil || m.aiOnlineEvaluations == nil {
		return
	}
	result := "scored"
	if score == 0 {
		result = "judge_error"
	}
	m.aiOnlineEvaluations.WithLabelValues(result).Inc()
}

func (m *Metrics) ObserveAIRouteFallback(reason string) {
	if m == nil || m.aiRouteFallbacks == nil {
		return
	}
	m.aiRouteFallbacks.WithLabelValues(reason).Inc()
}

func (m *Metrics) ObserveAITranslation(provider, outcome string) {
	if m == nil || m.aiTranslations == nil {
		return
	}
	m.aiTranslations.WithLabelValues(provider, outcome).Inc()
}

func (m *Metrics) ObserveAIBudgetAlert(budget string, thresholdPercent int) {
	if m == nil || m.aiBudgetAlerts == nil {
		return
	}
	m.aiBudgetAlerts.WithLabelValues(budget, strconv.Itoa(thresholdPercent)).Inc()
}

// ObserveAIContent counts one content policy finding (ADR-997).
func (m *Metrics) ObserveAIContent(integrationID, kind, action string) {
	if m == nil || m.aiContentFindings == nil {
		return
	}
	m.aiContentFindings.WithLabelValues(integrationID, kind, action).Inc()
}

func (m *Metrics) ObserveCredentialKey(integrationID, event string) {
	if m == nil || m.credentialKeyEvents == nil {
		return
	}
	m.credentialKeyEvents.WithLabelValues(integrationID, event).Inc()
}

func (m *Metrics) ObserveRelayConnection(outcome string) {
	if m == nil || m.relayConnections == nil {
		return
	}
	m.relayConnections.WithLabelValues(outcome).Inc()
}

func (m *Metrics) ObserveAIUsageRecord(integrationID, event string) {
	if m == nil || m.aiUsageRecordEvents == nil {
		return
	}
	m.aiUsageRecordEvents.WithLabelValues(integrationID, event).Inc()
}
