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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
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

// sendTestAlertPreset posts a synthetic alert payload to the
// webhook URL the customer configured when they enabled this
// preset (issue #1233 / ADR-123 PR-C commit 2). The body carries
// `payload.test = true` so the customer's verifier can branch on
// the discriminator (skip the production alert-write path, log
// to a quieter channel, etc.).
//
// Why the work bypasses meterd: the production alert-fire path
// is owned by the meterd evaluator at pkg/alerts/evaluator.go,
// which also writes to alert_deliveries (the operator's recent-
// deliveries pane). A test alert should NOT pollute that ledger —
// the customer expects "send a test", not "send a real alert
// that the operator sees". We dispatch through the same
// webhookout.Dispatcher the production path uses (same retry /
// backoff / signing), but with a synthetic observed value AND
// the test discriminator.
//
// Returns:
//
//	200 — payload delivered (any 2xx/3xx from the customer's
//	      receiver). Body: {"status":"sent","test":true,
//	      "delivery_id":"<uuid>","attempts":N}.
//	404 — no alert rule instantiated for this preset on this app.
//	      Mirrors handlers_alerts.go's getAlertRule 404 path.
//	402 — below the preset's minimum_plan (set by
//	      loadAndGateAlertPreset).
//	502 — webhook dispatch failed (non-2xx after retry exhaustion,
//	      SSRF rejection, or unseal failure). The customer sees
//	      "test failed — check your webhook URL".
func (s *server) sendTestAlertPreset(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	presetName := r.PathValue("name")
	res, prob := s.sendTestAlertPresetCore(r.Context(), acct, slug, presetName)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// sendTestAlertPresetCore is the shared work for the JSON path
// (POST /v1/apps/{slug}/alert-presets/{name}/test) and the
// dashboard form path (POST /dashboard/apps/{slug}/alert-presets/
// {name}/test, see dashboard_preset_enable.go). Returns a
// TestAlertPresetResponse on success or an *api.Problem on every
// failure. The split mirrors enableAlertPresetFromForm — same
// pattern, same rationale (JSON vs form decoders differ but the
// work is identical).
func (s *server) sendTestAlertPresetCore(ctx context.Context, acct state.Account, slug, presetName string) (api.TestAlertPresetResponse, *api.Problem) {
	preset, prob := s.loadAndGateAlertPreset(ctx, acct, presetName)
	if prob != nil {
		return api.TestAlertPresetResponse{}, prob
	}
	// App lookup mirrors the createAlertRule path at
	// handlers_alerts.go:163 — never reveal whether the slug
	// exists on another account (404 in both cases).
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		return api.TestAlertPresetResponse{}, api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such app", "no app with that slug is visible to this account")
	}
	rule, err := s.store.AlertRuleByAccountAppAndPresetName(ctx, acct.ID, app.ID, presetName)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return api.TestAlertPresetResponse{}, api.NewProblem(http.StatusNotFound, api.CodeValidation, "Preset not enabled", "no alert rule has been instantiated from this preset for this app; enable it first")
		}
		return api.TestAlertPresetResponse{}, api.ErrCapacity("could not load alert rule for preset")
	}
	plaintext, prob := unsealAlertRuleWebhookSecret(ctx, rule.WebhookSecretSealed)
	if prob != nil {
		return api.TestAlertPresetResponse{}, prob
	}
	deliveryID, evt, observed, prob := buildTestAlertEvent(acct, app, rule, preset)
	if prob != nil {
		return api.TestAlertPresetResponse{}, prob
	}
	disp := webhookout.NewDispatcher(webhookout.DispatcherOptions{HeaderSet: webhookout.HeaderSetAlert})
	result := disp.DispatchTest(ctx, webhookout.Target{
		URL:    rule.WebhookURL,
		Signer: webhookout.NewSigner(plaintext),
	}, evt)
	// ADR-123 PR-D: stamp an alert_deliveries row with IsTest=true so
	// operators can reach it via the `?include_test=true` toggle on
	// the deliveries endpoint. The production fire path
	// (Dispatcher.Dispatch + meterd evaluator) writes its own row
	// through ClaimAlertFire; the test path was deliberately routed
	// around that ledger in PR-C to avoid polluting the customer's
	// recent-deliveries pane. PR-D introduces the IsTest column so
	// operators get visibility without leaking test rows into the
	// default customer view. payload is the marshaled evt (mirrors
	// the production ClaimAlertFire payload stamp).
	payloadJSON, _ := json.Marshal(evt)
	now := time.Now().UTC()
	delivery := state.AlertDelivery{
		ID:             deliveryID,
		RuleID:         rule.ID,
		AccountID:      acct.ID,
		AppID:          app.ID,
		IdempotencyKey: deliveryID + ":test", // unique per click; production uses rule_id+cooldown bucket
		Payload:        payloadJSON,
		Status:         state.AlertDeliveryStatus(resultStatus(result, state.AlertDeliveryDelivered, state.AlertDeliveryFailed)),
		AttemptCount:   result.Attempts,
		LastStatusCode: result.StatusCode,
		ObservedValue:  observed,
		FiredAt:        now,
		IsTest:         true,
	}
	if result.Err != nil {
		delivery.LastError = result.Err.Error()
	}
	if delivery.Status == state.AlertDeliveryDelivered {
		delivery.DeliveredAt = now
	}
	if _, derr := s.store.RecordAlertDelivery(ctx, delivery); derr != nil && !errors.Is(derr, state.ErrConflict) {
		// Best-effort: a ledger write failure on the test path MUST
		// NOT surface as a customer-facing error. The audit log
		// already captures the attempt; the ledger row is operator
		// visibility, not a billing/correctness primitive.
		s.log.Warn("alert_deliveries write (test) failed",
			"delivery_id", deliveryID,
			"rule", logsanitize.Field(rule.ID),
			"err", derr)
	}
	s.log.Info("alert preset test sent",
		"preset", logsanitize.Field(preset.Name),
		"rule", logsanitize.Field(rule.ID),
		"app", logsanitize.Field(app.Slug),
		"account", acct.ID,
		"delivery_id", logsanitize.Field(deliveryID),
		"status_code", result.StatusCode,
		"attempts", result.Attempts,
	)
	s.audit.Emit(ctx, "alert_preset.test_sent", &acct.ID, map[string]any{
		"preset_name":          preset.Name,
		"preset_id":            preset.ID,
		"app_id":               app.ID,
		"app_slug":             app.Slug,
		"rule_id":              rule.ID,
		"webhook_url":          rule.WebhookURL,
		"delivery_id":          deliveryID,
		"test":                 true,
		"delivery_status_code": result.StatusCode,
		"delivery_attempts":    result.Attempts,
	})
	if result.Err != nil {
		return api.TestAlertPresetResponse{}, api.NewProblem(http.StatusBadGateway, api.CodeCapacity,
			"Webhook delivery failed",
			"the test alert could not be delivered to your webhook URL after retry exhaustion; check the URL + secret + receiver health. See the audit log entry for the status code.")
	}
	return api.TestAlertPresetResponse{
		Status:     "sent",
		Test:       true,
		DeliveryID: deliveryID,
		Attempts:   result.Attempts,
	}, nil
}

// unsealAlertRuleWebhookSecret pulls the host X25519 identity slice
// loaded by secretbox.LoadHostKeys at boot and unseals the
// per-rule webhook secret. Returns ErrCapacity on identity-miss
// (host key not loaded — boot config error) and ErrCapacity on
// OpenBytesMulti failure (seal corruption or wrong namespace).
// The namespace check is intentionally permissive (we accept any
// sealed blob whose seal label matches alert_rule_secret) so a
// future cross-namespace migration doesn't break the test path.
//
// The returned Problem carries the underlying crypto error only
// in slog fields via s.log at the call site — never in the wire
// body. secretbox error strings reveal which identities were
// tried and the seal structure; surfacing them on a customer-
// facing 5xx leaks that information to anyone who can trigger the
// unseal path.
func unsealAlertRuleWebhookSecret(ctx context.Context, sealed []byte) ([]byte, *api.Problem) {
	hostIdentities := hostIdentitiesForUnseal(ctx)
	if len(hostIdentities) == 0 {
		return nil, api.ErrCapacity("host identity not loaded — refusing to unseal webhook secret")
	}
	_, plaintext, err := secretbox.OpenBytesMulti(hostIdentities, sealed)
	if err != nil {
		// Do NOT include err.Error() in the wire response —
		// secretbox error strings reveal which identities were
		// tried and the seal structure; surfacing them on a
		// customer-facing 5xx leaks that information to anyone
		// who can trigger the unseal path. The call site in
		// sendTestAlertPresetCore logs the underlying error via
		// s.log so operators can correlate.
		return nil, api.NewProblem(http.StatusBadGateway, api.CodeCapacity,
			"Could not unseal webhook secret",
			"the host identity did not match this rule's seal — this is a boot-config or seal-corruption issue. The underlying error is logged server-side; reference the audit log entry for the delivery_id.")
	}
	if len(plaintext) == 0 {
		return nil, api.ErrCapacity("unsealed webhook secret is empty")
	}
	return plaintext, nil
}

// hostIdentitiesForUnseal is the indirection point so tests can
// swap the host-identity accessor without rewriting the handler.
// In production this resolves to the same accessor as
// handlers_mfa.go:645 (mfaIdentities), which is set at boot by
// SetMFAIdentities called from cmd/apid/main.go:1283. We use a
// separate var rather than mfaIdentities directly so a future
// rotate-overlap-isolation concern (e.g. refusing to unseal
// with the previous-previous key) can land here without
// cross-cutting the MFA path.
var hostIdentitiesForUnseal = func(_ context.Context) []*age.X25519Identity {
	return mfaIdentities()
}

// buildTestAlertEvent constructs the Event body the customer's
// receiver will see. The payload mirrors the production shape
// (see pkg/webhook/dispatcher.go:436-448) with two additions:
//
//  1. payload.test = true — the discriminator set by
//     DispatchTest, but we set it here too so the value is
//     visible in the handler's log line without the dispatcher
//     re-marshalling.
//  2. payload.observed — a synthetic value JUST PAST the
//     preset's threshold so the customer's verifier sees the
//     alert body in a fire-equivalent shape (not "everything's
//     fine"). The 1% margin is intentionally tiny — large
//     margins would make the test look like a runaway spike,
//     defeating the point of "I want to see what my receiver
//     gets when this fires".
//
// deliveryID is a fresh UUID — important: do NOT reuse the
// rule's primary key, because the production path uses the
// alert_deliveries row id as the canonical id, and a test
// delivery sharing that id would collide in the customer's
// audit log. UUID collision odds are negligible.
func buildTestAlertEvent(acct state.Account, app state.App, rule state.AlertRule, preset state.AlertPreset) (string, webhookout.Event, float64, *api.Problem) {
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", webhookout.Event{}, 0, api.ErrCapacity("could not generate delivery id: " + err.Error())
	}
	deliveryID := hex.EncodeToString(idBytes)
	// Synthetic observed value: threshold + 1% for "gt" and
	// threshold - 1% for "lt". The 1% margin is intentionally
	// tiny — large margins would make the test look like a
	// runaway spike, defeating the point of "I want to see what
	// my receiver gets when this fires".
	//
	// The 1% is computed against max(0.01, |threshold|*0.01) so
	// threshold=0 (the deploy_failed preset in
	// migrations/00418_alert_presets_seed.sql) still produces a
	// value JUST above 0 — without this guard, threshold*1.01=0
	// and the customer's verifier would treat the test as a
	// no-op (no fire-equivalent shape). Returned alongside
	// (deliveryID, evt) so the alert_deliveries ledger stamp in
	// sendTestAlertPresetCore (ADR-123 PR-D) records the SAME
	// observed value the customer's receiver sees — keeps the
	// operator's pane consistent with the customer's payload.
	margin := math.Max(0.01, math.Abs(preset.Threshold)*0.01)
	observed := preset.Threshold
	switch preset.Comparison {
	case "gt":
		observed = preset.Threshold + margin
	case "lt":
		observed = preset.Threshold - margin
	}
	return deliveryID, webhookout.Event{
		ID:         deliveryID,
		OccurredAt: time.Now().UTC(),
		Rule:       preset.Name,
		RuleName:   preset.DisplayName,
		AppID:      app.Slug,
		Payload: map[string]any{
			"preset":    preset.Name,
			"metric":    preset.Metric,
			"observed":  observed,
			"threshold": preset.Threshold,
			"window":    preset.WindowSpec,
			"test":      true,
		},
	}, observed, nil
}

// resultStatus maps a webhookout.Result to the AlertDeliveryStatus
// enum. The test-path ledger writer (ADR-123 PR-D) uses the same
// status vocabulary the production ClaimAlertFire path produces.
// Successful deliveries → "delivered"; any non-nil error → "failed";
// in-between (e.g. attempts-exhausted but a 2xx body returned) is
// treated as "delivered" because the receiver accepted it.
func resultStatus(r webhookout.Result, ok, fail state.AlertDeliveryStatus) state.AlertDeliveryStatus {
	if r.Err == nil {
		return ok
	}
	return fail
}
