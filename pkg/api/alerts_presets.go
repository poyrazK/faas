// Package api — alert preset DTOs (issue #1233, ADR-123).
//
// Catalog rows are system-owned and customer-visible (SELECT-only).
// Customers POST to the enable endpoint with their webhook URL +
// secret; the handler pre-fills the (metric, comparison, threshold,
// window_spec) quadruple from the catalog and reuses the
// createAlertRule path. No persistent preset_id FK lands on
// alert_rules — instantiation is a one-shot clone.
//
// Naming mirrors pkg/api/alerts.go so the CLI and the dashboard
// render the same JSON tags verbatim. The closed sets
// (category / minimum_plan) live as plain-string slices here so
// pkg/api stays free of the pkg/state import cycle (same
// precedent as AllowedAlertRuleMetrics at pkg/api/alerts.go:66-74).
package api

// AllowedAlertPresetCategories is the closed set for the `category`
// field on alert_presets. Mirrors the alert_presets_category_chk DB
// constraint (migrations/00347_alert_presets.sql). Used by the
// dashboard grid header to bucket the catalog cards.
var AllowedAlertPresetCategories = []string{
	"availability",
	"reliability",
	"cost",
	"deployment",
	"infrastructure",
}

// AllowedAlertPresetMinimumPlans is the closed set for the
// `minimum_plan` field. Mirrors the alert_presets_plan_chk DB
// constraint. Free = 0 is excluded from any preset's
// minimum_plan — the alert-rules engine has a Free-plan gate at
// cmd/apid/handlers_alerts.go:102-105 that returns 402 before any
// preset is consulted. Hobby is the floor for every shipped
// preset.
var AllowedAlertPresetMinimumPlans = []string{
	"free",
	"hobby",
	"pro",
	"scale",
}

// AlertPresetCategory returns true iff c is in
// AllowedAlertPresetCategories.
func AlertPresetCategory(c string) bool {
	return containsString(AllowedAlertPresetCategories, c)
}

// AlertPresetMinimumPlan returns true iff p is in
// AllowedAlertPresetMinimumPlans.
func AlertPresetMinimumPlan(p string) bool {
	return containsString(AllowedAlertPresetMinimumPlans, p)
}

// AlertPresetResponse is the GET /v1/alert-presets wire shape.
// Returned as a flat slice (no pagination — the catalog is bounded).
// The handler maps from the pgstore.CorsPreset-shaped row at the
// pkg/api ↔ pkg/state boundary so neither side imports the other
// (precedent: AlertRuleResponseFromRow at alerts.go:205).
type AlertPresetResponse struct {
	ID                     string  `json:"id"`
	Name                   string  `json:"name"`
	DisplayName            string  `json:"display_name"`
	Description            string  `json:"description"`
	Category               string  `json:"category"`
	Metric                 string  `json:"metric"`
	Comparison             string  `json:"comparison"`
	Threshold              float64 `json:"threshold"`
	WindowSpec             string  `json:"window_spec"`
	DefaultCooldownMinutes int     `json:"default_cooldown_minutes"`
	MinimumPlan            string  `json:"minimum_plan"`
	EnabledInCatalog       bool    `json:"enabled_in_catalog"`
}

// AlertPresetRow is the closed-set-typed counterpart of
// AlertPresetResponse. The handler converts at the pkg/api ↔
// pkg/state boundary so pkg/api stays free of pkg/state.
type AlertPresetRow struct {
	ID                     string
	Name                   string
	DisplayName            string
	Description            string
	Category               string
	Metric                 string
	Comparison             string
	Threshold              float64
	WindowSpec             string
	DefaultCooldownMinutes int
	MinimumPlan            string
	EnabledInCatalog       bool
}

// AlertPresetResponseFromRow maps a row to the response DTO. Used
// at the pkg/api ↔ pkg/state boundary. Staticcheck S1016 allows the
// struct conversion because AlertPresetRow and AlertPresetResponse
// share identical field names + types in the same order — keeping
// the wire-jSON tags on the response side while letting the row
// stay untagged.
func AlertPresetResponseFromRow(r AlertPresetRow) AlertPresetResponse {
	return AlertPresetResponse(r)
}

// EnableAlertPresetRequest is the POST /v1/apps/{slug}/alert-presets/{name}/enable
// body. The handler pre-fills (name, metric, comparison, threshold,
// window_spec, default_cooldown_minutes) from the catalog row; the
// customer supplies only the webhook delivery side.
//
// CooldownMinutes is optional and overrides the catalog default
// (alert_presets.default_cooldown_minutes). When nil, the catalog
// default wins.
//
// Enabled is optional and defaults to true. When false, the
// instantiated rule is created in disabled state so a customer
// can stage multiple presets before enabling them.
type EnableAlertPresetRequest struct {
	WebhookURL      string `json:"webhook_url"`
	WebhookSecret   string `json:"webhook_secret"`
	CooldownMinutes *int   `json:"cooldown_minutes,omitempty"`
	Enabled         *bool  `json:"enabled,omitempty"`
}

// TestAlertPresetResponse is the body returned by POST
// /v1/apps/{slug}/alert-presets/{name}/test (issue #1233 /
// ADR-123 PR-C commit 2). Status is always "sent" on a 200;
// "sent" is a literal string rather than a typed enum because
// the SDK + dashboard both treat any 2xx as success and ignore
// the value (the failure path is the 502 Problem, not a
// non-2xx-with-sent-false).
//
// DeliveryID is the synthetic UUID the dispatcher stamped on
// the test Event — preserved here so the customer's audit-log
// join can correlate their receiver's log entry with our audit
// row (alert_preset.test_sent).
//
// Attempts is the dispatcher's retry-loop attempt count; 1 means
// the first attempt succeeded, 5 means every retry was exhausted
// (in which case the handler returns 502 — this field stays at 5
// as a debugging breadcrumb in the audit row regardless).
type TestAlertPresetResponse struct {
	Status     string `json:"status"`      // always "sent" on 200
	Test       bool   `json:"test"`        // always true on 200
	DeliveryID string `json:"delivery_id"` // 32-char hex UUID
	Attempts   int    `json:"attempts"`    // 1..MaxAttempts
}
