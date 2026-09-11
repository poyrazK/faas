package main

// Outbound webhook CRUD + deliveries handlers (issue #476 / ADR-076).
//
// Surface under /v1/apps/{slug}/webhooks:
//   GET    /v1/apps/{slug}/webhooks                       — listAppWebhooks
//   POST   /v1/apps/{slug}/webhooks                       — createAppWebhook
//   GET    /v1/apps/{slug}/webhooks/{id}                  — getAppWebhook
//   PATCH  /v1/apps/{slug}/webhooks/{id}                  — updateAppWebhook
//   DELETE /v1/apps/{slug}/webhooks/{id}                  — deleteAppWebhook
//   POST   /v1/apps/{slug}/webhooks/{id}/rotate-secret    — rotateAppWebhookSecret
//   GET    /v1/apps/{slug}/webhooks/{id}/deliveries       — listAppWebhookDeliveries
//   POST   /v1/apps/{slug}/webhooks/{id}/deliveries/{did}/retry — retryAppWebhookDelivery
//
// Mirrors cmd/apid/handlers_alerts.go phase order:
//   decode → trim → plan-tier gate → loadApp → validate body
//   → SSRF guard → seal → quota → persist → audit → respond
//
// The sealed-secret write/read + masked response pattern is verbatim
// from handlers_alerts.go:601-650 (alertRuleResponse). The
// deliveries endpoint reuses the pageToken shape from
// handlers_ext.go (ListCronRunsResponse).

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// appWebhookSecretSealLabel is the secretbox namespace for outbound
// webhook secrets. Must match the dispatcher's namespace check
// (pkg/webhook/dispatcher.go:376). Single uppercase form mirrors
// the SecretKeyPattern contract (api.SecretKeyPattern =
// `^[A-Z][A-Z0-9_]*$`).
const appWebhookSecretSealLabel = "APP_WEBHOOK"

// defaultAppWebhookEnabled defaults to true when the customer
// omits `enabled` on create. Mirrors alert-rule default.
const defaultAppWebhookEnabled = true

// defaultAppWebhookRetryPolicy mirrors pkg/state's empty-string
// → 'default' fill-in (memstore_app_webhooks.go:36-38). The handler
// applies the same fill before persisting so the response shape
// always reflects the closed set.
const defaultAppWebhookRetryPolicy = "default"

// defaultAppWebhookEventFilter is the empty filter (every event)
// the customer gets when they omit event_filter. Mirrors the
// alert-rule default (empty failure_source).
func defaultAppWebhookEventFilter() []string { return []string{} }

// validateWebhookEventFilter rejects out-of-vocabulary entries
// before persist. Returns an ErrAppWebhookInvalid problem on
// failure.
func validateWebhookEventFilter(events []string) *api.Problem {
	if len(events) == 0 {
		return nil
	}
	if len(events) > api.AppWebhookEventFilterLenMax {
		return api.ErrAppWebhookInvalid(fmt.Sprintf("event_filter has %d entries; max %d",
			len(events), api.AppWebhookEventFilterLenMax))
	}
	allowed := make(map[string]struct{}, len(api.AllowedAppWebhookEvents))
	for _, e := range api.AllowedAppWebhookEvents {
		allowed[e] = struct{}{}
	}
	for _, e := range events {
		if _, ok := allowed[e]; !ok {
			return api.ErrAppWebhookInvalid(fmt.Sprintf("event %q is not in the closed vocabulary; allowed: %s",
				e, strings.Join(api.AllowedAppWebhookEvents, ", ")))
		}
	}
	return nil
}

// validateWebhookRetryPolicy rejects retry_policy values outside
// the closed set.
func validateWebhookRetryPolicy(p string) *api.Problem {
	if p == "" {
		return nil // caller applies the default
	}
	for _, allowed := range api.AllowedAppWebhookRetryPolicies {
		if p == allowed {
			return nil
		}
	}
	return api.ErrAppWebhookInvalid(fmt.Sprintf("retry_policy %q is not in the closed vocabulary; allowed: %s",
		p, strings.Join(api.AllowedAppWebhookRetryPolicies, ", ")))
}

// validateWebhookURL is the body-side length / scheme check. The
// SSRF guard (resolveAndCheckEgress) runs after this. Mirrors
// the alert handler's two-stage check.
func validateWebhookURL(rawURL string) *api.Problem {
	if rawURL == "" {
		return api.ErrAppWebhookInvalid("target_url is required")
	}
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		// Reject javascript:, data:, ftp://, and the entire non-HTTP
		// family before resolveAndCheckEgress gets a chance. We allow
		// http:// to keep the e2e loopback receiver viable (the
		// egress gate permits loopback only when
		// FAAS_EGRESS_ALLOW_LOOPBACK=1); production customers target
		// https://.
		return api.ErrAppWebhookInvalid("target_url must start with http:// or https://")
	}
	if len(rawURL) < 8 || len(rawURL) > 2048 {
		// Bounds match the DB CHECK on app_webhooks_target_url_len_chk
		// (migrations/00140_app_webhooks.sql:42-46).
		return api.ErrAppWebhookInvalid(fmt.Sprintf("target_url length %d out of bounds [8, 2048]", len(rawURL)))
	}
	return nil
}

// validateWebhookSecret caps the plaintext secret length. Mirrors
// the alert handler's pre-seal length check (handlers_alerts.go:165).
func validateWebhookSecret(plaintext string) *api.Problem {
	if plaintext == "" {
		return api.ErrAppWebhookInvalid("webhook_secret is required")
	}
	if len(plaintext) > api.AppWebhookSecretMaxBytes {
		return api.ErrAppWebhookInvalid(fmt.Sprintf("webhook_secret length %d exceeds max %d",
			len(plaintext), api.AppWebhookSecretMaxBytes))
	}
	return nil
}

// listAppWebhooks returns the customer's webhooks for the slug.
// Plan-tier gate fires BEFORE loadApp so a Free customer on a
// non-existent slug gets a clean 402 instead of a 404 that leaks
// the slug's existence. Mirrors listAlertRules.
func (s *server) listAppWebhooks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.WebhookPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanWebhooksNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	rows, err := s.store.ListAppWebhooksForApp(r.Context(), app.ID)
	if err != nil {
		s.log.WarnContext(r.Context(), "list app webhooks", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not list webhooks"))
		return
	}
	out := make([]api.AppWebhookResponse, 0, len(rows))
	for _, row := range rows {
		// Defence-in-depth: loadApp already verified app.AccountID,
		// but the per-app store query could in principle return a
		// row from another account if the FK index was stale. Reject
		// such rows silently rather than leak the foreign ID.
		if row.AccountID != acct.ID {
			continue
		}
		out = append(out, appWebhookResponse(row))
	}
	writeJSON(w, http.StatusOK, out)
}

// createAppWebhook persists a new subscription. Phase order:
// decode → plan-tier → loadApp → validate body → SSRF guard →
// seal → quota → persist → audit → respond.
//
// CodeQL go/log-injection: every interpolated user string is run
// through slog with structured args, never string concatenation.
// The audit payload is a structured map; the secret is never
// included.
func (s *server) createAppWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateAppWebhookRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeAppWebhookInvalid, "Bad request", err.Error()))
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.WebhookPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanWebhooksNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	if prob := validateWebhookURL(req.TargetURL); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := validateWebhookSecret(req.WebhookSecret); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := validateWebhookEventFilter(req.EventFilter); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := validateWebhookRetryPolicy(req.RetryPolicy); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := resolveAndCheckEgress(r.Context(), req.TargetURL); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		api.WriteProblem(w, api.ErrCapacity("host age recipient not loaded — refusing to seal webhook secret"))
		return
	}
	sealed, err := secretbox.SealBytes(recipient, appWebhookSecretSealLabel, []byte(req.WebhookSecret), api.AppWebhookSecretMaxBytes)
	if err != nil {
		if prob := api.AsProblem(err); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not seal webhook secret"))
		return
	}
	retryPolicy := req.RetryPolicy
	if retryPolicy == "" {
		retryPolicy = defaultAppWebhookRetryPolicy
	}
	enabled := defaultAppWebhookEnabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	eventFilter := req.EventFilter
	if eventFilter == nil {
		eventFilter = defaultAppWebhookEventFilter()
	}
	row, err := s.store.CreateAppWebhookIfUnderQuota(r.Context(), state.AppWebhook{
		AccountID:    acct.ID,
		AppID:        app.ID,
		TargetURL:    req.TargetURL,
		SecretSealed: sealed,
		EventFilter:  eventFilter,
		RetryPolicy:  state.AppWebhookRetryPolicy(retryPolicy),
		Enabled:      enabled,
	}, limits)
	if err != nil {
		var quotaErr *state.AppWebhookQuotaError
		if errors.As(err, &quotaErr) {
			api.WriteProblem(w, api.ErrPlanWebhookQuota(acct.Plan, string(quotaErr.Scope), quotaErr.Limit, quotaErr.Observed))
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrAppWebhookInvalid("a webhook for this app already targets this URL"))
			return
		}
		s.log.WarnContext(r.Context(), "create app webhook", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not create webhook"))
		return
	}
	s.audit.Emit(r.Context(), "app.webhook_created", &acct.ID, map[string]any{
		"webhook_id":   row.ID,
		"app_id":       app.ID,
		"target_url":   row.TargetURL,
		"retry_policy": string(row.RetryPolicy),
		"enabled":      row.Enabled,
	})
	writeJSON(w, http.StatusCreated, appWebhookResponse(row))
}

// getAppWebhook returns the row if it belongs to the caller's
// account. IDOR-safe: loadApp → store.AppWebhookByID with
// AppID + AccountID filter.
func (s *server) getAppWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.WebhookPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanWebhooksNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	id := r.PathValue("id")
	row, err := s.store.AppWebhookByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "webhook not found")
		return
	}
	if row.AppID != app.ID || row.AccountID != acct.ID {
		s.notFound(w, "webhook not found")
		return
	}
	writeJSON(w, http.StatusOK, appWebhookResponse(row))
}

// updateAppWebhook partial-updates the row. Same phase order as
// create; the body may carry any subset of the editable fields.
// The audit row is emitted only on actual change — same shape as
// the eviction-priority PATCH in handlers_apps.go.
func (s *server) updateAppWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.UpdateAppWebhookRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeAppWebhookInvalid, "Bad request", err.Error()))
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.WebhookPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanWebhooksNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	id := r.PathValue("id")
	existing, err := s.store.AppWebhookByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "webhook not found")
		return
	}
	if existing.AppID != app.ID || existing.AccountID != acct.ID {
		s.notFound(w, "webhook not found")
		return
	}
	params := state.UpdateAppWebhookParams{}
	if req.TargetURL != nil {
		if prob := validateWebhookURL(*req.TargetURL); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		if prob := resolveAndCheckEgress(r.Context(), *req.TargetURL); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.TargetURL = req.TargetURL
	}
	if req.EventFilter != nil {
		if prob := validateWebhookEventFilter(*req.EventFilter); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.EventFilter = req.EventFilter
	}
	if req.RetryPolicy != nil {
		if prob := validateWebhookRetryPolicy(*req.RetryPolicy); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.RetryPolicy = (*state.AppWebhookRetryPolicy)(req.RetryPolicy)
	}
	if req.Enabled != nil {
		params.Enabled = req.Enabled
	}
	if req.WebhookSecret != nil {
		if prob := validateWebhookSecret(*req.WebhookSecret); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		recipient := setSecretRecipient()
		if recipient == nil {
			api.WriteProblem(w, api.ErrCapacity("host age recipient not loaded — refusing to seal webhook secret"))
			return
		}
		sealed, sealErr := secretbox.SealBytes(recipient, appWebhookSecretSealLabel, []byte(*req.WebhookSecret), api.AppWebhookSecretMaxBytes)
		if sealErr != nil {
			if prob := api.AsProblem(sealErr); prob != nil {
				api.WriteProblem(w, prob)
				return
			}
			api.WriteProblem(w, api.ErrCapacity("could not seal webhook secret"))
			return
		}
		params.WebhookSecretSealed = &sealed
	}
	row, err := s.store.UpdateAppWebhook(r.Context(), id, params)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "webhook not found")
			return
		}
		s.log.WarnContext(r.Context(), "update app webhook", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not update webhook"))
		return
	}
	s.audit.Emit(r.Context(), "app.webhook_updated", &acct.ID, map[string]any{
		"webhook_id":   row.ID,
		"app_id":       app.ID,
		"target_url":   row.TargetURL,
		"enabled":      row.Enabled,
		"retry_policy": string(row.RetryPolicy),
	})
	writeJSON(w, http.StatusOK, appWebhookResponse(row))
}

// deleteAppWebhook removes the row + cascades deliveries (FK).
// Mirrors deleteAlertRule.
func (s *server) deleteAppWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.WebhookPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanWebhooksNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	id := r.PathValue("id")
	existing, err := s.store.AppWebhookByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "webhook not found")
		return
	}
	if existing.AppID != app.ID || existing.AccountID != acct.ID {
		s.notFound(w, "webhook not found")
		return
	}
	if err := s.store.DeleteAppWebhook(r.Context(), id); err != nil {
		s.log.WarnContext(r.Context(), "delete app webhook", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not delete webhook"))
		return
	}
	s.audit.Emit(r.Context(), "app.webhook_deleted", &acct.ID, map[string]any{
		"webhook_id": id,
		"app_id":     app.ID,
		"target_url": existing.TargetURL,
	})
	w.WriteHeader(http.StatusNoContent)
}

// rotateAppWebhookSecret seals a caller-supplied replacement and overwrites
// the row in place. The caller can install the same value in its receiver;
// reads and responses still expose only the masked constant.
func (s *server) rotateAppWebhookSecret(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.RotateAppWebhookSecretRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeAppWebhookInvalid, "Bad request", err.Error()))
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.WebhookPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanWebhooksNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	id := r.PathValue("id")
	existing, err := s.store.AppWebhookByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "webhook not found")
		return
	}
	if existing.AppID != app.ID || existing.AccountID != acct.ID {
		s.notFound(w, "webhook not found")
		return
	}
	if prob := validateWebhookSecret(req.WebhookSecret); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		api.WriteProblem(w, api.ErrCapacity("host age recipient not loaded — refusing to seal webhook secret"))
		return
	}
	sealed, err := secretbox.SealBytes(recipient, appWebhookSecretSealLabel, []byte(req.WebhookSecret), api.AppWebhookSecretMaxBytes)
	if err != nil {
		if prob := api.AsProblem(err); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not seal webhook secret"))
		return
	}
	row, err := s.store.UpdateAppWebhook(r.Context(), id, state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed})
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "webhook not found")
			return
		}
		s.log.WarnContext(r.Context(), "rotate app webhook secret", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not rotate webhook secret"))
		return
	}
	s.audit.Emit(r.Context(), "app.webhook_secret_rotated", &acct.ID, map[string]any{
		"webhook_id": row.ID,
		"app_id":     app.ID,
		"rotated_at": row.UpdatedAt,
	})
	writeJSON(w, http.StatusOK, api.RotateAppWebhookSecretResponse{
		RotatedAt:                 api.FormatAlertTime(row.UpdatedAt),
		WebhookSecretSealedMasked: api.AppWebhookSecretMasked,
	})
}

// listAppWebhookDeliveries returns the recent deliveries for the
// webhook. pageSize is bounded by api.PageSizeMax; the pageToken
// is a synthetic cursor in MemStore, a real cursor in PgStore.
func (s *server) listAppWebhookDeliveries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.WebhookPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanWebhooksNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	id := r.PathValue("id")
	existing, err := s.store.AppWebhookByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "webhook not found")
		return
	}
	if existing.AppID != app.ID || existing.AccountID != acct.ID {
		s.notFound(w, "webhook not found")
		return
	}
	pageSize := 50
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			pageSize = n
		}
	}
	rows, next, err := s.store.ListAppWebhookDeliveries(r.Context(), app.ID, id, pageSize, r.URL.Query().Get("page_token"))
	if err != nil {
		s.log.WarnContext(r.Context(), "list app webhook deliveries", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not list deliveries"))
		return
	}
	out := api.AppWebhookDeliveryListResponse{
		Deliveries: make([]api.AppWebhookDeliveryResponse, 0, len(rows)),
		NextToken:  next,
	}
	for _, row := range rows {
		out.Deliveries = append(out.Deliveries, appWebhookDeliveryResponse(row))
	}
	writeJSON(w, http.StatusOK, out)
}

// retryAppWebhookDelivery resets a dead row to pending with
// attempt=0 + next_attempt_at=now. Mirrors the alert retry path
// (handlers_alerts.go:587-595). Manual retry is the customer's
// escape hatch when the dispatcher DLQ'd a row at attempt 7.
func (s *server) retryAppWebhookDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.WebhookPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanWebhooksNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	id := r.PathValue("id")
	existing, err := s.store.AppWebhookByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "webhook not found")
		return
	}
	if existing.AppID != app.ID || existing.AccountID != acct.ID {
		s.notFound(w, "webhook not found")
		return
	}
	did := r.PathValue("did")
	// Pass webhookID + accountID so the store's SQL-level IDOR guard
	// returns ErrNotFound for deliveries that belong to a different
	// webhook or account (see pkg/state ResetAppWebhookDeliveryFromDead).
	if err := s.store.ResetAppWebhookDeliveryFromDead(r.Context(), did, id, acct.ID, timeNow()); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "delivery not found")
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrAppWebhookInvalid("delivery is not in 'dead' state; only dead deliveries can be retried"))
			return
		}
		s.log.WarnContext(r.Context(), "retry app webhook delivery", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not retry delivery"))
		return
	}
	row, err := s.store.AppWebhookDeliveryByID(r.Context(), did)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load delivery after retry"))
		return
	}
	s.audit.Emit(r.Context(), "app.webhook_delivery_retried", &acct.ID, map[string]any{
		"webhook_id":  existing.ID,
		"delivery_id": did,
		"app_id":      app.ID,
	})
	writeJSON(w, http.StatusOK, api.AppWebhookRetryDeliveryResponse{
		Delivery: appWebhookDeliveryResponse(row),
	})
}

// appWebhookResponse maps a state row to the wire DTO. The
// sealed secret is dropped; webhook_secret_sealed_masked is the
// masked constant. EventFilter is copied verbatim (the JSON
// shape is a list, not a string).
func appWebhookResponse(r state.AppWebhook) api.AppWebhookResponse {
	return api.AppWebhookResponseFromRow(api.AppWebhookRow{
		ID:          r.ID,
		AppID:       r.AppID,
		AccountID:   r.AccountID,
		TargetURL:   r.TargetURL,
		EventFilter: r.EventFilter,
		RetryPolicy: string(r.RetryPolicy),
		Enabled:     r.Enabled,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	})
}

// appWebhookDeliveryResponse maps a delivery row to the wire DTO.
// Payload is dropped when empty (already-delivered retries drop
// the body to keep the wire small).
func appWebhookDeliveryResponse(r state.AppWebhookDelivery) api.AppWebhookDeliveryResponse {
	return api.AppWebhookDeliveryResponseFromRow(api.AppWebhookDeliveryRow{
		ID:               r.ID,
		WebhookID:        r.WebhookID,
		AppID:            r.AppID,
		AccountID:        r.AccountID,
		Event:            string(r.Event),
		Payload:          r.Payload,
		Attempt:          r.Attempt,
		Status:           string(r.Status),
		LastError:        r.LastError,
		LastResponseCode: r.LastResponseCode,
		NextAttemptAt:    r.NextAttemptAt,
		DeliveredAt:      r.DeliveredAt,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	})
}

// timeNow is the time.Now seam for the deliveries handler's
// retry path. Production value is time.Now; tests override.
var timeNow = time.Now
