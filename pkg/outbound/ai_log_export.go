package outbound

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// aiLogExportSource names AI gateway lines in the app's log drains.
const aiLogExportSource = "ai_gateway"

// aiLogExportAttributesMax bounds the OTLP attributes of one line.
const aiLogExportAttributesMax = 8

// aiLogExportLine is the JSON line an exported call adds to the app's log
// drains (ADR-1340). It holds metadata only, never prompts or completions.
type aiLogExportLine struct {
	Source            string `json:"source"`
	IntegrationID     string `json:"integration_id"`
	Model             string `json:"model"`
	Status            int    `json:"status"`
	Outcome           string `json:"outcome"`
	InputTokens       int64  `json:"input_tokens"`
	CachedInputTokens int64  `json:"cached_input_tokens"`
	CacheWriteTokens  int64  `json:"cache_write_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
	CostMillicents    int64  `json:"cost_millicents"`
	DurationMS        int64  `json:"duration_ms"`
	TTFBMS            *int64 `json:"ttfb_ms,omitempty"`
	Attempts          int    `json:"attempts"`
	RouteID           string `json:"route_id,omitempty"`
	RouteTarget       int    `json:"route_target,omitempty"`
	Canary            bool   `json:"canary,omitempty"`
	EndUser           string `json:"end_user,omitempty"`
	// Tags are the call's usage tags by key (ADR-1436).
	Tags map[string]string `json:"tags,omitempty"`
	// TraceID is the call's AI trace and ToolCalls the tools its response
	// requested (ADR 20261020202035).
	TraceID              string   `json:"trace_id,omitempty"`
	ToolCalls            []string `json:"tool_calls,omitempty"`
	Moderation           string   `json:"moderation,omitempty"`
	ModerationCategories []string `json:"moderation_categories,omitempty"`
	// ADR-1435: the response check's result, what it found and the
	// categories its moderation flagged.
	ResponseCheck                string   `json:"response_check,omitempty"`
	ResponseFindings             []string `json:"response_findings,omitempty"`
	ResponseModerationCategories []string `json:"response_moderation_categories,omitempty"`
}

// aiLogExportFor builds an exported call's line, or nil when the
// integration does not export or the caller is not an app. Only an
// account's own integrations export: the line goes to the calling app's
// drains, which belong to the same account.
func aiLogExportFor(integration Integration, record AIUsageRecord, settlement aiSettlement, timeToFirstByte time.Duration, now time.Time) *AILogExport {
	if !integration.AILogExport || integration.OwnerKind != IntegrationOwnerCustomer || !appCaller(record.Caller) ||
		!validUUID(record.Caller) || !validUUID(integration.AccountID) {
		return nil
	}
	usage := record.Usage
	line := aiLogExportLine{
		Source: aiLogExportSource, IntegrationID: integration.ID, Model: usage.Model, Status: settlement.status,
		Outcome: string(record.Outcome), InputTokens: usage.InputTokens, CachedInputTokens: usage.CachedInputTokens,
		CacheWriteTokens: usage.CacheWriteTokens, OutputTokens: usage.OutputTokens, CostMillicents: record.CostMillicents,
		DurationMS: max(now.Sub(settlement.started).Milliseconds(), 0), Attempts: settlement.attempts,
		RouteID: settlement.route.routeID, EndUser: record.EndUser, TraceID: record.TraceID, ToolCalls: record.ToolCalls,
		Moderation: settlement.moderation.result, ModerationCategories: settlement.moderation.categories,
	}
	if settlement.route.routeID != "" {
		line.RouteTarget, line.Canary = settlement.route.position, settlement.route.canary
	}
	if check := settlement.response; check != nil && check.result != "" {
		line.ResponseCheck, line.ResponseFindings = check.result, check.findings()
		line.ResponseModerationCategories = check.moderation.categories
	}
	if timeToFirstByte > 0 {
		ttfb := timeToFirstByte.Milliseconds()
		line.TTFBMS = &ttfb
	}
	for _, tag := range record.Tags {
		key, value, _ := strings.Cut(tag, "=")
		if line.Tags == nil {
			line.Tags = make(map[string]string, len(record.Tags))
		}
		line.Tags[key] = value
	}
	encoded, err := json.Marshal(line)
	if err != nil {
		return nil
	}
	export := &AILogExport{
		AppID: record.Caller, AccountID: integration.AccountID, Line: string(encoded),
		Attributes: map[string]string{
			"gen_ai.system":              integration.AIProvider,
			"gen_ai.response.model":      usage.Model,
			"gen_ai.usage.input_tokens":  strconv.FormatInt(usage.InputTokens, 10),
			"gen_ai.usage.output_tokens": strconv.FormatInt(usage.OutputTokens, 10),
			"gregale.ai.outcome":         string(record.Outcome),
			"gregale.ai.integration_id":  integration.ID,
		},
	}
	if len(record.Tags) > 0 {
		export.Attributes["gregale.ai.tags"] = formatAITags(record.Tags)
	}
	if record.TraceID != "" {
		export.Attributes["gregale.ai.trace_id"] = record.TraceID
	}
	return export
}
