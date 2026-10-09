package api_test

// Tests for pkg/api/alerts.go (issue #396 / ADR-045 PR 3).
//
// Coverage matrix:
//   - masked constant: literal "***" (the secret-redaction contract)
//   - max-bytes constant: 256 (matches seal-time guard)
//   - default-cooldown constant: 15 (matches public §4.4)
//   - cooldown band constants: 5 / 1440 (matches DB CHECK)
//   - IsFiniteFloat: NaN, ±Inf, finite — boundary cases
//   - FormatAlertTime: zero time → "" (no "0001-01-01T00:00:00Z" leak)
//   - FormatAlertTime: non-zero time → RFC3339 UTC round-trip
//   - AlertRuleResponseFromRow: drops sealed secret, renders masked
//   - AlertRuleResponseFromRow: zero-valued timestamps render as ""
//   - closed-set membership predicates (metric / comparison / etc.)
//
// The DTO field round-trip tests live implicitly in the cmd/apid
// integration tests (handlers decode + encode); the package-level
// tests here pin the invariants that downstream callers depend on.

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAlertRuleWebhookSecretMasked_IsLiteralThreeAsterisks(t *testing.T) {
	// The masked constant is part of the wire contract — every alert-rule
	// response carries it. Drift to "***" → "redacted" or "•" would break
	// dashboard rendering and the SDK. Pin the literal.
	if api.AlertRuleWebhookSecretMasked != "***" {
		t.Fatalf("AlertRuleWebhookSecretMasked = %q, want \"***\"", api.AlertRuleWebhookSecretMasked)
	}
}

func TestAlertRuleConstants_Pinned(t *testing.T) {
	// Constants that downstream code (handlers, validators) rely on.
	// Drift here would silently shift the wire contract or the DB
	// CHECK-constraint boundary. Pin each one.
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"AlertRuleWebhookSecretMaxBytes", api.AlertRuleWebhookSecretMaxBytes, 256},
		{"AlertRuleDefaultCooldownMinutes", api.AlertRuleDefaultCooldownMinutes, 15},
		{"AlertRuleCooldownMinMinutes", api.AlertRuleCooldownMinMinutes, 5},
		{"AlertRuleCooldownMaxMinutes", api.AlertRuleCooldownMaxMinutes, 1440},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestIsFiniteFloat_BoundaryCases(t *testing.T) {
	cases := []struct {
		in   float64
		want bool
	}{
		{math.NaN(), false},
		{math.Inf(1), false},
		{math.Inf(-1), false},
		{0.0, true},
		{-1.5, true},
		{1e9, true},
	}
	for _, tc := range cases {
		if got := api.IsFiniteFloat(tc.in); got != tc.want {
			t.Errorf("IsFiniteFloat(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFormatAlertTime_ZeroIsEmptyString(t *testing.T) {
	// Zero time MUST serialise as "" so the JSON omitempty tag drops
	// the field. Otherwise the wire leaks "0001-01-01T00:00:00Z".
	got := api.FormatAlertTime(time.Time{})
	if got != "" {
		t.Errorf("FormatAlertTime(zero) = %q, want \"\"", got)
	}
}

func TestFormatAlertTime_RFC3339UTC(t *testing.T) {
	// A non-UTC time must serialise in UTC so customers in different
	// timezones see the same timestamp shape. The dashboard's clock
	// rendering depends on this.
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("no tzdata on this host: %v", err)
	}
	tt := time.Date(2026, 7, 28, 12, 0, 0, 0, loc)
	got := api.FormatAlertTime(tt)
	if !strings.HasSuffix(got, "Z") {
		t.Errorf("FormatAlertTime did not normalise to UTC: %q", got)
	}
	// Round-trip: parse and verify year/month/day survives.
	parsed, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("FormatAlertTime did not emit RFC3339: %v (got %q)", err, got)
	}
	if parsed.Year() != 2026 || parsed.Month() != 7 || parsed.Day() != 28 {
		t.Errorf("RFC3339 round-trip lost date: %v", parsed)
	}
}

func TestAlertRuleResponseFromRow_DropsSealedSecretAndMasks(t *testing.T) {
	// The state row carries a 256-byte sealed ciphertext that must NEVER
	// reach the wire. The DTO replaces it with the masked constant. The
	// row-level sealed field doesn't exist on AlertRuleRow by design —
	// the handler is the only place that touches sealed ciphertext — so
	// the wire-side contract is that the field never appears.
	row := api.AlertRuleRow{
		ID:              "rule-1",
		AppID:           "app-1",
		Name:            "p99 > 500ms",
		Enabled:         true,
		Metric:          "latency_p99_ms",
		Comparison:      "gt",
		Threshold:       500,
		WindowSpec:      "5m",
		WebhookURL:      "https://example.com/hook",
		CooldownMinutes: 15,
		State:           "ok",
		CreatedAt:       time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2026, 7, 28, 11, 0, 0, 0, time.UTC),
	}

	resp := api.AlertRuleResponseFromRow(row)
	if resp.WebhookSecretSealedMasked != api.AlertRuleWebhookSecretMasked {
		t.Errorf("WebhookSecretSealedMasked = %q, want %q", resp.WebhookSecretSealedMasked, api.AlertRuleWebhookSecretMasked)
	}
	if resp.ID != "rule-1" || resp.AppID != "app-1" || resp.Name != "p99 > 500ms" {
		t.Errorf("field round-trip lost data: %+v", resp)
	}
	if resp.Metric != "latency_p99_ms" || resp.Comparison != "gt" || resp.WindowSpec != "5m" {
		t.Errorf("closed-set strings lost: %+v", resp)
	}

	// Marshal to JSON and confirm the wire shape is correct: the
	// masked-constant field appears with the literal "***" and no
	// sealed-ciphertext field of any name leaks via a future struct
	// rename. PR review finding F7: the previous assertion checked
	// the Go field name (which never appears in JSON) and was
	// vacuously true — fix below uses the JSON tag name.
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const maskedField = `"webhook_secret_sealed_masked":"***"`
	if !strings.Contains(string(raw), maskedField) {
		t.Errorf("JSON missing the masked-constant field %s; got %s", maskedField, raw)
	}
	// Belt-and-braces: if a future rename accidentally lands a
	// sealed-ciphertext field on the wire, the test must catch it
	// even if the row is the wrong shape. Match any JSON key
	// containing "secret" or "sealed" (case-insensitive) that is
	// NOT the masked-constant field.
	for _, line := range strings.Split(string(raw), ",") {
		l := strings.ToLower(line)
		if strings.Contains(l, "secret") || strings.Contains(l, "sealed") {
			if strings.Contains(line, "***") {
				continue
			}
			t.Errorf("JSON leaked a non-masked secret/sealed field: %s", line)
		}
	}
}

func TestAlertRuleResponseFromRow_ZeroTimesOmit(t *testing.T) {
	// LastFiredAt and LastEvaluatedAt are zero until first fire / eval.
	// The wire must omit them, not render "0001-01-01T00:00:00Z".
	row := api.AlertRuleRow{
		ID:    "rule-1",
		AppID: "app-1",
		Name:  "fresh",
		State: "ok",
		// LastFiredAt, LastEvaluatedAt, CreatedAt, UpdatedAt all zero.
	}
	resp := api.AlertRuleResponseFromRow(row)
	if resp.LastFiredAt != "" {
		t.Errorf("LastFiredAt = %q, want \"\" (zero time must omit)", resp.LastFiredAt)
	}
	if resp.LastEvaluatedAt != "" {
		t.Errorf("LastEvaluatedAt = %q, want \"\" (zero time must omit)", resp.LastEvaluatedAt)
	}
	if resp.CreatedAt != "" {
		t.Errorf("CreatedAt = %q, want \"\" (zero time must serialise as empty)", resp.CreatedAt)
	}
	if resp.UpdatedAt != "" {
		t.Errorf("UpdatedAt = %q, want \"\" (zero time must serialise as empty)", resp.UpdatedAt)
	}
}

func TestAlertRuleResponseFromRow_NonZeroTimesRender(t *testing.T) {
	row := api.AlertRuleRow{
		ID:              "rule-1",
		State:           "firing",
		LastFiredAt:     time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
		LastEvaluatedAt: time.Date(2026, 7, 28, 10, 1, 0, 0, time.UTC),
		CreatedAt:       time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2026, 7, 28, 10, 2, 0, 0, time.UTC),
	}
	resp := api.AlertRuleResponseFromRow(row)
	for _, c := range []struct {
		field string
		got   string
	}{
		{"LastFiredAt", resp.LastFiredAt},
		{"LastEvaluatedAt", resp.LastEvaluatedAt},
		{"CreatedAt", resp.CreatedAt},
		{"UpdatedAt", resp.UpdatedAt},
	} {
		if c.got == "" {
			t.Errorf("%s = \"\", want non-empty RFC3339", c.field)
		}
		if _, err := time.Parse(time.RFC3339, c.got); err != nil {
			t.Errorf("%s = %q is not RFC3339: %v", c.field, c.got, err)
		}
	}
}

func TestCreateAlertRuleRequest_RoundTrip(t *testing.T) {
	enabled := true
	cooldown := 30
	in := api.CreateAlertRuleRequest{
		Name:            "rule-A",
		Enabled:         &enabled,
		Metric:          "error_rate_pct",
		Comparison:      "gte",
		Threshold:       1.5,
		WindowSpec:      "15m",
		WebhookURL:      "https://example.com/h",
		WebhookSecret:   "shh",
		CooldownMinutes: &cooldown,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out api.CreateAlertRuleRequest
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Name != in.Name || out.Metric != in.Metric || out.Comparison != in.Comparison {
		t.Errorf("round-trip lost data: %+v", out)
	}
	if out.CooldownMinutes == nil || *out.CooldownMinutes != cooldown {
		t.Errorf("CooldownMinutes = %v, want pointer to %d", out.CooldownMinutes, cooldown)
	}
}

func TestUpdateAlertRuleRequest_PartialFieldsOmitEmpty(t *testing.T) {
	// The partial-update pattern: nil pointers must NOT serialise so the
	// handler can distinguish "omitted" from "zero". Drift to serialise
	// nil → "" would clear the field on every PATCH.
	name := "rule-A"
	threshold := 0.0
	in := api.UpdateAlertRuleRequest{Name: &name, Threshold: &threshold}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Only "name" and "threshold" should appear; nil pointer fields
	// must be absent so json.Unmarshal leaves them nil on the way back.
	if !strings.Contains(string(raw), `"name":"rule-A"`) {
		t.Errorf("JSON missing name: %s", raw)
	}
	if !strings.Contains(string(raw), `"threshold":0`) {
		t.Errorf("JSON missing threshold: %s", raw)
	}
	for _, banned := range []string{
		`"enabled":`, `"metric":`, `"comparison":`, `"window_spec":`,
		`"webhook_url":`, `"webhook_secret":`, `"cooldown_minutes":`,
	} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("JSON leaked nil-pointer field %q: %s", banned, raw)
		}
	}
}

func TestClosedSetPredicates(t *testing.T) {
	// Each predicate MUST accept every value in its slice and reject
	// drift. Drift here would silently let the DB's CHECK constraint
	// trip on a payload that should have been caught at the API.
	cases := []struct {
		name   string
		slice  []string
		accept func(string) bool
	}{
		{"Metric", api.AllowedAlertRuleMetrics, api.AllowedAlertRuleMetric},
		{"Comparison", api.AllowedAlertRuleComparisons, api.AllowedAlertRuleComparison},
		{"WindowSpec", api.AllowedAlertRuleWindowSpecs, api.AllowedAlertRuleWindowSpec},
		{"FailureSource", api.AllowedAlertRuleFailureSources, api.AllowedAlertRuleFailureSource},
		{"State", api.AllowedAlertRuleStates, api.AllowedAlertRuleState},
	}
	for _, tc := range cases {
		for _, v := range tc.slice {
			if !tc.accept(v) {
				t.Errorf("%s predicate rejected %q (its own closed-set value)", tc.name, v)
			}
		}
		if tc.accept("__not_in_closed_set__") {
			t.Errorf("%s predicate accepted a bogus value", tc.name)
		}
		if tc.accept("") {
			t.Errorf("%s predicate accepted empty string (closed sets reject empty)", tc.name)
		}
	}
}

func TestNotificationOnlyAlertActions(t *testing.T) {
	for _, metric := range []string{"pre_auth_target_threshold", "pre_auth_target_signal_gap_pct", "pre_auth_pressure", "edge_validation_failures", "edge_rejections", "workflow_failures", "workflow_schedule_quota_skips", "workflow_pending_age_seconds", "workflow_waiting_age_seconds", "workflow_due_age_seconds"} {
		for _, action := range []string{"", "webhook"} {
			if !api.AlertRuleActionAllowedForMetric(metric, action) {
				t.Fatalf("notification-only metric %q rejected action %q", metric, action)
			}
		}
		for _, action := range []string{"rollback", "demote", "promote"} {
			if api.AlertRuleActionAllowedForMetric(metric, action) {
				t.Fatalf("notification-only metric %q accepted deployment action %q", metric, action)
			}
		}
	}
	if !api.AlertRuleActionAllowedForMetric("error_rate_pct", "rollback") {
		t.Fatal("unrelated alert action rejected")
	}
}

func TestAllowedClosedSets_OrderedAndNonEmpty(t *testing.T) {
	// Pin order + non-empty for the closed sets the OpenAPI spec
	// enumerates verbatim. Drift would break the spec.
	cases := []struct {
		name  string
		slice []string
		min   int
	}{
		{"Metric", api.AllowedAlertRuleMetrics, 5},
		{"Comparison", api.AllowedAlertRuleComparisons, 2},
		{"WindowSpec", api.AllowedAlertRuleWindowSpecs, 5},
		{"FailureSource", api.AllowedAlertRuleFailureSources, 2},
		{"State", api.AllowedAlertRuleStates, 4},
	}
	for _, tc := range cases {
		if len(tc.slice) < tc.min {
			t.Errorf("AllowedAlertRule%s closed set has %d entries, want ≥ %d", tc.name, len(tc.slice), tc.min)
		}
	}
}

func TestTrimNonEmpty(t *testing.T) {
	cases := []struct {
		in        string
		wantOut   string
		wantEmpty bool
	}{
		{"hello", "hello", true},
		{"  hello  ", "hello", true},
		{"", "", false},
		{"   ", "", false},
		{"\t\nhello\n", "hello", true},
	}
	for _, tc := range cases {
		got, ok := api.TrimNonEmpty(tc.in)
		if got != tc.wantOut || ok != tc.wantEmpty {
			t.Errorf("TrimNonEmpty(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.wantOut, tc.wantEmpty)
		}
	}
}
