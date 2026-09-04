package api

// Alert-rule DTOs (issue #396, ADR-045). Plaintext webhook_secret only
// appears in CreateAlertRuleRequest and UpdateAlertRuleRequest; the
// response shape (AlertRuleResponse) carries a masked constant
// (AlertRuleWebhookSecretMasked) — same posture as pkg/api/secrets.go
// for app secrets.
//
// Naming mirrors the cron resource shape (pkg/api/dto.go:253-276) so
// the CLI (cmd/faas) and the dashboard can use the same JSON tags
// verbatim. UpdateAlertRuleRequest uses pointer-everything optionals
// for the partial-update pattern (mirrors UpdateCronRequest and
// state.UpdateAlertRuleParams).
//
// Closed-set vocabularies (metric / comparison / window_spec /
// failure_source / state) are typed as plain strings here so the DTO
// file can stay in pkg/api without dragging the pkg/state dependency
// into the cycle. The handler in cmd/apid validates each field against
// the corresponding state.* closed set and rejects drift with 400
// ErrAlertRuleInvalid. Mirrors the pattern at pkg/api/dto.go where
// CronResponse carries app_id as a plain string and the handler does
// the FK lookup.

import (
	"math"
	"strings"
	"time"
)

// AlertRuleWebhookSecretMaxBytes bounds the plaintext webhook_secret
// the customer may submit on create / update. 256 is generous: the
// underlying HMAC-SHA256 only needs 32 bytes, but customers paste
// longer strings from their secret managers and the rotate-secret
// endpoint mints a 32-byte value that base64-encodes to 44 bytes.
// 256 leaves headroom for the future "key=<base64>" encoding and
// rejects accidental megabyte uploads.
const AlertRuleWebhookSecretMaxBytes = 256

// AlertRuleWebhookSecretMasked is the literal returned in every
// response shape that carries the webhook_secret field. Never echo
// the plaintext back to the customer — same posture as
// pkg/api/secrets.go (AppSecretResponse omits value).
const AlertRuleWebhookSecretMasked = "***"

// AlertRuleDefaultCooldownMinutes is the cooldown applied when the
// caller omits the field. 15 min matches the public spec (§4.4):
// short enough that an actual incident isn't missed, long enough that
// a flapping metric doesn't spam the customer's endpoint.
const AlertRuleDefaultCooldownMinutes = 15

// AlertRuleCooldownMinMinutes / AlertRuleCooldownMaxMinutes bound the
// closed band for cooldown_minutes on both create and update.
// Mirrors the DB CHECK constraint on alert_rules.cooldown_minutes.
const (
	AlertRuleCooldownMinMinutes = 5
	AlertRuleCooldownMaxMinutes = 1440
)

// AlertRuleNameMaxChars is the alert_rules.name column upper
// bound expressed in CHARACTERS (Unicode code points). Mirrors
// the alert_rules_name_len_chk DB constraint at
// migrations/00062_alert_rules.sql:84-86 (`char_length(name)
// between 1 and 64`). Used by enableAlertPreset to clamp the
// derived "<preset display_name> (<app slug>)" name so the
// catalog-side display_name can't blow past the DB cap. The
// enforcement is rune-aware (NOT byte-aware) so a multi-byte
// slug like "küche-app" doesn't get cut mid-codepoint — a
// naive `len(s) > 64; s = s[:64]` slice would land on an
// invalid-UTF-8 boundary that Postgres rejects with SQLSTATE
// 22021 at INSERT time.
const AlertRuleNameMaxChars = 64

// TruncateRunes returns s clipped to at most maxRunes Unicode
// code points. Safe against multi-byte boundaries: the cut
// always lands on a rune boundary so the result is valid UTF-8
// even when the input contains code points outside ASCII. Used
// by the alert-preset enable path to clamp the derived rule
// name against AlertRuleNameMaxChars.
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

// AllowedAlertRuleMetrics is the closed set for the `metric` field.
// Must match state.AlertMetric's enumerated values byte-for-byte;
// the handler validates membership before persisting.
//
// Kept in pkg/api (not pkg/state) so this DTO file stays free of the
// pkg/api ↔ pkg/state import cycle — same precedent as the
// pkg/api/dto.go cron block (precedent: PR #327 review).
//
// Issue #1233 / ADR-123 — extended from 7 to 12 metrics for the alert
// preset catalog (api_up, account_spend_eur, deployment_failed,
// cert_expiry_seconds, queue_depth). The DB CHECK constraint on
// alert_rules.metric mirrors this list — see migrations/00349.
var AllowedAlertRuleMetrics = []string{
	"error_rate_pct",
	"latency_p50_ms",
	"latency_p95_ms",
	"latency_p99_ms",
	"cold_start_pct",
	"request_count",
	"failed_invocations",
	"api_up",
	"account_spend_eur",
	"deployment_failed",
	"cert_expiry_seconds",
	"queue_depth",
	// SAFE-RELEASES-OBS PR-B (issue #976 / ADR-122): 4 new
	// Prometheus-counter-backed tripwires for the canary/safedeploy
	// lifecycle. The actual firing happens in Prometheus against
	// the wire.OpsMetrics counters; the catalog entry exists so
	// customers see the presets in /dashboard/alerts and the
	// handler validates the rule.metric. Operators only — gated
	// by minimum_plan = 'scale' in migrations/20260905000000001.
	"canary_stuck_step",
	"safedeploy_audit_emit_failing",
	"deployment_audit_gc_failing",
	"canary_fleet_in_flight_high",
}

// AllowedAlertRuleComparisons is the closed set for the `comparison` field.
var AllowedAlertRuleComparisons = []string{"gt", "gte", "lt", "lte"}

// AllowedAlertRuleWindowSpecs is the closed set for the `window_spec` field.
// Bounded by Prometheus retention (15d).
var AllowedAlertRuleWindowSpecs = []string{
	"5m", "15m", "1h", "6h", "24h", "7d", "15d",
}

// AllowedAlertRuleFailureSources is the closed set for the optional
// `failure_source` field. Only populated when metric == failed_invocations;
// otherwise the DB's alert_rules_failure_source_xor_chk constraint rejects
// the row.
var AllowedAlertRuleFailureSources = []string{
	"any", "cron", "queue", "delayed_task", "async_invoke",
}

// AllowedAlertRuleStates is the closed set for the read-only `state`
// field on the response.
var AllowedAlertRuleStates = []string{"ok", "firing"}

// AllowedAlertRuleActions is the closed set for the `action` field on
// alert_rules. Mirrors the alert_rules_action_chk DB constraint
// (migrations/00481_alert_rules_action.sql). When set to anything
// other than "webhook", the alert evaluator fans out to the new
// pkg/alerts.ActionExecutor seam in addition to the legacy Dispatcher
// webhook fan-out. Issue #976 / ADR-122 / SAFE-RELEASES-B.
var AllowedAlertRuleActions = []string{"webhook", "rollback", "demote", "promote"}

// AllowedAlertRuleAction returns true iff v is in
// AllowedAlertRuleActions. Mirrors the membership-helper pattern at
// AllowedAlertRuleMetric / AllowedAlertRuleComparison / etc.
func AllowedAlertRuleAction(v string) bool {
	return containsString(AllowedAlertRuleActions, v)
}

// CreateAlertRuleRequest is the POST /v1/apps/{slug}/alerts body.
// AppID is the URL slug, not the body — same shape as the per-app
// custom-domain and metric routes.
type CreateAlertRuleRequest struct {
	Name            string  `json:"name"`
	Enabled         *bool   `json:"enabled,omitempty"`
	Metric          string  `json:"metric"`
	Comparison      string  `json:"comparison"`
	Threshold       float64 `json:"threshold"`
	WindowSpec      string  `json:"window_spec"`
	FailureSource   string  `json:"failure_source,omitempty"`
	Action          *string `json:"action,omitempty"`
	WebhookURL      string  `json:"webhook_url"`
	WebhookSecret   string  `json:"webhook_secret"`
	CooldownMinutes *int    `json:"cooldown_minutes,omitempty"`
}

// UpdateAlertRuleRequest is the PATCH body. Every editable field is
// pointer-typed so the handler can distinguish "omitted" (leave
// alone) from "zero" (clear). Mirrors UpdateCronRequest and
// state.UpdateAlertRuleParams.
//
// FailureSource is intentionally NOT here: the
// alert_rules_failure_source_xor_chk constraint forbids rotating the
// metric family in isolation (see pkg/state/types.go:511-518), and
// UpdateAlertRuleParams intentionally omits the field. PR 3's
// handler rejects metric-family swaps with 400
// ErrAlertRuleInvalid("metric family cannot change; delete and recreate").
type UpdateAlertRuleRequest struct {
	Name            *string  `json:"name,omitempty"`
	Enabled         *bool    `json:"enabled,omitempty"`
	Metric          *string  `json:"metric,omitempty"`
	Comparison      *string  `json:"comparison,omitempty"`
	Threshold       *float64 `json:"threshold,omitempty"`
	WindowSpec      *string  `json:"window_spec,omitempty"`
	Action          *string  `json:"action,omitempty"`
	WebhookURL      *string  `json:"webhook_url,omitempty"`
	WebhookSecret   *string  `json:"webhook_secret,omitempty"`
	CooldownMinutes *int     `json:"cooldown_minutes,omitempty"`
}

// RotateAlertRuleSecretRequest is the rotate-secret body. Reserved for
// a future "customer supplies plaintext" variant; PR 3 always
// server-mints via crypto/rand so the body is empty today.
type RotateAlertRuleSecretRequest struct{}

// AlertRuleResponse is the GET / list / create / update shape. It
// mirrors state.AlertRule but drops the sealed ciphertext and renders
// the masked constant in webhook_secret_sealed_masked. Times are
// RFC3339 strings (precedent: CronResponse, InstanceResponse).
//
// The closed-set vocabularies (metric / comparison / window_spec /
// failure_source / state) are plain strings — see AllowedAlertRule*
// for membership. The handler maps them from the corresponding
// state.* typed values at the boundary (handles the import cycle
// for us).
type AlertRuleResponse struct {
	ID                        string  `json:"id"`
	AppID                     string  `json:"app_id"`
	Name                      string  `json:"name"`
	Enabled                   bool    `json:"enabled"`
	Metric                    string  `json:"metric"`
	Comparison                string  `json:"comparison"`
	Threshold                 float64 `json:"threshold"`
	WindowSpec                string  `json:"window_spec"`
	FailureSource             string  `json:"failure_source,omitempty"`
	Action                    string  `json:"action"`
	WebhookURL                string  `json:"webhook_url"`
	WebhookSecretSealedMasked string  `json:"webhook_secret_sealed_masked"`
	CooldownMinutes           int     `json:"cooldown_minutes"`
	State                     string  `json:"state"`
	LastFiredAt               string  `json:"last_fired_at,omitempty"`
	LastEvaluatedAt           string  `json:"last_evaluated_at,omitempty"`
	CreatedAt                 string  `json:"created_at"`
	UpdatedAt                 string  `json:"updated_at"`
}

// AlertRuleRow is the closed-set-typed counterpart of AlertRuleResponse,
// mirroring state.AlertRule verbatim. Used by the handler at the
// pkg/api ↔ pkg/state boundary so the conversion from typed to string
// stays in one place. NOT exported on the wire — kept here so the
// handler test can pin the mapping without dragging pkg/state into
// pkg/api_test.
type AlertRuleRow struct {
	ID              string
	AppID           string
	Name            string
	Enabled         bool
	Metric          string
	Comparison      string
	Threshold       float64
	WindowSpec      string
	FailureSource   string
	Action          string
	WebhookURL      string
	CooldownMinutes int
	State           string
	LastFiredAt     time.Time
	LastEvaluatedAt time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// AlertRuleResponseFromRow maps a wire-shaped row (closed sets as
// strings) to the response DTO. Drops the sealed secret; renders the
// masked constant. Times are RFC3339 strings; zero times serialise as
// empty so the omitempty tag drops them.
//
// This is the load-bearing shape at the pkg/api ↔ pkg/state boundary:
// pkg/api/alerts.go defines the DTO and the converter; pkg/state
// defines the typed row. The handler in cmd/apid does the conversion
// at the seam so neither side imports the other (precedent: PR #327).
func AlertRuleResponseFromRow(r AlertRuleRow) AlertRuleResponse {
	return AlertRuleResponse{
		ID:                        r.ID,
		AppID:                     r.AppID,
		Name:                      r.Name,
		Enabled:                   r.Enabled,
		Metric:                    r.Metric,
		Comparison:                r.Comparison,
		Threshold:                 r.Threshold,
		WindowSpec:                r.WindowSpec,
		FailureSource:             r.FailureSource,
		Action:                    r.Action,
		WebhookURL:                r.WebhookURL,
		WebhookSecretSealedMasked: AlertRuleWebhookSecretMasked,
		CooldownMinutes:           r.CooldownMinutes,
		State:                     r.State,
		LastFiredAt:               FormatAlertTime(r.LastFiredAt),
		LastEvaluatedAt:           FormatAlertTime(r.LastEvaluatedAt),
		CreatedAt:                 FormatAlertTime(r.CreatedAt),
		UpdatedAt:                 FormatAlertTime(r.UpdatedAt),
	}
}

// RotateAlertRuleSecretResponse is the rotate-secret response. The
// plaintext is server-minted and NEVER returned to the customer; the
// only signal is the rotated-at timestamp + the masked constant.
type RotateAlertRuleSecretResponse struct {
	RotatedAt                 string `json:"rotated_at"`
	WebhookSecretSealedMasked string `json:"webhook_secret_sealed_masked"`
}

// FormatAlertTime serialises t as an RFC3339 string. Zero times
// serialise as empty so the JSON omits them via the omitempty tag
// (last_fired_at, last_evaluated_at). CreatedAt / UpdatedAt carry
// the empty string on the wire too — never "0001-01-01T00:00:00Z".
//
// Exported so the test in pkg/api/alerts_test.go can pin the zero-time
// and timezone-normalisation invariants at the package boundary.
func FormatAlertTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// IsFiniteFloat returns true iff v is finite (not NaN, not ±Inf).
// PR 3's body validator rejects non-finite thresholds before they
// reach the DB — Postgres NUMERIC accepts NaN via float8 paths but
// the Go sql layer coerces to NULL silently, which would surface as
// a stale 0.0 alert row. Defence in depth at the API boundary.
func IsFiniteFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// containsString returns true iff needle is in haystack. Used by the
// closed-set validators in the handler. Kept here so pkg/api owns the
// membership check (not pkg/state, not cmd/apid) — the closed sets
// live here too.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// AllowedAlertRuleMetric / AllowedAlertRuleComparison /
// AllowedAlertRuleWindowSpec / AllowedAlertRuleFailureSource /
// AllowedAlertRuleState are the membership predicates the handler
// uses. Each delegates to containsString over the matching slice so
// the closed-set list and the predicate stay co-located.
func AllowedAlertRuleMetric(v string) bool { return containsString(AllowedAlertRuleMetrics, v) }
func AllowedAlertRuleComparison(v string) bool {
	return containsString(AllowedAlertRuleComparisons, v)
}
func AllowedAlertRuleWindowSpec(v string) bool {
	return containsString(AllowedAlertRuleWindowSpecs, v)
}
func AllowedAlertRuleFailureSource(v string) bool {
	return containsString(AllowedAlertRuleFailureSources, v)
}
func AllowedAlertRuleState(v string) bool { return containsString(AllowedAlertRuleStates, v) }

// TrimNonEmpty trims surrounding whitespace from s and returns the
// trimmed string along with whether the result is non-empty. Used by
// the validator for the rule name + URL fields so a payload like
// `{"name":"  "}` is rejected with the same 400 a missing name would
// get — no silent trim-and-accept.
func TrimNonEmpty(s string) (string, bool) {
	t := strings.TrimSpace(s)
	return t, t != ""
}
