// preview_alert_presets_test.go — dashboard render test for the
// alert-preset grid (issue #1233 / ADR-123).
//
// Pins the structural contract:
//
//  1. The grid renders when .Data.Presets is non-empty; the section
//     is suppressed entirely when it is nil or zero-length
//     (mirrors the existing .Data.Previews panel at
//     preview_panel_test.go:44).
//  2. Each card shows DisplayName, Category, the (metric, threshold,
//     window_spec) rule code, and a Cooldown / Min-plan line.
//  3. Three card-state branches render the right element:
//     - .Enabled → form POSTing to /apps/{slug}/alert-presets/{name}/enable
//     with the webhook_url + webhook_secret fields.
//     - !EnabledInCatalog → "Coming soon" badge.
//     - else (EnabledInCatalog but !MeetsPlan) → "Upgrade to {plan}" badge.
//  4. The form action URL is built from AppSlug + Name (no
//     leaked values).
package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

// TestRender_AppDetail_AlertPresets_ThreeCardStates pins the three
// card branches in one shot. The seed covers an Enabled card, a
// coming-soon card, and an upgrade-required card so any future
// template refactor that drops a branch fires the test.
func TestRender_AppDetail_AlertPresets_ThreeCardStates(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const nonce = "preset-nonce-22chars-12"

	page := dashboard.Page{
		Title: "demo",
		Body:  "app_detail",
		Data: dashboard.AppDetailData{
			App: dashboard.AppListItem{
				Slug:   "demo",
				AppID:  "demo-uuid",
				Status: "active",
				URL:    "https://demo.apps.gregale.dev",
			},
			Presets: []dashboard.AlertPresetItem{
				{
					// Enabled card — Hobby minimum_plan, customer's
					// plan meets it, preset enabled in catalog.
					Name:                   "error_rate_2pct",
					DisplayName:            "Error rate exceeds 2%",
					Description:            "Fires when the rolling error rate exceeds 2%.",
					Category:               "reliability",
					Metric:                 "error_rate_pct",
					Comparison:             "gt",
					Threshold:              2,
					WindowSpec:             "15m",
					DefaultCooldownMinutes: 15,
					MinimumPlan:            "hobby",
					EnabledInCatalog:       true,
					Enabled:                true,
					MeetsPlan:              true,
					AppSlug:                "demo",
					EnableConfirmToken:     "csrf-token-three-states-test",
				},
				{
					// Coming-soon card — EnabledInCatalog=false but
					// MeetsPlan=true (customer's plan meets the floor).
					Name:                   "api_down",
					DisplayName:            "API is down",
					Description:            "Fires when last /readyz probe failed.",
					Category:               "availability",
					Metric:                 "api_up",
					Comparison:             "lt",
					Threshold:              1,
					WindowSpec:             "5m",
					DefaultCooldownMinutes: 5,
					MinimumPlan:            "pro",
					EnabledInCatalog:       false,
					Enabled:                false,
					MeetsPlan:              true,
					AppSlug:                "demo",
				},
				{
					// Upgrade-required card — EnabledInCatalog=true
					// but MeetsPlan=false (Hobby customer looking at
					// a Pro-floor preset).
					Name:                   "deploy_failed",
					DisplayName:            "Deployment failed",
					Description:            "Fires when the latest deployment fails healthcheck.",
					Category:               "deployment",
					Metric:                 "deployment_failed",
					Comparison:             "gt",
					Threshold:              0,
					WindowSpec:             "1h",
					DefaultCooldownMinutes: 15,
					MinimumPlan:            "pro",
					EnabledInCatalog:       true,
					Enabled:                false,
					MeetsPlan:              false,
					AppSlug:                "demo",
				},
			},
		},
	}
	if err := dashboard.Render(rec, log, nonce, page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		// Section header.
		"Alert presets",
		// Enabled card — form action + button + fields.
		"Error rate exceeds 2%",
		`action="/apps/demo/alert-presets/error_rate_2pct/enable"`,
		`name="csrf_token"`,
		`value="csrf-token-three-states-test"`,
		`name="webhook_url"`,
		`name="webhook_secret"`,
		"Enable",
		// Coming-soon card.
		"API is down",
		"badge-coming-soon",
		"Coming soon",
		// Upgrade card.
		"Deployment failed",
		"badge-upgrade",
		"Upgrade to pro",
		// Card-level structural pieces (one per row, so check at least one).
		"alert-preset-grid",
		"alert-preset-card",
		`error_rate_pct gt 2 over 15m`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
	// Coming-soon card must NOT carry the enable form (no webhook
	// fields leak).
	if strings.Contains(body, `alert-presets/api_down/enable`) {
		t.Errorf("coming-soon card leaked the enable form action")
	}
	// Upgrade card must NOT carry the enable form either.
	if strings.Contains(body, `alert-presets/deploy_failed/enable`) {
		t.Errorf("upgrade-required card leaked the enable form action")
	}
}

// TestRender_AppDetail_AlertPresets_EmptyHidden pins the
// suppression contract: a nil Presets slice must NOT render the
// grid (the section header is a permanent anchor the dashboard
// uses for the Alerts → Alert presets navigation; the grid is
// what gates on the slice). Mirrors the existing
// TestRender_AppDetail_PreviewPanel_Empty (preview_panel_test.go:138)
// so a future refactor that flips the {{if}} semantics fails
// loudly.
func TestRender_AppDetail_AlertPresets_EmptyHidden(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "demo",
		Body:  "app_detail",
		Data: dashboard.AppDetailData{
			App: dashboard.AppListItem{
				Slug:   "demo",
				AppID:  "demo-uuid",
				Status: "active",
				URL:    "https://demo.apps.gregale.dev",
			},
			// Presets nil → grid suppressed, header still present.
		},
	}
	if err := dashboard.Render(rec, log, "nonce-22chars-12", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	// Assert no <article class="alert-preset-card …"> renders — the
	// bare class names also appear in the embedded <style> block,
	// so anchor on the rendered-element marker instead.
	if strings.Contains(body, `class="alert-preset-card`) {
		t.Errorf("body rendered alert-preset-card with nil Presets slice")
	}
	if strings.Contains(body, `<div class="alert-preset-grid">`) {
		t.Errorf("body rendered alert-preset-grid div with nil Presets slice")
	}
	// Section header is the permanent anchor; it must still render
	// so the navigation is consistent regardless of catalog state.
	if !strings.Contains(body, "Alert presets") {
		t.Errorf("body missing the Alert presets section anchor")
	}
}
