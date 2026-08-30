// Dashboard enable-from-preset form-POST handler (issue #1233 /
// ADR-123). Mirrors dashboard_cron_fire.go:88 exactly — the
// dashboard renders the Alert presets grid with a per-card
// <form method="POST" action="/apps/{slug}/alert-presets/{name}/enable">;
// this file decodes that form, verifies CSRF, and reuses the
// JSON enableAlertPreset path (handlers_alert_presets.go:117)
// to do the work.
//
// Why a separate handler (vs. reusing enableAlertPreset's JSON
// body directly): the dashboard form posts
// application/x-www-form-urlencoded, not application/json. Two
// seams with different content-types means two handlers, each
// < 50 lines per CLAUDE.md. The work is shared via the same
// JSON API path — this handler is a thin adapter that
// parses form values + invokes the JSON handler internally.
//
// Returns 302 to /dashboard/apps/{slug}?preset_enabled={ok|error}
// so the app-detail page surfaces the success / failure via a
// flash banner (mirrors dashboardFireCron's ?fired=1 pattern).
package main

import (
	"net/http"
	"regexp"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/middleware"
)

// dashboardEnablePresetAction is the CSRF action binding for the
// preset-enable forms on /dashboard/apps/{slug}. Shared across
// every preset card on the grid (same shape as
// dashboardFireCronAction at dashboard_cron_fire.go:45). The
// verifier (middleware.VerifyAuthenticated) seals
// (action, account_id) and checks the form's csrf_token field
// against the envelope on POST.
const dashboardEnablePresetAction = "enable_alert_preset"

// presetNameRe matches the catalog-key shape (lowercase + underscore +
// digit, 1..64 chars — mirrors the alert_presets_name_len_chk DB
// constraint at migrations/00347_alert_presets.sql). Same regex
// gating as dashboardFireCronIDRe to kill G710 open-redirect via
// path-param taint — the redirect target concatenates the slug +
// preset name into a fragment / query value.
var presetNameRe = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// renderDashboardEnablePreset handles POST
// /dashboard/apps/{slug}/alert-presets/{name}/enable from the
// dashboard's form-encoded path. Verifies CSRF against the
// (action="enable_alert_preset", account_id) sealed envelope,
// reuses enableAlertPreset's JSON work via an internal adapter,
// and redirects back to /dashboard/apps/{slug} with a
// ?preset_enabled=… flash flag the template renders.
//
// Form fields:
//
//	csrf_token     — required; rendered by renderAppDetail as a
//	                 hidden <input name="csrf_token" value="{{…}}">.
//	webhook_url    — required; the delivery URL the alert posts to.
//	webhook_secret — required; the plaintext HMAC secret that gets
//	                 sealed server-side.
func (s *server) renderDashboardEnablePreset(w http.ResponseWriter, r *http.Request, slug, presetName string) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !validSlug(slug) || !presetNameRe.MatchString(presetName) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := middleware.VerifyAuthenticated(s.sessions, r, dashboardEnablePresetAction, acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	webhookURL := r.PostFormValue("webhook_url")
	webhookSecret := r.PostFormValue("webhook_secret")
	if webhookURL == "" || webhookSecret == "" {
		http.Redirect(w, r, "/dashboard/apps/"+slug+"?preset_enabled=error", http.StatusFound)
		return
	}
	// Build an EnableAlertPresetRequest and reuse the JSON path's
	// guard order. We don't reuse the JSON handler directly because
	// the JSON handler decodes from r.Body and would re-do the
	// CSRF + auth check (those are already satisfied at this
	// point). The internal call here is to a private helper that
	// expects an already-authed acct + a pre-validated request.
	row, err := s.enableAlertPresetFromForm(r.Context(), acct, slug, presetName, api.EnableAlertPresetRequest{
		WebhookURL:    webhookURL,
		WebhookSecret: webhookSecret,
		Enabled:       ptrTrue(),
	})
	if err != nil {
		// Surface via redirect + flash flag; the template renders
		// the ?preset_enabled=error banner. Mirrors
		// renderDashboardFireCron's error branch (cron_fire.go:121-124).
		//nolint:gosec // G710: slug + presetName are regex-gated above, so the redirect target cannot escape the /dashboard/apps/{slug} prefix.
		http.Redirect(w, r, "/dashboard/apps/"+slug+"?preset_enabled=error", http.StatusFound)
		return
	}
	s.log.Info("dashboard: alert preset enabled",
		"preset", presetName,
		"rule", row.ID,
		"app", slug,
		"account", acct.ID,
	)
	//nolint:gosec // G710: same gating as the error branch above.
	http.Redirect(w, r, "/dashboard/apps/"+slug+"?preset_enabled=ok", http.StatusFound)
}

// dashboardEnablePreset is the mux.HandleFunc entrypoint. Decodes
// the path params and delegates to renderDashboardEnablePreset;
// lives next to the handler it serves so the mux.HandleFunc
// registration in server.go can keep one s.dashboardEnablePreset
// reference without stutter. Mirrors dashboardFireCron at
// dashboard_cron_fire.go:140.
func (s *server) dashboardEnablePreset(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	name := r.PathValue("name")
	s.renderDashboardEnablePreset(w, r, slug, name)
}

// ptrTrue is a small helper that returns &true so the dashboard
// can set Enabled=true on the EnableAlertPresetRequest without
// an unconditional import of pkg/api in this file's other
// helpers. Equivalent to cmd/apid/handlers_secrets.go's
// internal ptrBool helper — kept local so the seam stays
// dashboard-scoped.
func ptrTrue() *bool { v := true; return &v }

// dashboardSendTestAlertPresetAction is the CSRF action binding
// for the per-card "Send test alert" forms on /dashboard/apps/{slug}.
// Separate from dashboardEnablePresetAction so a customer who has
// the page open with both forms visible cannot replay the enable
// action's CSRF envelope on the test endpoint (and vice versa).
// Reusing dashboardEnablePresetAction across both forms would
// shrink the attack surface for a stolen cookie; the verifier
// (middleware.VerifyAuthenticated) seals (action, account_id) and
// refuses cross-action replays.
const dashboardSendTestAlertPresetAction = "send_test_alert_preset"

// renderDashboardSendTestAlertPreset handles POST
// /dashboard/apps/{slug}/alert-presets/{name}/test from the
// dashboard's per-card "Send test alert" button. Mirrors
// renderDashboardEnablePreset's seam shape (form-encoded → JSON
// work via internal helper → redirect + flash flag).
//
// Unlike the enable path, this endpoint requires no body — the
// instantiated rule already carries the webhook_url + secret,
// so the click is action-only.
//
// Returns 302 to /dashboard/apps/{slug}?test_alert={ok|error}
// so the app-detail page surfaces the success / failure via a
// flash banner (same pattern as ?preset_enabled=… for the
// enable path). The flash flag is per-action so the two banners
// don't collide.
func (s *server) renderDashboardSendTestAlertPreset(w http.ResponseWriter, r *http.Request, slug, presetName string) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !validSlug(slug) || !presetNameRe.MatchString(presetName) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := middleware.VerifyAuthenticated(s.sessions, r, dashboardSendTestAlertPresetAction, acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	res, prob := s.sendTestAlertPresetCore(r.Context(), acct, slug, presetName)
	if prob != nil {
		s.log.Warn("dashboard: alert preset test failed",
			"preset", presetName,
			"app", slug,
			"account", acct.ID,
			"err_code", logsanitize.Field(prob.Code),
		)
		//nolint:gosec // G710: slug + presetName are regex-gated above, so the redirect target cannot escape the /dashboard/apps/{slug} prefix.
		http.Redirect(w, r, "/dashboard/apps/"+slug+"?test_alert=error", http.StatusFound)
		return
	}
	s.log.Info("dashboard: alert preset test sent",
		"preset", presetName,
		"app", slug,
		"account", acct.ID,
		"delivery_id", logsanitize.Field(res.DeliveryID),
		"attempts", res.Attempts,
	)
	//nolint:gosec // G710: same gating as the error branch above.
	http.Redirect(w, r, "/dashboard/apps/"+slug+"?test_alert=ok", http.StatusFound)
}

// dashboardSendTestAlertPreset is the mux.HandleFunc entrypoint.
// Decodes the path params and delegates to
// renderDashboardSendTestAlertPreset. Mirrors dashboardEnablePreset
// at dashboard_preset_enable.go:122.
func (s *server) dashboardSendTestAlertPreset(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	name := r.PathValue("name")
	s.renderDashboardSendTestAlertPreset(w, r, slug, name)
}
