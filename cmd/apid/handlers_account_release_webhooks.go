package main

// ADR-224: one account-owned release receiver follows current and future apps.
// The account route never exposes app-scoped subscriptions by ID.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func validateAccountReleaseWebhookFilter(events []string) *api.Problem {
	if len(events) == 0 || len(events) > len(api.AllowedAccountReleaseWebhookEvents) {
		return api.ErrAppWebhookInvalid("event_filter must contain 1–4 release events")
	}
	allowed := make(map[string]struct{}, len(api.AllowedAccountReleaseWebhookEvents))
	for _, event := range api.AllowedAccountReleaseWebhookEvents {
		allowed[event] = struct{}{}
	}
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		if _, ok := allowed[event]; !ok {
			return api.ErrAppWebhookInvalid(fmt.Sprintf("event %q is not a release event; allowed: %s", event,
				strings.Join(api.AllowedAccountReleaseWebhookEvents, ", ")))
		}
		if _, duplicate := seen[event]; duplicate {
			return api.ErrAppWebhookInvalid(fmt.Sprintf("event %q is duplicated", event))
		}
		seen[event] = struct{}{}
	}
	return nil
}

func accountReleaseWebhookLimits(w http.ResponseWriter, acct state.Account) (api.Limits, bool) {
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.WebhookPerAccount == 0 {
		api.WriteProblem(w, api.ErrPlanWebhooksNotAllowed(acct.Plan))
		return api.Limits{}, false
	}
	return limits, true
}

func sealAccountReleaseWebhookSecret(plaintext string) ([]byte, *api.Problem) {
	if prob := validateWebhookSecret(plaintext); prob != nil {
		return nil, prob
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		return nil, api.ErrCapacity("host age recipient not loaded — refusing to seal webhook secret")
	}
	sealed, err := secretbox.SealBytes(recipient, appWebhookSecretSealLabel, []byte(plaintext), api.AppWebhookSecretMaxBytes)
	if err != nil {
		if prob := api.AsProblem(err); prob != nil {
			return nil, prob
		}
		return nil, api.ErrCapacity("could not seal webhook secret")
	}
	return sealed, nil
}

func (s *server) accountReleaseWebhookByRequest(w http.ResponseWriter, r *http.Request, acct state.Account) (state.AppWebhook, bool) {
	row, err := s.store.AppWebhookByID(r.Context(), r.PathValue("id"))
	if err != nil || row.Scope != state.AppWebhookScopeAccount || row.AppID != "" || row.AccountID != acct.ID {
		s.notFound(w, "release webhook not found")
		return state.AppWebhook{}, false
	}
	return row, true
}

func accountReleaseWebhookResponse(row state.AppWebhook) api.AccountReleaseWebhookResponse {
	return api.AccountReleaseWebhookResponse{
		ID: row.ID, Scope: string(state.AppWebhookScopeAccount), AccountID: row.AccountID,
		TargetURL: row.TargetURL, WebhookSecretSealedMasked: api.AppWebhookSecretMasked,
		EventFilter: row.EventFilter, RetryPolicy: string(row.RetryPolicy),
		DeliveryFormat: string(row.DeliveryFormat), Enabled: row.Enabled,
		CreatedAt: api.FormatAlertTime(row.CreatedAt), UpdatedAt: api.FormatAlertTime(row.UpdatedAt),
	}
}

func (s *server) listAccountReleaseWebhooks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := accountReleaseWebhookLimits(w, acct); !ok {
		return
	}
	rows, err := s.store.ListAppWebhooksForAccount(r.Context(), acct.ID)
	if err != nil {
		s.log.WarnContext(r.Context(), "list account release webhooks", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not list release webhooks"))
		return
	}
	out := make([]api.AccountReleaseWebhookResponse, 0)
	for _, row := range rows {
		if row.Scope == state.AppWebhookScopeAccount && row.AppID == "" && row.AccountID == acct.ID {
			out = append(out, accountReleaseWebhookResponse(row))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createAccountReleaseWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateAccountReleaseWebhookRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrAppWebhookInvalid(err.Error()))
		return
	}
	limits, ok := accountReleaseWebhookLimits(w, acct)
	if !ok {
		return
	}
	input, prob := accountReleaseWebhookInput(r.Context(), acct.ID, req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	row, err := s.store.CreateAccountReleaseWebhookIfUnderQuota(r.Context(), input, limits)
	if err != nil {
		s.writeAccountReleaseWebhookCreateError(w, r, acct, err)
		return
	}
	s.audit.Emit(r.Context(), "account.release_webhook_created", &acct.ID, map[string]any{
		"webhook_id": row.ID, "target_url": row.TargetURL, "event_filter": row.EventFilter,
	})
	writeJSON(w, http.StatusCreated, accountReleaseWebhookResponse(row))
}

func accountReleaseWebhookInput(ctx context.Context, accountID string, req api.CreateAccountReleaseWebhookRequest) (state.AppWebhook, *api.Problem) {
	if prob := validateWebhookURL(req.TargetURL); prob != nil {
		return state.AppWebhook{}, prob
	}
	if prob := validateAccountReleaseWebhookFilter(req.EventFilter); prob != nil {
		return state.AppWebhook{}, prob
	}
	if prob := validateWebhookRetryPolicy(req.RetryPolicy); prob != nil {
		return state.AppWebhook{}, prob
	}
	if req.DeliveryFormat != "" {
		if prob := validateWebhookDeliveryFormat(req.DeliveryFormat); prob != nil {
			return state.AppWebhook{}, prob
		}
	}
	if prob := resolveAndCheckEgress(ctx, req.TargetURL); prob != nil {
		return state.AppWebhook{}, prob
	}
	sealed, prob := sealAccountReleaseWebhookSecret(req.WebhookSecret)
	if prob != nil {
		return state.AppWebhook{}, prob
	}
	policy, format, enabled := req.RetryPolicy, req.DeliveryFormat, true
	if policy == "" {
		policy = defaultAppWebhookRetryPolicy
	}
	if format == "" {
		format = defaultAppWebhookDeliveryFormat
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return state.AppWebhook{
		AccountID: accountID, Scope: state.AppWebhookScopeAccount,
		TargetURL: req.TargetURL, SecretSealed: sealed, EventFilter: req.EventFilter,
		RetryPolicy: state.AppWebhookRetryPolicy(policy), DeliveryFormat: state.AppWebhookDeliveryFormat(format), Enabled: enabled,
	}, nil
}

func (s *server) writeAccountReleaseWebhookCreateError(w http.ResponseWriter, r *http.Request, acct state.Account, err error) {
	var quota *state.AppWebhookQuotaError
	switch {
	case errors.As(err, &quota):
		api.WriteProblem(w, api.ErrPlanWebhookQuota(acct.Plan, string(quota.Scope), quota.Limit, quota.Observed))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.ErrAppWebhookInvalid("an account release webhook already targets this URL"))
	case errors.Is(err, state.ErrInvalidAppWebhookScope):
		api.WriteProblem(w, api.ErrAppWebhookInvalid("invalid account release webhook scope or event_filter"))
	default:
		s.log.WarnContext(r.Context(), "create account release webhook", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not create release webhook"))
	}
}

func (s *server) getAccountReleaseWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := accountReleaseWebhookLimits(w, acct); !ok {
		return
	}
	row, ok := s.accountReleaseWebhookByRequest(w, r, acct)
	if ok {
		writeJSON(w, http.StatusOK, accountReleaseWebhookResponse(row))
	}
}

func (s *server) updateAccountReleaseWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.UpdateAccountReleaseWebhookRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrAppWebhookInvalid(err.Error()))
		return
	}
	if _, ok := accountReleaseWebhookLimits(w, acct); !ok {
		return
	}
	if _, ok := s.accountReleaseWebhookByRequest(w, r, acct); !ok {
		return
	}
	params, prob := accountReleaseWebhookUpdateParams(r.Context(), req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	row, err := s.store.UpdateAppWebhook(r.Context(), r.PathValue("id"), params)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrAppWebhookInvalid("an account release webhook already targets this URL"))
			return
		}
		s.log.WarnContext(r.Context(), "update account release webhook", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not update release webhook"))
		return
	}
	s.audit.Emit(r.Context(), "account.release_webhook_updated", &acct.ID, map[string]any{
		"webhook_id": row.ID, "target_url": row.TargetURL, "enabled": row.Enabled,
	})
	writeJSON(w, http.StatusOK, accountReleaseWebhookResponse(row))
}

func accountReleaseWebhookUpdateParams(ctx context.Context, req api.UpdateAccountReleaseWebhookRequest) (state.UpdateAppWebhookParams, *api.Problem) {
	p := state.UpdateAppWebhookParams{}
	if req.TargetURL != nil {
		if prob := validateWebhookURL(*req.TargetURL); prob != nil {
			return p, prob
		}
		if prob := resolveAndCheckEgress(ctx, *req.TargetURL); prob != nil {
			return p, prob
		}
		p.TargetURL = req.TargetURL
	}
	if req.EventFilter != nil {
		if prob := validateAccountReleaseWebhookFilter(*req.EventFilter); prob != nil {
			return p, prob
		}
		p.EventFilter = req.EventFilter
	}
	if req.RetryPolicy != nil {
		if prob := validateWebhookRetryPolicy(*req.RetryPolicy); prob != nil {
			return p, prob
		}
		p.RetryPolicy = (*state.AppWebhookRetryPolicy)(req.RetryPolicy)
	}
	if req.DeliveryFormat != nil {
		if prob := validateWebhookDeliveryFormat(*req.DeliveryFormat); prob != nil {
			return p, prob
		}
		p.DeliveryFormat = (*state.AppWebhookDeliveryFormat)(req.DeliveryFormat)
	}
	p.Enabled = req.Enabled
	if req.WebhookSecret != nil {
		sealed, prob := sealAccountReleaseWebhookSecret(*req.WebhookSecret)
		if prob != nil {
			return p, prob
		}
		p.WebhookSecretSealed = &sealed
	}
	return p, nil
}

func (s *server) deleteAccountReleaseWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := accountReleaseWebhookLimits(w, acct); !ok {
		return
	}
	row, ok := s.accountReleaseWebhookByRequest(w, r, acct)
	if !ok {
		return
	}
	if err := s.store.DeleteAppWebhook(r.Context(), row.ID); err != nil {
		s.log.WarnContext(r.Context(), "delete account release webhook", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not delete release webhook"))
		return
	}
	s.audit.Emit(r.Context(), "account.release_webhook_deleted", &acct.ID, map[string]any{
		"webhook_id": row.ID, "target_url": row.TargetURL,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) rotateAccountReleaseWebhookSecret(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.RotateAppWebhookSecretRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrAppWebhookInvalid(err.Error()))
		return
	}
	if _, ok := accountReleaseWebhookLimits(w, acct); !ok {
		return
	}
	if _, ok := s.accountReleaseWebhookByRequest(w, r, acct); !ok {
		return
	}
	sealed, prob := sealAccountReleaseWebhookSecret(req.WebhookSecret)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	row, err := s.store.UpdateAppWebhook(r.Context(), r.PathValue("id"), state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed})
	if err != nil {
		s.log.WarnContext(r.Context(), "rotate account release webhook secret", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not rotate release webhook secret"))
		return
	}
	s.audit.Emit(r.Context(), "account.release_webhook_secret_rotated", &acct.ID, map[string]any{"webhook_id": row.ID})
	writeJSON(w, http.StatusOK, api.RotateAppWebhookSecretResponse{
		RotatedAt: api.FormatAlertTime(row.UpdatedAt), WebhookSecretSealedMasked: api.AppWebhookSecretMasked,
	})
}

func (s *server) listAccountReleaseWebhookDeliveries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := accountReleaseWebhookLimits(w, acct); !ok {
		return
	}
	row, ok := s.accountReleaseWebhookByRequest(w, r, acct)
	if !ok {
		return
	}
	pageSize := 50
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			pageSize = n
		}
	}
	rows, next, err := s.store.ListAccountReleaseWebhookDeliveries(r.Context(), acct.ID, row.ID, pageSize, r.URL.Query().Get("page_token"))
	if err != nil {
		s.log.WarnContext(r.Context(), "list account release webhook deliveries", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not list release webhook deliveries"))
		return
	}
	out := api.AppWebhookDeliveryListResponse{Deliveries: make([]api.AppWebhookDeliveryResponse, 0, len(rows)), NextToken: next}
	for _, delivery := range rows {
		out.Deliveries = append(out.Deliveries, appWebhookDeliveryResponse(delivery))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) retryAccountReleaseWebhookDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := accountReleaseWebhookLimits(w, acct); !ok {
		return
	}
	row, ok := s.accountReleaseWebhookByRequest(w, r, acct)
	if !ok {
		return
	}
	did := r.PathValue("did")
	if err := s.store.ResetAppWebhookDeliveryFromDead(r.Context(), did, row.ID, acct.ID, timeNow()); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "delivery not found")
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrAppWebhookInvalid("delivery is not in 'dead' state; only dead deliveries can be retried"))
			return
		}
		s.log.WarnContext(r.Context(), "retry account release webhook delivery", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not retry release webhook delivery"))
		return
	}
	delivery, err := s.store.AppWebhookDeliveryByID(r.Context(), did)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load delivery after retry"))
		return
	}
	s.audit.Emit(r.Context(), "account.release_webhook_delivery_retried", &acct.ID,
		map[string]any{"webhook_id": row.ID, "delivery_id": did, "app_id": delivery.AppID})
	writeJSON(w, http.StatusOK, api.AppWebhookRetryDeliveryResponse{Delivery: appWebhookDeliveryResponse(delivery)})
}
