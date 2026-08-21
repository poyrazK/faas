// cmd/apid/handlers_alert_presets.go — alert-preset catalog +
// enable-from-preset handlers (issue #1233 / ADR-123).
//
// Two handlers:
//
//   - listAlertPresets   → GET  /v1/alert-presets
//   - enableAlertPreset  → POST /v1/apps/{slug}/alert-presets/{name}/enable
//
// Mirrors cmd/apid/handlers_alerts.go:1-22 (the routes doc-comment
// at the top of that file is the canonical reference for the alert
// rule CRUD routes; this file documents the preset routes alongside
// the other preset API surfaces). Closed-vocabulary guarantees
// (catalog rows come from a system-seeded, R/O table; the handler
// rejects POSTs against disabled rows with ErrAlertPresetDisabled)
// match the precedent set by
// cmd/apid/handlers_cors_presets.go from ADR-091 — read that file
// first if you need to understand the catalog-then-instantiate
// shape.
//
// The enable path is a thin wrapper around createAlertRule (see
// handlers_alerts.go:142). The catalog row's (name, metric,
// comparison, threshold, window_spec, default_cooldown_minutes)
// pre-fill the CreateAlertRuleRequest; only the customer-supplied
// webhook_url + webhook_secret need their own validation. The
// quota path (CreateAlertRuleIfUnderQuota) is reused verbatim —
// instantiating a preset counts toward the same per-app +
// per-account cap as a hand-rolled rule, so the existing 403
// payload is the right answer for the cap-reached path.
package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// listAlertPresets returns every row in alert_presets ordered by
// category, name. The catalog is small (8 rows in PR-A) so no
// pagination; the response is a flat slice.
//
// Plan-tier filtering: rows whose minimum_plan is above the
// caller's plan are returned with enabled_in_catalog=false AND
// minimum_plan unchanged so the dashboard can render the same
// "upgrade to Pro" hint the per-row card already shows. The
// preset row itself stays visible — surfacing a hidden row would
// confuse the dashboard's grid count. Customers above the row's
// minimum_plan see enabled_in_catalog=true iff the catalog has it
// enabled at the migration level (the seed sets this column for
// the 3 shipped-in-PR-A vs 5 staged-for-future rows).
func (s *server) listAlertPresets(w http.ResponseWriter, r *http.Request, _ state.Account) {
	rows, err := s.store.ListAlertPresets(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list alert presets"))
		return
	}
	out := make([]api.AlertPresetResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.AlertPresetResponseFromRow(api.AlertPresetRow{
			ID:                     row.ID,
			Name:                   row.Name,
			DisplayName:            row.DisplayName,
			Description:            row.Description,
			Category:               row.Category,
			Metric:                 row.Metric,
			Comparison:             row.Comparison,
			Threshold:              row.Threshold,
			WindowSpec:             row.WindowSpec,
			DefaultCooldownMinutes: row.DefaultCooldownMinutes,
			MinimumPlan:            row.MinimumPlan,
			EnabledInCatalog:       row.EnabledInCatalog,
		}))
	}
	writeJSON(w, http.StatusOK, out)
}

// enableAlertPreset instantiates a preset as a real alert_rules row.
//
//   1. Load the preset by {slug, name}. 404 on miss.
//   2. Reject disabled-in-catalog with 400 (ErrAlertPresetDisabled).
//   3. Reject below-minimum-plan with 402 (ErrPlanAlertPresetsNotAllowed).
//   4. Validate body (webhook_url, webhook_secret, cooldown_minutes band).
//   5. SSRF guard the webhook URL (reuses resolveAndCheckEgress from
//      handlers_alerts.go:819).
//   6. Seal the webhook secret (same path as createAlertRule).
//   7. Persist via CreateAlertRuleIfUnderQuota — the existing per-app
//      + per-account cap counts an instantiated preset toward the
//      customer's allowance.
//   8. Audit alert_preset.enabled with {preset_name, app_slug, rule_id}.
//   9. Respond 201 with the AlertRuleResponse so the dashboard renders
//      the new rule alongside hand-rolled ones.
//
// Plan-tier gate order matters: the minimum_plan check fires BEFORE
// loadApp for the same slug-leak reason ErrPlanAlertRulesNotAllowed
// does at handlers_alerts.go:158-162 — a low-plan customer posting
// to a non-existent slug must not see a 404 (the response would
// leak the slug's existence).
func (s *server) enableAlertPreset(w http.ResponseWriter, r *http.Request, acct state.Account) {
	presetName := r.PathValue("name")
	// Pre-resolve the catalog row by name only. We DO NOT load the
	// app here — the plan-tier gate must run before loadApp to
	// avoid the slug-leak, and we need the preset's minimum_plan
	// before we know which 402 body to write.
	preset, err := s.store.AlertPresetByName(r.Context(), presetName)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such alert preset", "no preset with that name is in the catalog"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load alert preset"))
		return
	}
	if !preset.EnabledInCatalog {
		api.WriteProblem(w, api.ErrAlertPresetDisabled(preset.Name))
		return
	}
	if !api.PlanMeetsMinimumPlan(acct.Plan, api.Plan(preset.MinimumPlan)) {
		api.WriteProblem(w, api.ErrPlanAlertPresetsNotAllowed(acct.Plan, preset.Name, preset.MinimumPlan))
		return
	}
	var req api.EnableAlertPresetRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrAlertPresetInvalid("could not decode body: "+err.Error()))
		return
	}
	if req.WebhookURL == "" || req.WebhookSecret == "" {
		api.WriteProblem(w, api.ErrAlertPresetInvalid("webhook_url and webhook_secret are required"))
		return
	}
	if prob := resolveAndCheckEgress(r.Context(), req.WebhookURL); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	cooldown := preset.DefaultCooldownMinutes
	if req.CooldownMinutes != nil {
		if *req.CooldownMinutes < 1 || *req.CooldownMinutes > api.AlertRuleCooldownMaxMinutes {
			api.WriteProblem(w, api.ErrAlertPresetInvalid("cooldown_minutes out of band (1..1440)"))
			return
		}
		cooldown = *req.CooldownMinutes
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		api.WriteProblem(w, api.ErrCapacity("host age recipient not loaded — refusing to seal webhook secret"))
		return
	}
	sealed, err := secretbox.SealBytes(recipient, alertRuleSecretSealLabel, []byte(req.WebhookSecret), api.AlertRuleWebhookSecretMaxBytes)
	if err != nil {
		if prob := api.AsProblem(err); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not seal webhook secret"))
		return
	}
	// Plan-tier check for the underlying alert_rules cap (Hobby +
	// above). Mirrors createAlertRule's pre-loadApp gate.
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.AlertRuleLimitPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanAlertRulesNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	// Display name: <preset display_name> (<app slug>) so a customer
	// with multiple apps can tell which rule covers which surface.
	// Clamped to api.AlertRuleNameMaxBytes (the DB column is
	// varchar(AlertRuleNameMaxBytes)) — the trim is at the seam so
	// the catalogue-side display_name can't blow past the cap.
	displayName := preset.DisplayName + " (" + app.Slug + ")"
	if len(displayName) > api.AlertRuleNameMaxBytes {
		displayName = displayName[:api.AlertRuleNameMaxBytes]
	}
	row, err := s.store.CreateAlertRuleIfUnderQuota(r.Context(), state.AlertRule{
		AccountID:           acct.ID,
		AppID:               app.ID,
		Name:                displayName,
		Enabled:             enabled,
		Metric:              state.AlertMetric(preset.Metric),
		Comparison:          state.AlertComparison(preset.Comparison),
		Threshold:           preset.Threshold,
		WindowSpec:          state.AlertWindowSpec(preset.WindowSpec),
		// Preset rows never carry a failure_source (the catalog's
		// metrics are the closed vocabulary at pkg/api/alerts.go:66
		// — none of them are failed_invocations).
		FailureSource:       "",
		WebhookURL:          req.WebhookURL,
		WebhookSecretSealed: sealed,
		CooldownMinutes:     cooldown,
	}, limits)
	if err != nil {
		var qe *state.AlertRuleQuotaError
		switch {
		case errors.As(err, &qe):
			api.WriteProblem(w, api.ErrPlanAlertRuleQuota(acct.Plan, string(qe.Scope), qe.Limit, qe.Observed))
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such app")
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Preset already enabled", "an alert rule for this preset already exists on this app; delete it first to re-enable."))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not create alert rule from preset"))
		}
		return
	}
	s.log.Info("alert preset enabled",
		"preset", logsanitize.Field(preset.Name),
		"rule", logsanitize.Field(row.ID),
		"app", app.Slug,
		"account", acct.ID,
		"metric", logsanitize.Field(string(row.Metric)),
	)
	s.audit.Emit(r.Context(), "alert_preset.enabled", &acct.ID, map[string]any{
		"preset_name": preset.Name,
		"preset_id":   preset.ID,
		"app_id":      row.AppID,
		"rule_id":     row.ID,
		"app_slug":    app.Slug,
		"metric":      row.Metric,
		"threshold":   row.Threshold,
		"webhook_url": req.WebhookURL,
		"enabled":     enabled,
	})
	writeJSON(w, http.StatusCreated, alertRuleResponse(row))
}