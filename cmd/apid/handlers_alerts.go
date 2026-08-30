// handlers_alerts.go — apid handlers for customer-configurable alert
// rules (issue #396, ADR-045 PR 3).
//
// Routes (registered in server.go::handler):
//
//	GET    /v1/apps/{slug}/alerts                     → listAlertRules
//	POST   /v1/apps/{slug}/alerts                     → createAlertRule
//	GET    /v1/apps/{slug}/alerts/{id}                → getAlertRule
//	PATCH  /v1/apps/{slug}/alerts/{id}                → updateAlertRule
//	DELETE /v1/apps/{slug}/alerts/{id}                → deleteAlertRule
//	POST   /v1/apps/{slug}/alerts/{id}/rotate-secret  → rotateAlertRuleSecret
//
// Trust model
//
//   - Plaintext webhook_secret arrives in the request body and lives
//     transiently in this handler. It is sealed by pkg/secretbox.SealOne
//     against the host age recipient (setSecretRecipient, reused from
//     handlers_secrets.go:49) before it lands in PG. The plaintext NEVER
//     appears in a response, audit row, or log line — same posture as
//     pkg/api/secrets.go for app secrets.
//   - webhook_url is SSRF-guarded at write time via oci.EgressIPAllowed
//     (pkg/oci/egress.go:322). This is the first apid call site for
//     the egress guard; meterd (PR 4) re-validates on dispatch.
//   - Account-wide rules (AppID == "") appear in every per-app list
//     alongside app-scoped rules — same idempotent-visible surface as
//     the cron block at handlers_ext.go:758.
//   - Metric-family swaps (e.g. error_rate_pct → failed_invocations)
//     are rejected with 400 ErrAlertRuleInvalid because the xor_chk
//     constraint forbids rotating the failure_source half alone. The
//     customer-facing copy is "metric family cannot change; delete
//     and recreate".

package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// alertRuleWebhookSecretBytes is the size of the random plaintext
// minted by rotateAlertRuleSecret. 32 bytes is the HMAC-SHA256 key
// length, which is what webhookout.Signer expects (PR 4 meterd).
const alertRuleWebhookSecretBytes = 32

// alertRuleSecretSealLabel is the namespace string passed as the
// `key` argument to secretbox.SealOne so the age/X25519 footer is
// namespaced (not the same footer the app-secrets path uses — a
// rotated alert-rule secret and a rotated app secret must not be
// interchangeable if a future migration ever refactors the on-disk
// format). The label is never logged.
const alertRuleSecretSealLabel = "alert_rule_secret"

// alertRuleMetricFailedInvocations is the wire-level name of the
// `failed_invocations` metric family (the only metric in the alert
// vocabulary that carries a failure_source sibling — every other
// metric must have failure_source empty per the xor_chk DB
// constraint). Lifted out of the user-facing error messages and
// alertRuleFamily so the literal has a single source of truth and
// goconst stays happy.
const alertRuleMetricFailedInvocations = "failed_invocations"

// docsTypeBase is the canonical docs path prefix for problem
// `type:` URLs emitted by the apid alert handlers. Sourced from
// wire.DocsHost so a rotation only edits pkg/wire/docs.go.
var docsTypeBase = "https://" + wire.DocsHost + "/problems"

// --- list -------------------------------------------------------------------

// listAlertRules returns the rules visible to (acct, app). Per-app
// rules AND account-wide rules (AppID == "") are both included —
// account-wide rules apply to every app on the account, so the
// per-app listing carries them. Mirrors the per-app custom-domain
// surface (handlers_ext.go:758).
//
// 403/404 contract: missing app → 404 "no such app" (loadApp
// handles the IDOR-safe lookup). Unknown plan → 402 via
// ErrPlanAlertRulesNotAllowed (matches createAlertRule).
//
// Plan-tier gate fires BEFORE loadApp so a Free customer posting to
// a non-existent slug gets a clean 402 instead of a 404 (and the
// reverse — a Free customer on a real slug gets 402, not a 404
// masquerading as plan-gating). PR review finding F4: the gate
// ordering matters because the slug existence is itself a small
// information leak.
func (s *server) listAlertRules(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if limits, ok := api.LimitsFor(acct.Plan); !ok || limits.AlertRuleLimitPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanAlertRulesNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	rows, err := s.store.ListAlertRulesForAccount(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list alert rules"))
		return
	}
	out := make([]api.AlertRuleResponse, 0, len(rows))
	for _, row := range rows {
		if row.AppID != "" && row.AppID != app.ID {
			continue
		}
		out = append(out, alertRuleResponse(row))
	}
	writeJSON(w, http.StatusOK, out)
}

// --- create -----------------------------------------------------------------

// createAlertRule validates the body, SSRF-guards the webhook URL,
// seals the plaintext secret, and persists via
// CreateAlertRuleIfUnderQuota (atomic per-app + per-account cap).
//
// Phase order (validate → plan gate → load app → validate body →
// SSRF guard → family check → seal → quota → persist → audit → log
// → respond) matches createCron (handlers_ext.go:688) and setSecret
// (handlers_secrets.go:91). Each phase returns a ready-to-write
// *api.Problem so the handler itself reads as a sequence of guards.
//
// CodeQL go/log-injection (alert #117): every interpolated user
// string is run through logsanitize.RedactValue + a two-call
// strings.ReplaceAll(CR) / ReplaceAll(LF) pattern before reaching
// slog. Audit payloads are structured maps, never strings.
func (s *server) createAlertRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateAlertRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	// Trim the name BEFORE plan/AppID resolution so the validator
	// sees the trimmed value AND the persisted row doesn't carry a
	// leading/trailing-whitespace variant of the same string. PR
	// review finding F3.
	trimmed, ok := api.TrimNonEmpty(req.Name)
	if !ok {
		api.WriteProblem(w, api.ErrAlertRuleInvalid("name must be non-empty"))
		return
	}
	req.Name = trimmed
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
	if prob := validateAlertRuleBody(req); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// FailureSource family check: failed_invocations needs a
	// non-empty failure_source; every other metric needs empty.
	// Mirrors the DB alert_rules_failure_source_xor_chk constraint.
	if prob := validateFailureSourceFamily(req); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := resolveAndCheckEgress(r.Context(), req.WebhookURL); prob != nil {
		api.WriteProblem(w, prob)
		return
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
	row, err := s.store.CreateAlertRuleIfUnderQuota(r.Context(), state.AlertRule{
		AccountID:           acct.ID,
		AppID:               app.ID,
		Name:                req.Name,
		Enabled:             alertRuleEnabledFrom(req.Enabled),
		Metric:              state.AlertMetric(req.Metric),
		Comparison:          state.AlertComparison(req.Comparison),
		Threshold:           req.Threshold,
		WindowSpec:          state.AlertWindowSpec(req.WindowSpec),
		FailureSource:       state.AlertFailureSource(req.FailureSource),
		Action:              alertRuleActionFrom(req.Action),
		WebhookURL:          req.WebhookURL,
		WebhookSecretSealed: sealed,
		CooldownMinutes:     alertRuleCooldownFrom(req.CooldownMinutes),
	}, limits)
	if err != nil {
		var qe *state.AlertRuleQuotaError
		switch {
		case errors.As(err, &qe):
			api.WriteProblem(w, api.ErrPlanAlertRuleQuota(acct.Plan, string(qe.Scope), qe.Limit, qe.Observed))
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such app")
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Rule name already exists", "an alert rule with this name already exists for this app; pick a unique name"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not create alert rule"))
		}
		return
	}
	s.log.Info("alert rule created",
		"rule", logsanitize.Field(row.ID),
		"app", app.Slug,
		"account", acct.ID,
		// CodeQL go/log-injection (alert #126): row.Metric is closed-set
		// validated before insert (validateAlertRuleBody calls
		// api.AllowedAlertRuleMetric), so it can never contain CR/LF,
		// but the static analyser can't see the closed-set guard.
		// Sanitise anyway so the log line stays one-line-per-event and
		// the alert gets dismissed without a // codeql[go/log-injection]
		// suppression comment (precedent: pkg/logsanitize.Field on
		// every user-influenced attribute).
		"metric", logsanitize.Field(string(row.Metric)),
	)
	// IAM-4 (ADR-035): audit the rule creation. Mirrors
	// cron.created (handlers_ext.go:748). NEVER carry the plaintext
	// secret or its sealed ciphertext — both would leak the same
	// material (the sealed value is decryptable by anyone with the
	// host key) and only the masked constant is safe.
	s.audit.Emit(r.Context(), "alert_rule.created", &acct.ID, map[string]any{
		"rule_id":          row.ID,
		"app_id":           row.AppID,
		"name":             row.Name,
		"metric":           row.Metric,
		"comparison":       row.Comparison,
		"threshold":        row.Threshold,
		"window_spec":      row.WindowSpec,
		"failure_source":   row.FailureSource,
		"webhook_url":      row.WebhookURL,
		"enabled":          row.Enabled,
		"cooldown_minutes": row.CooldownMinutes,
	})
	writeJSON(w, http.StatusCreated, alertRuleResponse(row))
}

// --- get --------------------------------------------------------------------

// getAlertRule resolves the rule + verifies the customer owns it.
// AlertRuleByID does NOT filter by account, so the IDOR check is
// load-bearing — a stolen API key must not be able to read a
// foreign account's rule by id. Mirrors handlers_ext.go:789.
func (s *server) getAlertRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	row, err := s.store.AlertRuleByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such alert rule")
		return
	}
	if row.AccountID != acct.ID {
		s.notFound(w, "no such alert rule")
		return
	}
	// Cross-account app check (only when the rule is app-scoped).
	// Account-wide rules (AppID == "") skip this — they don't have
	// an app to verify against.
	if row.AppID != "" {
		app, err := s.store.AppByID(r.Context(), row.AppID)
		if err != nil || app.AccountID != acct.ID {
			s.notFound(w, "no such alert rule")
			return
		}
	}
	writeJSON(w, http.StatusOK, alertRuleResponse(row))
}

// --- list deliveries -------------------------------------------------------

// listAlertRuleDeliveries (ADR-123 PR-D) returns the recent
// alert_deliveries rows for one rule, newest-first, capped at
// limit (default 50, max 100 — matches the dashboard's
// fetchDashboardAlerts limit clamp).
//
// ?include_test=true (default false): when true, surfaces the rows
// written by Dispatcher.DispatchTest. The default hides them so
// the customer's recent-deliveries pane is not polluted by every
// "send test alert" click. Operators can flip the toggle on for
// post-mortems; the production hot path stays index-only via the
// partial index alert_deliveries_rule_fired_production_idx
// (migrations/00528).
//
// Auth: same scope as getAlertRule (read surface). The IDOR check
// uses AlertRuleByID + an AccountID match — mirroring getAlertRule
// line-by-line so a customer cannot probe another account's
// delivery ledger by guessing rule IDs.
func (s *server) listAlertRuleDeliveries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	row, err := s.store.AlertRuleByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such alert rule")
		return
	}
	if row.AccountID != acct.ID {
		s.notFound(w, "no such alert rule")
		return
	}
	if row.AppID != "" {
		app, err := s.store.AppByID(r.Context(), row.AppID)
		if err != nil || app.AccountID != acct.ID {
			s.notFound(w, "no such alert rule")
			return
		}
	}
	includeTest, _ := strconv.ParseBool(r.URL.Query().Get("include_test"))
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be a positive integer"))
			return
		}
		if n > 100 {
			n = 100
		}
		limit = n
	}
	rows, err := s.store.ListAlertDeliveriesForRule(r.Context(), id, limit, includeTest)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list alert deliveries"))
		return
	}
	out := make([]api.AlertDeliveryResponse, 0, len(rows))
	for _, d := range rows {
		out = append(out, alertDeliveryResponse(d))
	}
	writeJSON(w, http.StatusOK, out)
}

// --- update -----------------------------------------------------------------

// updateAlertRule applies a partial update. Pointer-everything
// optionals let the handler distinguish "omitted" from "zero" —
// the partial-update contract is the same shape as
// state.UpdateAlertRuleParams (pkg/state/types.go:519).
//
// Metric-family swap is rejected with 400 ErrAlertRuleInvalid:
// rotating the metric from error_rate_pct to failed_invocations
// would silently clear failure_source (xor_chk) and the customer
// would see "firing" for the wrong metric. Force them to delete
// and recreate.
func (s *server) updateAlertRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.UpdateAlertRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	id := r.PathValue("id")
	row, err := s.store.AlertRuleByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such alert rule")
		return
	}
	if row.AccountID != acct.ID {
		s.notFound(w, "no such alert rule")
		return
	}
	if row.AppID != "" {
		app, err := s.store.AppByID(r.Context(), row.AppID)
		if err != nil || app.AccountID != acct.ID {
			s.notFound(w, "no such alert rule")
			return
		}
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.AlertRuleLimitPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanAlertRulesNotAllowed(acct.Plan))
		return
	}
	// Build the merged state row for body validation so the
	// closed-set + threshold + cooldown checks see the post-patch
	// shape, not just the partial request.
	merged := alertRuleRowForValidation(row, req)
	if prob := validateAlertRuleRowUpdate(merged); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// Metric-family swap rejection. "Family" = the xor_chk partition:
	// failed_invocations needs failure_source set; everything else
	// needs it empty. Crossing that line in PATCH silently drops
	// either the source selector or the source itself; refuse.
	if req.Metric != nil {
		newMetric := state.AlertMetric(*req.Metric)
		oldFamily := alertRuleFamily(row.Metric)
		newFamily := alertRuleFamily(newMetric)
		if oldFamily != newFamily {
			api.WriteProblem(w, api.ErrAlertRuleInvalid("metric family cannot change; delete and recreate"))
			return
		}
	}
	// Optional URL re-guard.
	if req.WebhookURL != nil {
		if prob := resolveAndCheckEgress(r.Context(), *req.WebhookURL); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
	}
	// Optional secret re-seal.
	var sealedPtr *[]byte
	if req.WebhookSecret != nil {
		recipient := setSecretRecipient()
		if recipient == nil {
			api.WriteProblem(w, api.ErrCapacity("host age recipient not loaded — refusing to seal webhook secret"))
			return
		}
		sealed, err := secretbox.SealBytes(recipient, alertRuleSecretSealLabel, []byte(*req.WebhookSecret), api.AlertRuleWebhookSecretMaxBytes)
		if err != nil {
			if prob := api.AsProblem(err); prob != nil {
				api.WriteProblem(w, prob)
				return
			}
			api.WriteProblem(w, api.ErrCapacity("could not seal webhook secret"))
			return
		}
		sealedPtr = &sealed
	}
	updated, err := s.store.UpdateAlertRule(r.Context(), id, state.UpdateAlertRuleParams{
		Name:                req.Name,
		Enabled:             req.Enabled,
		Metric:              ptrAlertMetric(req.Metric),
		Comparison:          ptrAlertComparison(req.Comparison),
		Threshold:           req.Threshold,
		WindowSpec:          ptrAlertWindowSpec(req.WindowSpec),
		Action:              req.Action,
		WebhookURL:          req.WebhookURL,
		WebhookSecretSealed: sealedPtr,
		CooldownMinutes:     req.CooldownMinutes,
	})
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such alert rule")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not update alert rule"))
		return
	}
	s.log.Info("alert rule updated",
		"rule", logsanitize.Field(updated.ID),
		"app", updated.AppID,
		"account", acct.ID,
	)
	// IAM-4 (ADR-035): audit what the customer altered and to what.
	// Only the fields actually sent (req.X != nil) appear in the
	// old/new maps so a name-only patch does not carry
	// `threshold` on either side.
	oldMap := map[string]any{}
	newMap := map[string]any{}
	if req.Name != nil {
		oldMap["name"] = row.Name
		newMap["name"] = updated.Name
	}
	if req.Enabled != nil {
		oldMap["enabled"] = row.Enabled
		newMap["enabled"] = updated.Enabled
	}
	if req.Metric != nil {
		oldMap["metric"] = row.Metric
		newMap["metric"] = updated.Metric
	}
	if req.Comparison != nil {
		oldMap["comparison"] = row.Comparison
		newMap["comparison"] = updated.Comparison
	}
	if req.Threshold != nil {
		oldMap["threshold"] = row.Threshold
		newMap["threshold"] = updated.Threshold
	}
	if req.WindowSpec != nil {
		oldMap["window_spec"] = row.WindowSpec
		newMap["window_spec"] = updated.WindowSpec
	}
	if req.WebhookURL != nil {
		oldMap["webhook_url"] = row.WebhookURL
		newMap["webhook_url"] = updated.WebhookURL
	}
	if req.WebhookSecret != nil {
		// We never log or audit the plaintext. The audit row only
		// records the fact that a rotation occurred (matching the
		// separate rotate-secret audit kind, which is reserved for
		// server-minted rotations).
		oldMap["webhook_secret_sealed"] = "rotated"
		newMap["webhook_secret_sealed"] = "rotated"
	}
	if req.CooldownMinutes != nil {
		oldMap["cooldown_minutes"] = row.CooldownMinutes
		newMap["cooldown_minutes"] = updated.CooldownMinutes
	}
	s.audit.Emit(r.Context(), "alert_rule.updated", &acct.ID, map[string]any{
		"rule_id": updated.ID,
		"app_id":  updated.AppID,
		"old":     oldMap,
		"new":     newMap,
	})
	writeJSON(w, http.StatusOK, alertRuleResponse(updated))
}

// --- delete -----------------------------------------------------------------

// deleteAlertRule removes the row after the same IDOR + cross-app
// checks as get / update. The handler captures the rule's name +
// app id BEFORE the delete so the audit row carries something
// useful — after the row is gone, AlertRuleByID returns 404 and the
// audit row would be empty.
func (s *server) deleteAlertRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	row, err := s.store.AlertRuleByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such alert rule")
		return
	}
	if row.AccountID != acct.ID {
		s.notFound(w, "no such alert rule")
		return
	}
	if row.AppID != "" {
		app, err := s.store.AppByID(r.Context(), row.AppID)
		if err != nil || app.AccountID != acct.ID {
			s.notFound(w, "no such alert rule")
			return
		}
	}
	if err := s.store.DeleteAlertRule(r.Context(), id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such alert rule")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete alert rule"))
		return
	}
	s.log.Info("alert rule deleted",
		"rule", logsanitize.Field(id),
		"app", row.AppID,
		"account", acct.ID,
	)
	// IAM-4 (ADR-035): record the rule deletion. Pair of
	// .created + .deleted, matching the cron family at
	// handlers_ext.go:855.
	s.audit.Emit(r.Context(), "alert_rule.deleted", &acct.ID, map[string]any{
		"rule_id": id,
		"app_id":  row.AppID,
		"name":    row.Name,
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- rotate-secret ----------------------------------------------------------

// rotateAlertRuleSecret mints a fresh 32-byte secret via
// crypto/rand, base64-encodes it, seals via the host age recipient,
// and overwrites the row's webhook_secret_sealed in place. No
// secret_version column exists (issue #396 plan) — rotation is
// overwrite-in-place. The plaintext NEVER appears in the response,
// audit row, or log line.
//
// The plaintext lives for the duration of the handler call: mint →
// seal → overwrite → return masked constant. At no point is it
// logged or persisted as plaintext. The 256-byte byte cap on
// SealOne is the base64-expanded limit; the 32-byte raw secret
// base64-encodes to 44 bytes which is comfortably under.
func (s *server) rotateAlertRuleSecret(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	row, err := s.store.AlertRuleByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such alert rule")
		return
	}
	if row.AccountID != acct.ID {
		s.notFound(w, "no such alert rule")
		return
	}
	if row.AppID != "" {
		app, err := s.store.AppByID(r.Context(), row.AppID)
		if err != nil || app.AccountID != acct.ID {
			s.notFound(w, "no such alert rule")
			return
		}
	}
	plaintext, err := mintAlertRuleSecret(alertRuleWebhookSecretBytes)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not mint webhook secret"))
		return
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		api.WriteProblem(w, api.ErrCapacity("host age recipient not loaded — refusing to seal webhook secret"))
		return
	}
	sealed, err := secretbox.SealBytes(recipient, alertRuleSecretSealLabel, plaintext, api.AlertRuleWebhookSecretMaxBytes)
	if err != nil {
		if prob := api.AsProblem(err); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not seal webhook secret"))
		return
	}
	if _, err := s.store.UpdateAlertRule(r.Context(), id, state.UpdateAlertRuleParams{
		WebhookSecretSealed: &sealed,
	}); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such alert rule")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not rotate webhook secret"))
		return
	}
	now := time.Now().UTC()
	s.log.Info("alert rule secret rotated",
		"rule", logsanitize.Field(id),
		"app", row.AppID,
		"account", acct.ID,
	)
	// IAM-4 (ADR-035): audit the rotation event. Mirror of
	// secret.rotated — the audit row carries the rule id, the
	// rotated_at timestamp, and the fact that a rotation occurred.
	// NO plaintext, no secret_version (the column does not exist),
	// no sealed ciphertext (decryptable by anyone with the host
	// key). PR review finding F11: audit row now carries the
	// timestamp so the dashboard can render "last rotated 6h ago"
	// without a separate query.
	s.audit.Emit(r.Context(), "alert_rule.secret_rotated", &acct.ID, map[string]any{
		"rule_id":    id,
		"app_id":     row.AppID,
		"rotated_at": now.Format(time.RFC3339),
	})
	writeJSON(w, http.StatusOK, api.RotateAlertRuleSecretResponse{
		RotatedAt:                 now.Format(time.RFC3339),
		WebhookSecretSealedMasked: api.AlertRuleWebhookSecretMasked,
	})
	// Defensive wipe: scrub the plaintext from the local stack
	// frame as soon as the handler returns. Go's escape analysis
	// usually allocates on the heap, but the explicit zero-byte
	// loop costs nothing and removes one ambiguity from a future
	// reviewer wondering whether the plaintext survived into a
	// closure capture.
	for i := range plaintext {
		plaintext[i] = 0
	}
}

// --- helpers ----------------------------------------------------------------

// alertRuleResponse maps a state.AlertRule to the wire DTO. Drops
// the sealed ciphertext; renders the masked constant. The
// conversion from typed state.* enums to plain strings happens
// here at the pkg/api ↔ pkg/state boundary so pkg/api/alerts.go
// stays free of the import cycle.
func alertRuleResponse(r state.AlertRule) api.AlertRuleResponse {
	return api.AlertRuleResponseFromRow(api.AlertRuleRow{
		ID:              r.ID,
		AppID:           r.AppID,
		Name:            r.Name,
		Enabled:         r.Enabled,
		Metric:          string(r.Metric),
		Comparison:      string(r.Comparison),
		Threshold:       r.Threshold,
		WindowSpec:      string(r.WindowSpec),
		FailureSource:   string(r.FailureSource),
		Action:          string(r.Action),
		WebhookURL:      r.WebhookURL,
		CooldownMinutes: r.CooldownMinutes,
		State:           string(r.State),
		LastFiredAt:     r.LastFiredAt,
		LastEvaluatedAt: r.LastEvaluatedAt,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	})
}

// alertDeliveryResponse maps a state.AlertDelivery row onto the
// public api.AlertDeliveryResponse. Truncates LastError via the
// dashboard's formatter so the response is log-injection-safe —
// same precedent as handlers_dashboard.go:756-763.
func alertDeliveryResponse(d state.AlertDelivery) api.AlertDeliveryResponse {
	lastErr := d.LastError
	if lastErr != "" {
		lastErr = dashboard.FormatAlertError(lastErr)
	}
	return api.AlertDeliveryResponse{
		ID:             d.ID,
		RuleID:         d.RuleID,
		AccountID:      d.AccountID,
		AppID:          d.AppID,
		IdempotencyKey: d.IdempotencyKey,
		Status:         string(d.Status),
		AttemptCount:   d.AttemptCount,
		LastStatusCode: d.LastStatusCode,
		LastError:      lastErr,
		ObservedValue:  d.ObservedValue,
		FiredAt:        d.FiredAt,
		DeliveredAt:    d.DeliveredAt,
		IsTest:         d.IsTest,
	}
}

// alertRuleEnabledFrom returns the create-request's enabled flag
// or true (default) when omitted.
func alertRuleEnabledFrom(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

// alertRuleCooldownFrom returns the create-request's cooldown or
// the public default (AlertRuleDefaultCooldownMinutes = 15) when
// omitted. Same default as the DB-side fallback if the column
// defaults to 15 (issue #396 plan).
func alertRuleCooldownFrom(p *int) int {
	if p == nil {
		return api.AlertRuleDefaultCooldownMinutes
	}
	return *p
}

// alertRuleActionFrom returns the create-request's action or
// AlertActionWebhook (the legacy Dispatcher fan-out) when omitted.
// Issue #976 / ADR-122 / SAFE-RELEASES-B. Validator (above) has
// already enforced pkg/api.AllowedAlertRuleActions membership on
// non-nil values, so this helper never has to.
func alertRuleActionFrom(p *string) state.AlertAction {
	if p == nil {
		return state.AlertActionWebhook
	}
	return state.AlertAction(*p)
}

// validateAlertRuleBody is the create-side validator. Closed-vocab
// checks (metric / comparison / window_spec / failure_source) +
// threshold finite + cooldown band + secret size + URL parse.
// SSRF-resolve is handled separately by resolveAndCheckEgress so the
// caller can run the body check BEFORE the network lookup. Returns
// nil on success, a ready-to-write *api.Problem on failure.
//
// The caller (createAlertRule) is responsible for trimming the name
// before calling this validator; the validator assumes a non-empty
// trimmed name. The same trim-non-empty check is mirrored on the
// update path via state.AlertRule.Name (the DB schema enforces
// non-empty via CHECK).
func validateAlertRuleBody(req api.CreateAlertRuleRequest) *api.Problem {
	if !api.AllowedAlertRuleMetric(req.Metric) {
		return api.ErrAlertRuleInvalid(fmt.Sprintf("metric must be one of error_rate_pct, latency_p50_ms, latency_p95_ms, latency_p99_ms, cold_start_pct, request_count, %s", alertRuleMetricFailedInvocations))
	}
	if !api.AllowedAlertRuleComparison(req.Comparison) {
		return api.ErrAlertRuleInvalid("comparison must be one of gt, gte, lt, lte")
	}
	if !api.AllowedAlertRuleWindowSpec(req.WindowSpec) {
		return api.ErrAlertRuleInvalid("window_spec must be one of 5m, 15m, 1h, 6h, 24h, 7d, 15d")
	}
	if req.FailureSource != "" && !api.AllowedAlertRuleFailureSource(req.FailureSource) {
		return api.ErrAlertRuleInvalid("failure_source must be one of any, cron, queue, delayed_task, async_invoke (or empty)")
	}
	// Issue #976 / ADR-122 / SAFE-RELEASES-B: Action is the
	// new seam-routing field on alert_rules. nil on the wire
	// defaults to 'webhook' (legacy Dispatcher fan-out) so
	// every pre-PR body stays byte-identical. A non-nil value
	// must be in pkg/api.AllowedAlertRuleActions; the schema
	// alert_rules_action_chk is the second-line defence.
	if req.Action != nil && !api.AllowedAlertRuleAction(*req.Action) {
		return api.ErrAlertRuleInvalid(fmt.Sprintf("action must be one of %v (or omitted for webhook)", api.AllowedAlertRuleActions))
	}
	if !api.IsFiniteFloat(req.Threshold) {
		return api.ErrAlertRuleInvalid("threshold must be a finite number")
	}
	if req.CooldownMinutes != nil {
		if *req.CooldownMinutes < api.AlertRuleCooldownMinMinutes || *req.CooldownMinutes > api.AlertRuleCooldownMaxMinutes {
			return api.ErrAlertRuleInvalid(fmt.Sprintf("cooldown_minutes must be in [%d, %d]", api.AlertRuleCooldownMinMinutes, api.AlertRuleCooldownMaxMinutes))
		}
	}
	if len(req.WebhookSecret) == 0 {
		return api.ErrAlertRuleInvalid("webhook_secret must be non-empty")
	}
	// Cap on raw bytes (UTF-8) — matches what SealBytes will
	// actually encrypt. Counting characters (runes) would let a
	// multi-byte payload pass the validator and then fail at the
	// seal boundary. PR review finding F5: byte vs rune.
	if len(req.WebhookSecret) > api.AlertRuleWebhookSecretMaxBytes {
		return api.ErrAlertRuleInvalid(fmt.Sprintf("webhook_secret exceeds %d-byte cap", api.AlertRuleWebhookSecretMaxBytes))
	}
	if _, err := url.ParseRequestURI(req.WebhookURL); err != nil {
		return api.ErrAlertRuleInvalid("webhook_url is not a valid URL")
	}
	if !strings.HasPrefix(req.WebhookURL, "https://") && !strings.HasPrefix(req.WebhookURL, "http://") {
		return api.ErrAlertRuleInvalid("webhook_url must be http(s)")
	}
	return nil
}

// validateAlertRuleRowUpdate is the update-side validator. The
// merged row (existing + request) is checked against the same
// closed-vocab + threshold + cooldown + secret-size rules as the
// create path. Cooldown is optional on update so a nil merge is
// fine. Secret is also optional: nil means "don't reseal".
func validateAlertRuleRowUpdate(merged state.AlertRule) *api.Problem {
	if _, ok := api.TrimNonEmpty(merged.Name); !ok {
		return api.ErrAlertRuleInvalid("name must be non-empty")
	}
	if !api.AllowedAlertRuleMetric(string(merged.Metric)) {
		return api.ErrAlertRuleInvalid(fmt.Sprintf("metric must be one of error_rate_pct, latency_p50_ms, latency_p95_ms, latency_p99_ms, cold_start_pct, request_count, %s", alertRuleMetricFailedInvocations))
	}
	if !api.AllowedAlertRuleComparison(string(merged.Comparison)) {
		return api.ErrAlertRuleInvalid("comparison must be one of gt, gte, lt, lte")
	}
	if !api.AllowedAlertRuleWindowSpec(string(merged.WindowSpec)) {
		return api.ErrAlertRuleInvalid("window_spec must be one of 5m, 15m, 1h, 6h, 24h, 7d, 15d")
	}
	if merged.FailureSource != "" && !api.AllowedAlertRuleFailureSource(string(merged.FailureSource)) {
		return api.ErrAlertRuleInvalid("failure_source must be one of any, cron, queue, delayed_task, async_invoke (or empty)")
	}
	// Issue #976 / ADR-122 / SAFE-RELEASES-B: Action must
	// round-trip through the merged row check too, so a PATCH
	// can't sneak an out-of-set value past the create-path
	// validator.
	if merged.Action != "" && !api.AllowedAlertRuleAction(string(merged.Action)) {
		return api.ErrAlertRuleInvalid(fmt.Sprintf("action must be one of %v (or empty for webhook)", api.AllowedAlertRuleActions))
	}
	if !api.IsFiniteFloat(merged.Threshold) {
		return api.ErrAlertRuleInvalid("threshold must be a finite number")
	}
	if merged.CooldownMinutes < api.AlertRuleCooldownMinMinutes || merged.CooldownMinutes > api.AlertRuleCooldownMaxMinutes {
		return api.ErrAlertRuleInvalid(fmt.Sprintf("cooldown_minutes must be in [%d, %d]", api.AlertRuleCooldownMinMinutes, api.AlertRuleCooldownMaxMinutes))
	}
	if _, err := url.ParseRequestURI(merged.WebhookURL); err != nil {
		return api.ErrAlertRuleInvalid("webhook_url is not a valid URL")
	}
	if !strings.HasPrefix(merged.WebhookURL, "https://") && !strings.HasPrefix(merged.WebhookURL, "http://") {
		return api.ErrAlertRuleInvalid("webhook_url must be http(s)")
	}
	return nil
}

// alertRuleRowForValidation folds an existing row + a partial update
// request into a merged state.AlertRule suitable for the update
// validator. Used by validateAlertRuleRowUpdate so the validator
// sees the post-patch shape, not just the partial request.
func alertRuleRowForValidation(existing state.AlertRule, req api.UpdateAlertRuleRequest) state.AlertRule {
	merged := existing
	if req.Name != nil {
		merged.Name = *req.Name
	}
	if req.Enabled != nil {
		merged.Enabled = *req.Enabled
	}
	if req.Metric != nil {
		merged.Metric = state.AlertMetric(*req.Metric)
	}
	if req.Comparison != nil {
		merged.Comparison = state.AlertComparison(*req.Comparison)
	}
	if req.Threshold != nil {
		merged.Threshold = *req.Threshold
	}
	if req.WindowSpec != nil {
		merged.WindowSpec = state.AlertWindowSpec(*req.WindowSpec)
	}
	// Issue #976 / ADR-122 / SAFE-RELEASES-B: merge Action
	// through so the row-update validator sees the post-patch
	// shape (mirrors Metric / Comparison / WindowSpec).
	if req.Action != nil {
		merged.Action = state.AlertAction(*req.Action)
	}
	if req.WebhookURL != nil {
		merged.WebhookURL = *req.WebhookURL
	}
	if req.CooldownMinutes != nil {
		merged.CooldownMinutes = *req.CooldownMinutes
	}
	return merged
}

// validateFailureSourceFamily enforces the xor_chk constraint at
// the API boundary so a customer submitting
// {metric: failed_invocations, failure_source: ""} gets a clean
// 400 instead of a 503 with the underlying constraint-violation
// error from the DB.
func validateFailureSourceFamily(req api.CreateAlertRuleRequest) *api.Problem {
	if req.Metric == string(state.AlertMetricFailedInvocs) {
		if req.FailureSource == "" {
			return api.ErrAlertRuleInvalid("failure_source must be set when metric is failed_invocations")
		}
	} else {
		if req.FailureSource != "" {
			return api.ErrAlertRuleInvalid("failure_source must be empty when metric is not failed_invocations")
		}
	}
	return nil
}

// alertRuleFamily partitions the metric vocabulary into the two
// xor_chk families: failed_invocations needs failure_source set;
// every other metric needs it empty. Used by the metric-family
// swap check in updateAlertRule.
func alertRuleFamily(m state.AlertMetric) string {
	if m == state.AlertMetricFailedInvocs {
		return alertRuleMetricFailedInvocations
	}
	return "other"
}

// resolveAndCheckEgress parses the URL, resolves the host via the
// system resolver, and runs every returned IP through
// oci.EgressIPAllowed. ANY denied IP short-circuits to 403
// image_egress_denied (CodeQL go/log-injection: ErrAlertRuleInvalid
// is for body validation, not network policy — 403 is correct for
// SSRF).
//
// Mirror of pkg/oci.EgressDialContext's resolve-then-check loop;
// pre-validating here stops the customer from configuring a webhook
// URL we know the dispatcher (PR 4) would never be able to reach.
// Defense in depth: meterd re-validates on every dispatch in case
// the IP set rotates between create-time and fire-time.
//
// PR review finding F6 noted that we use net.DefaultResolver here
// rather than an egress-aware resolver. pkg/oci does not yet expose
// a standalone resolver handle (EgressDialContext wraps a net.Dialer
// and the resolver lives inside that). The dispatch-time re-check
// is the load-bearing layer — the create-time check is a fast-path
// that catches obvious misconfigurations. We accept the small
// resolver-discrepancy window because the dispatcher re-validates
// every fire using the same oci.EgressIPAllowed predicate.
func resolveAndCheckEgress(c context.Context, rawURL string) *api.Problem {
	u, err := url.Parse(rawURL)
	if err != nil {
		return api.ErrAlertRuleInvalid("webhook_url is not a valid URL")
	}
	host := u.Hostname()
	if host == "" {
		return api.ErrAlertRuleInvalid("webhook_url must include a host")
	}
	// Resolve. If the host doesn't resolve at create-time we
	// don't 403 — the customer might be staging a rule that fires
	// later when DNS is back. The dispatcher re-resolves on every
	// fire and will 503 the dispatch if the IP is still missing.
	//
	// The LookupIPAddr error is intentionally discarded (the
	// customer-facing posture is "missing DNS isn't a validation
	// failure, it's a future-fire failure"); nilerr is satisfied
	// because we only return nil on the no-IPs path, never on a
	// non-nil err path. The error path falls through to the
	// IP-iteration loop where len(ips)==0 means "nothing to check,
	// caller proceeds".
	ips, _ := net.DefaultResolver.LookupIPAddr(c, host)
	if len(ips) == 0 {
		return nil
	}
	for _, ipa := range ips {
		addr, ok := netip.AddrFromSlice(ipa.IP)
		if !ok {
			continue
		}
		if !oci.EgressIPAllowed(addr.Unmap()) {
			return &api.Problem{
				Status: 403,
				Type:   docsTypeBase + "/image-egress-denied",
				Title:  "Egress denied",
				Detail: fmt.Sprintf("webhook_url resolves to %s, which is in the egress denylist", ipa.IP),
				Code:   api.CodeImageEgressDenied,
			}
		}
	}
	return nil
}

// mintAlertRuleSecret returns a base64-encoded random secret of
// the requested byte length. The plaintext lifetime is the
// handler call; the caller is responsible for zeroing the byte
// slice before returning.
func mintAlertRuleSecret(byteLen int) ([]byte, error) {
	raw := make([]byte, byteLen)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	encoded := make([]byte, base64.StdEncoding.EncodedLen(byteLen))
	base64.StdEncoding.Encode(encoded, raw)
	return encoded, nil
}

// ptrAlertMetric / ptrAlertComparison / ptrAlertWindowSpec convert
// the *string DTO fields to *state.AlertMetric / *state.AlertComparison
// / *state.AlertWindowSpec. nil in → nil out; non-nil string in →
// typed pointer out. The chain keeps pkg/api free of the typed
// enums at the DTO layer.
func ptrAlertMetric(p *string) *state.AlertMetric {
	if p == nil {
		return nil
	}
	v := state.AlertMetric(*p)
	return &v
}

func ptrAlertComparison(p *string) *state.AlertComparison {
	if p == nil {
		return nil
	}
	v := state.AlertComparison(*p)
	return &v
}

func ptrAlertWindowSpec(p *string) *state.AlertWindowSpec {
	if p == nil {
		return nil
	}
	v := state.AlertWindowSpec(*p)
	return &v
}
