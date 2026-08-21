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
//
// The work is split into 3 phase helpers
// (loadAndGatePreset, validateAndDeriveEnablePresetOpts,
// persistInstantiatedAlertRule) so enableAlertPresetFromForm is
// the orchestrator and the body stays under the CLAUDE.md
// ≤-50-lines handler ceiling. The same orchestrator is reused by
// the dashboard form-encoded handler at cmd/apid/dashboard_preset_enable.go.
package main

import (
	"context"
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
// JSON entrypoint; the work delegates to enableAlertPresetFromForm.
//
//  1. Load the preset by {slug, name}. 404 on miss.
//  2. Reject disabled-in-catalog with 400 (ErrAlertPresetDisabled).
//  3. Reject below-minimum-plan with 402 (ErrPlanAlertPresetsNotAllowed).
//  4. Validate body (webhook_url, webhook_secret, cooldown_minutes band).
//  5. SSRF guard the webhook URL (reuses resolveAndCheckEgress from
//     handlers_alerts.go:819).
//  6. Seal the webhook secret (same path as createAlertRule).
//  7. Persist via CreateAlertRuleIfUnderQuota — the existing per-app
//     + per-account cap counts an instantiated preset toward the
//     customer's allowance.
//  8. Audit alert_preset.enabled with {preset_name, app_slug, rule_id}.
//  9. Respond 201 with the AlertRuleResponse so the dashboard renders
//     the new rule alongside hand-rolled ones.
//
// Plan-tier gate order matters: the minimum_plan check fires BEFORE
// loadApp for the same slug-leak reason ErrPlanAlertRulesNotAllowed
// does at handlers_alerts.go:158-162 — a low-plan customer posting
// to a non-existent slug must not see a 404 (the response would
// leak the slug's existence).
func (s *server) enableAlertPreset(w http.ResponseWriter, r *http.Request, acct state.Account) {
	presetName := r.PathValue("name")
	var req api.EnableAlertPresetRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrAlertPresetInvalid("could not decode body: "+err.Error()))
		return
	}
	row, err := s.enableAlertPresetFromForm(r.Context(), acct, r.PathValue("slug"), presetName, req)
	if err != nil {
		api.WriteProblem(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, alertRuleResponse(row))
}

// enableAlertPresetFromForm is the shared work for both the JSON
// path (POST /v1/apps/{slug}/alert-presets/{name}/enable) and the
// dashboard's form-encoded path (POST
// /dashboard/apps/{slug}/alert-presets/{name}/enable). The body
// is the orchestrator: each phase is a private helper that owns
// its own validation surface so the lockstep between JSON + form
// callers is enforced at the orchestrator level (any future
// phase addition lands in one helper, not two). The form path
// calls this after CSRF + auth + form-decoding; the JSON path
// calls it after JSON-decoding. Returns an *api.Problem on every
// failure so both callers can write it verbatim.
func (s *server) enableAlertPresetFromForm(ctx context.Context, acct state.Account, slug, presetName string, req api.EnableAlertPresetRequest) (state.AlertRule, *api.Problem) {
	preset, prob := s.loadAndGateAlertPreset(ctx, acct, presetName)
	if prob != nil {
		return state.AlertRule{}, prob
	}
	if req.WebhookURL == "" || req.WebhookSecret == "" {
		return state.AlertRule{}, api.ErrAlertPresetInvalid("webhook_url and webhook_secret are required")
	}
	if prob := resolveAndCheckEgress(ctx, req.WebhookURL); prob != nil {
		return state.AlertRule{}, prob
	}
	cooldown, enabled, prob := validateAndDeriveEnablePresetOpts(req, preset.DefaultCooldownMinutes)
	if prob != nil {
		return state.AlertRule{}, prob
	}
	sealed, prob := sealPresetWebhookSecret(ctx, req.WebhookSecret)
	if prob != nil {
		return state.AlertRule{}, prob
	}
	return s.persistInstantiatedAlertRule(ctx, acct, slug, preset, req, sealed, cooldown, enabled)
}

// loadAndGateAlertPreset resolves the catalog row by name and
// applies the two plan-tier gates that MUST run before loadApp
// (the slug-leak guard). Returns the typed preset + nil on
// success, or zero-valued + a 4xx Problem on any failure. Pulled
// out of enableAlertPresetFromForm so the catalog-resolve and
// plan-gate phases are unit-testable in isolation — both surfaces
// (JSON, form) share this exact gate.
func (s *server) loadAndGateAlertPreset(ctx context.Context, acct state.Account, presetName string) (state.AlertPreset, *api.Problem) {
	preset, err := s.store.AlertPresetByName(ctx, presetName)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return state.AlertPreset{}, api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such alert preset", "no preset with that name is in the catalog")
		}
		return state.AlertPreset{}, api.ErrCapacity("could not load alert preset")
	}
	if !preset.EnabledInCatalog {
		return state.AlertPreset{}, api.ErrAlertPresetDisabled(preset.Name)
	}
	if !api.PlanMeetsMinimumPlan(acct.Plan, api.Plan(preset.MinimumPlan)) {
		return state.AlertPreset{}, api.ErrPlanAlertPresetsNotAllowed(acct.Plan, preset.Name, preset.MinimumPlan)
	}
	return preset, nil
}

// validateAndDeriveEnablePresetOpts clamps the customer-supplied
// cooldown + enabled override against the preset's defaults and
// the plan-wide band ([1, AlertRuleCooldownMaxMinutes]). Returns
// the derived (cooldownMinutes, enabled) pair + nil on success
// or a 400 ErrAlertPresetInvalid if the cooldown is out of band.
// Pure — no I/O — so it lives in package scope (not a server
// method) and is trivially unit-testable.
func validateAndDeriveEnablePresetOpts(req api.EnableAlertPresetRequest, defaultCooldown int) (cooldown int, enabled bool, prob *api.Problem) {
	cooldown = defaultCooldown
	if req.CooldownMinutes != nil {
		if *req.CooldownMinutes < 1 || *req.CooldownMinutes > api.AlertRuleCooldownMaxMinutes {
			return 0, false, api.ErrAlertPresetInvalid("cooldown_minutes out of band (1..1440)")
		}
		cooldown = *req.CooldownMinutes
	}
	enabled = true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return cooldown, enabled, nil
}

// sealPresetWebhookSecret seals the customer-supplied plaintext
// under the host's age recipient. The recipient IS the secretbox
// owner's long-term private key (set at boot); a nil recipient
// is a hard refusal — the rule never gets persisted in cleartext.
// The 1 KiB plaintext ceiling is enforced by the secretbox layer
// (api.AlertRuleWebhookSecretMaxBytes).
func sealPresetWebhookSecret(ctx context.Context, plaintext string) (sealed []byte, prob *api.Problem) {
	recipient := setSecretRecipient()
	if recipient == nil {
		return nil, api.ErrCapacity("host age recipient not loaded — refusing to seal webhook secret")
	}
	out, err := secretbox.SealBytes(recipient, alertRuleSecretSealLabel, []byte(plaintext), api.AlertRuleWebhookSecretMaxBytes)
	if err != nil {
		if p := api.AsProblem(err); p != nil {
			return nil, p
		}
		return nil, api.ErrCapacity("could not seal webhook secret")
	}
	return out, nil
}

// persistInstantiatedAlertRule wires the final phase: app lookup
// + per-plan rule-cap gate (the underlying cap distinct from the
// preset's minimum_plan gate), display-name derivation, the
// CreateAlertRuleIfUnderQuota persist call, the structured log,
// and the audit row. Returns the freshly-persisted AlertRule +
// nil, or zero + a Problem on any failure.
func (s *server) persistInstantiatedAlertRule(ctx context.Context, acct state.Account, slug string, preset state.AlertPreset, req api.EnableAlertPresetRequest, sealed []byte, cooldown int, enabled bool) (state.AlertRule, *api.Problem) {
	// Plan-tier check for the underlying alert_rules cap (Hobby +
	// above). Mirrors createAlertRule's pre-loadApp gate. Lives
	// here, not in loadAndGateAlertPreset, because the cap is on
	// the *rule* (alert_rules table), not on the preset catalog —
	// a Free customer can still LIST the catalog but every
	// enable path hits this 402.
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.AlertRuleLimitPerApp == 0 {
		return state.AlertRule{}, api.ErrPlanAlertRulesNotAllowed(acct.Plan)
	}
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		// Same IDOR-safe 404 as the createAlertRule path —
		// never reveal whether the app exists on another account.
		return state.AlertRule{}, api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such app", "no app with that slug is visible to this account")
	}
	// Display name: <preset display_name> (<app slug>) so a customer
	// with multiple apps can tell which rule covers which surface.
	// Clamped to api.AlertRuleNameMaxChars via api.TruncateRunes
	// — the DB column is varchar(64) CHARACTERS (char_length, not
	// octet_length), and a multi-byte codepoint in the slug would
	// otherwise get sliced mid-rune, producing invalid UTF-8 that
	// Postgres rejects with SQLSTATE 22021 at INSERT time.
	displayName := api.TruncateRunes(preset.DisplayName+" ("+app.Slug+")", api.AlertRuleNameMaxChars)
	row, err := s.store.CreateAlertRuleIfUnderQuota(ctx, state.AlertRule{
		AccountID:  acct.ID,
		AppID:      app.ID,
		Name:       displayName,
		Enabled:    enabled,
		Metric:     state.AlertMetric(preset.Metric),
		Comparison: state.AlertComparison(preset.Comparison),
		Threshold:  preset.Threshold,
		WindowSpec: state.AlertWindowSpec(preset.WindowSpec),
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
			return state.AlertRule{}, api.ErrPlanAlertRuleQuota(acct.Plan, string(qe.Scope), qe.Limit, qe.Observed)
		case errors.Is(err, state.ErrNotFound):
			return state.AlertRule{}, api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such app", "no app with that slug is visible to this account")
		case errors.Is(err, state.ErrConflict):
			return state.AlertRule{}, api.NewProblem(http.StatusConflict, api.CodeValidation, "Preset already enabled", "an alert rule for this preset already exists on this app; delete it first to re-enable.")
		default:
			return state.AlertRule{}, api.ErrCapacity("could not create alert rule from preset")
		}
	}
	s.log.Info("alert preset enabled",
		"preset", logsanitize.Field(preset.Name),
		"rule", logsanitize.Field(row.ID),
		"app", app.Slug,
		"account", acct.ID,
		"metric", logsanitize.Field(string(row.Metric)),
	)
	s.audit.Emit(ctx, "alert_preset.enabled", &acct.ID, map[string]any{
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
	return row, nil
}
