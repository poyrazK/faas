// handlers_alert_presets_test.go — unit tests for the pure
// phase helpers extracted from enableAlertPresetFromForm
// (issue #1233 / ADR-123 review fix #5).
//
// The orchestrator enableAlertPresetFromForm is exercised
// end-to-end by the dashboard_preset_enable_test (CSRF + form)
// and the integration smoke from PR-A; here we pin the
// smaller helpers that have no I/O:
//
//   - validateAndDeriveEnablePresetOpts — cooldown band gate
//   - enabled override.
//   - (TruncateRunes is tested in pkg/api via the existing
//     AlertRuleNameMaxChars helper tests; a follow-up could
//     add an explicit UTF-8 boundary test if needed.)
//
// These helpers live in package main (server methods + free
// funcs); the test is in the same package so it can call them
// directly. The CSRF + form-encoded handler is tested via
// dashboard_preset_enable_test.go (separate file).
package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestValidateAndDeriveEnablePresetOpts_CooldownBand pins the
// cooldown gate: a value inside [1, AlertRuleCooldownMaxMinutes]
// is accepted verbatim; a value outside is rejected with
// ErrAlertPresetInvalid; a nil override falls back to the
// preset default. The 0 sentinel is NOT special-cased here —
// the band gate is independent of the CLI's "0 means use
// preset default" convention (the CLI drops 0 from the wire
// body so this gate only sees a non-nil override in {1,1440}).
func TestValidateAndDeriveEnablePresetOpts_CooldownBand(t *testing.T) {
	const defaultCooldown = 15
	cases := []struct {
		name        string
		override    *int
		wantCD      int
		wantEnabled bool
		wantErr     bool
	}{
		{name: "no override uses default", override: nil, wantCD: defaultCooldown, wantEnabled: true, wantErr: false},
		{name: "override in band", override: intPtr(30), wantCD: 30, wantEnabled: true, wantErr: false},
		{name: "override at floor", override: intPtr(1), wantCD: 1, wantEnabled: true, wantErr: false},
		{name: "override at ceiling", override: intPtr(api.AlertRuleCooldownMaxMinutes), wantCD: api.AlertRuleCooldownMaxMinutes, wantEnabled: true, wantErr: false},
		{name: "override below floor", override: intPtr(0), wantCD: 0, wantEnabled: false, wantErr: true},
		{name: "override above ceiling", override: intPtr(api.AlertRuleCooldownMaxMinutes + 1), wantCD: 0, wantEnabled: false, wantErr: true},
		{name: "override far above ceiling", override: intPtr(99999), wantCD: 0, wantEnabled: false, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := api.EnableAlertPresetRequest{CooldownMinutes: c.override}
			gotCD, gotEnabled, prob := validateAndDeriveEnablePresetOpts(req, defaultCooldown)
			if (prob != nil) != c.wantErr {
				t.Fatalf("prob = %v, wantErr = %v", prob, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if gotCD != c.wantCD {
				t.Errorf("cooldown = %d, want %d", gotCD, c.wantCD)
			}
			if gotEnabled != c.wantEnabled {
				t.Errorf("enabled = %v, want %v", gotEnabled, c.wantEnabled)
			}
		})
	}
}

// TestValidateAndDeriveEnablePresetOpts_EnabledOverride pins
// the Enabled pointer semantics: nil → true (default-on),
// &true → true, &false → false. Theorized to be needed by a
// future "disable this rule on instantiate" feature; today
// every code path sets it implicitly.
func TestValidateAndDeriveEnablePresetOpts_EnabledOverride(t *testing.T) {
	cases := []struct {
		name    string
		enabled *bool
		want    bool
	}{
		{"nil defaults to true", nil, true},
		{"true explicit", boolPtr(true), true},
		{"false explicit", boolPtr(false), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, got, _ := validateAndDeriveEnablePresetOpts(api.EnableAlertPresetRequest{Enabled: c.enabled}, 15)
			if got != c.want {
				t.Errorf("enabled = %v, want %v", got, c.want)
			}
		})
	}
}

// TestValidateAndDeriveEnablePresetOpts_BandErrorsAreAlertPresetInvalid
// pins that every out-of-band rejection carries the
// ErrAlertPresetInvalid error code, NOT ErrCapacity or a bare
// 400. The dashboard flash banner keys off this code to render
// the right "validation error" copy.
func TestValidateAndDeriveEnablePresetOpts_BandErrorsAreAlertPresetInvalid(t *testing.T) {
	_, _, prob := validateAndDeriveEnablePresetOpts(api.EnableAlertPresetRequest{CooldownMinutes: intPtr(0)}, 15)
	if prob == nil {
		t.Fatal("expected Problem, got nil")
	}
	if prob.Code != api.CodeAlertPresetInvalid {
		t.Errorf("code = %q, want %q", prob.Code, api.CodeAlertPresetInvalid)
	}
	if prob.Status != 400 {
		t.Errorf("status = %d, want 400", prob.Status)
	}
}
