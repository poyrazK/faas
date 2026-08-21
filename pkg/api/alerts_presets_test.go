// Package api — alert-preset DTO + closed-set tests (issue #1233 / ADR-123).
//
// Covers the small surface that pkg/api owns for the alert-preset
// catalog:
//
//   - AllowedAlertPresetCategories / AllowedAlertPresetMinimumPlans
//     closed-set membership (AlertPresetCategory / AlertPresetMinimumPlan)
//   - PlanMeetsMinimumPlan rank comparison (Free=0..Scale=3; fail-closed
//     on unknown plans)
//   - AlertPresetResponseFromRow mapping (the pkg/api ↔ pkg/state
//     boundary helper)
//   - ErrAlertPresetInvalid / ErrAlertPresetDisabled /
//     ErrPlanAlertPresetsNotAllowed API problem codes
//
// The bulk of the handler logic (instantiate path, plan-tier gate,
// egress guard, quota path) is exercised end-to-end via
// cmd/apid/handlers_alerts_test.go's existing fixtures. This file
// only tests the pkg/api surface itself.
package api

import (
	"fmt"
	"strings"
	"testing"
)

// TestAlertPresetCategory_ClosedSet asserts every entry in
// AllowedAlertPresetCategories passes the membership helper and a
// handful of obviously-out-of-set strings do not. Same drift test
// shape as the cors_presets / consumer_keys closed-set tests.
func TestAlertPresetCategory_ClosedSet(t *testing.T) {
	for _, c := range AllowedAlertPresetCategories {
		if !AlertPresetCategory(c) {
			t.Errorf("AlertPresetCategory(%q) = false; want true", c)
		}
	}
	bad := []string{"", "AvailAbility", "reliability ", "security", "perf"}
	for _, c := range bad {
		if AlertPresetCategory(c) {
			t.Errorf("AlertPresetCategory(%q) = true; want false", c)
		}
	}
}

// TestAlertPresetMinimumPlan_ClosedSet is the same drift test for
// the plan side. The set MUST include "free" (the catalog's
// minimum_plan CHECK admits it even though every shipped preset
// uses hobby+) so a future preset that downgrades is a one-line
// seed change, not a code + migration change.
func TestAlertPresetMinimumPlan_ClosedSet(t *testing.T) {
	for _, p := range AllowedAlertPresetMinimumPlans {
		if !AlertPresetMinimumPlan(p) {
			t.Errorf("AlertPresetMinimumPlan(%q) = false; want true", p)
		}
	}
	bad := []string{"", "Hobby", "enterprise", "trial"}
	for _, p := range bad {
		if AlertPresetMinimumPlan(p) {
			t.Errorf("AlertPresetMinimumPlan(%q) = true; want false", p)
		}
	}
}

// TestPlanMeetsMinimumPlan covers the rank comparison that the
// enableAlertPresetFromForm helper uses. The shape: each tier meets
// its own rank + every rank above; never meets a higher rank; an
// unknown plan fails closed (returns false).
func TestPlanMeetsMinimumPlan(t *testing.T) {
	cases := []struct {
		customer, minimum Plan
		want              bool
	}{
		{PlanFree, PlanFree, true},
		{PlanFree, PlanHobby, false},
		{PlanFree, PlanPro, false},
		{PlanFree, PlanScale, false},
		{PlanHobby, PlanFree, true},
		{PlanHobby, PlanHobby, true},
		{PlanHobby, PlanPro, false},
		{PlanPro, PlanHobby, true},
		{PlanPro, PlanPro, true},
		{PlanScale, PlanPro, true},
		{PlanScale, PlanScale, true},
		// Unknown plans fail closed.
		{Plan("enterprise"), PlanHobby, false},
		{PlanHobby, Plan("enterprise"), false},
	}
	for _, c := range cases {
		if got := PlanMeetsMinimumPlan(c.customer, c.minimum); got != c.want {
			t.Errorf("PlanMeetsMinimumPlan(%q, %q) = %v; want %v", c.customer, c.minimum, got, c.want)
		}
	}
}

// TestAlertPresetResponseFromRow is the row-mapping test. Threshold
// and the booleans must round-trip verbatim (no encoding glitch).
func TestAlertPresetResponseFromRow(t *testing.T) {
	row := AlertPresetRow{
		ID:                     "preset-uuid",
		Name:                   "error_rate_2pct",
		DisplayName:            "Error rate exceeds 2%",
		Description:            "Fires when the rolling-window error rate exceeds 2%.",
		Category:               "reliability",
		Metric:                 "error_rate_pct",
		Comparison:             "gt",
		Threshold:              2.0,
		WindowSpec:             "15m",
		DefaultCooldownMinutes: 15,
		MinimumPlan:            "hobby",
		EnabledInCatalog:       true,
	}
	got := AlertPresetResponseFromRow(row)
	if got.ID != row.ID || got.Name != row.Name || got.DisplayName != row.DisplayName {
		t.Errorf("ID/Name/DisplayName round-trip mismatch: %+v", got)
	}
	if got.Threshold != row.Threshold {
		t.Errorf("Threshold round-trip: got %v; want %v", got.Threshold, row.Threshold)
	}
	if !got.EnabledInCatalog {
		t.Errorf("EnabledInCatalog = false; want true")
	}
	if got.MinimumPlan != "hobby" {
		t.Errorf("MinimumPlan = %q; want hobby", got.MinimumPlan)
	}
}

// TestErrAlertPresetInvalid_CodeStable pins the wire-stable code
// string used by the dashboard's flash-banner machinery + the CLI
// JSON-mode error classifier. Changing the literal is a breaking
// change for any external dashboard rendering.
func TestErrAlertPresetInvalid_CodeStable(t *testing.T) {
	p := ErrAlertPresetInvalid("threshold out of band")
	if p.Code != "alert_preset_invalid" {
		t.Errorf("Code = %q; want alert_preset_invalid", p.Code)
	}
	if p.Status != 400 {
		t.Errorf("Status = %d; want 400", p.Status)
	}
	if !strings.Contains(p.Detail, "threshold out of band") {
		t.Errorf("Detail = %q; want substring %q", p.Detail, "threshold out of band")
	}
}

// TestErrAlertPresetDisabled_CodeStable pins the disabled-in-catalog
// code (issue #1233) — fires before the plan-tier gate so a customer
// who tries to enable a coming-soon preset sees the disabled
// rejection, not a 402.
func TestErrAlertPresetDisabled_CodeStable(t *testing.T) {
	p := ErrAlertPresetDisabled("api_down")
	if p.Code != "alert_preset_disabled" {
		t.Errorf("Code = %q; want alert_preset_disabled", p.Code)
	}
	if p.Status != 400 {
		t.Errorf("Status = %d; want 400", p.Status)
	}
	if !strings.Contains(p.Detail, "api_down") {
		t.Errorf("Detail = %q; want substring %q", p.Detail, "api_down")
	}
}

// TestErrPlanAlertPresetsNotAllowed_CodeStable pins the 402 code.
// The error fires for plans below the preset's minimum_plan and
// MUST run BEFORE loadApp in enableAlertPresetFromForm so a
// low-plan customer posting to a non-existent slug does not see a
// 404 (the response would leak the slug's existence).
func TestErrPlanAlertPresetsNotAllowed_CodeStable(t *testing.T) {
	p := ErrPlanAlertPresetsNotAllowed(PlanHobby, "api_down", "pro")
	if p.Code != "plan_alert_presets_not_allowed" {
		t.Errorf("Code = %q; want plan_alert_presets_not_allowed", p.Code)
	}
	if p.Status != 402 {
		t.Errorf("Status = %d; want 402", p.Status)
	}
	if !strings.Contains(p.Detail, "api_down") {
		t.Errorf("Detail = %q; want substring %q", p.Detail, "api_down")
	}
}

// TestAlertPresetCodes_NoCollision asserts the 3 new codes do not
// collide with the existing alert_rules codes (which already
// include "alert_rule_invalid" / "plan_alert_rules_not_allowed").
// Drift test so a future refactor that folds them together trips
// before the wire breaks.
func TestAlertPresetCodes_NoCollision(t *testing.T) {
	codes := []string{
		CodeAlertPresetInvalid,
		CodeAlertPresetDisabled,
		CodePlanAlertPresetsNotAllowed,
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Errorf("duplicate code %q", c)
		}
		seen[c] = true
	}
	// Sanity check: the existing alert_rules code must NOT collide.
	if seen["alert_rule_invalid"] {
		t.Errorf("alert_preset code collided with alert_rule_invalid")
	}
}

// TestErrAlertPresetInvalid_AsProblem asserts the wrapped-error
// path that the secretbox seal-error takes (handlers_alert_presets.go
// calls api.AsProblem(sealErr) — the *Problem return then flows
// to api.WriteProblem verbatim). The smoke test confirms the
// Problem pointer survives a fmt.Errorf wrap without being clobbered
// to a plain string.
func TestErrAlertPresetInvalid_AsProblem(t *testing.T) {
	p := ErrAlertPresetInvalid("body parse: bad json")
	wrapped := fmt.Errorf("seal failed: %w", p)
	if got := AsProblem(wrapped); got != p {
		t.Errorf("AsProblem(wrapped Problem) = %v; want same pointer", got)
	}
}