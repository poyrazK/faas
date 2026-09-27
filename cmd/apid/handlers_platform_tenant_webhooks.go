package main

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) tenantWebhookOwner(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, state.PlatformTenantWebhookStore, bool) {
	tenants, ok := s.platformTenantStore(w, acct)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenants)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	store, ok := s.store.(state.PlatformTenantWebhookStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant webhooks are unavailable"))
		return state.PlatformTenant{}, nil, false
	}
	return tenant, store, true
}

func platformTenantWebhookLimits(w http.ResponseWriter, acct state.Account) (api.Limits, bool) {
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.WebhookPerAccount == 0 {
		api.WriteProblem(w, api.ErrPlanWebhooksNotAllowed(acct.Plan))
		return api.Limits{}, false
	}
	return limits, true
}

func platformTenantWebhookResponse(row state.AppWebhook) api.PlatformTenantWebhookResponse {
	return api.PlatformTenantWebhookResponse{
		ID: row.ID, Scope: string(row.Scope), PlatformTenantID: row.PlatformTenantID,
		AccountID: row.AccountID, TargetURL: row.TargetURL,
		WebhookSecretSealedMasked: api.AppWebhookSecretMasked,
		EventFilter:               append([]string(nil), row.EventFilter...), RetryPolicy: string(row.RetryPolicy),
		DeliveryFormat: string(row.DeliveryFormat), Enabled: row.Enabled,
		CreatedAt: api.FormatAlertTime(row.CreatedAt), UpdatedAt: api.FormatAlertTime(row.UpdatedAt),
	}
}

func (s *server) platformTenantWebhookByRequest(w http.ResponseWriter, r *http.Request, acct state.Account, tenant state.PlatformTenant) (state.AppWebhook, bool) {
	row, err := s.store.AppWebhookByID(r.Context(), r.PathValue("webhook_id"))
	if err != nil || row.Scope != state.AppWebhookScopePlatformTenant || row.PlatformTenantID != tenant.ID || row.AccountID != acct.ID {
		s.notFound(w, "platform tenant webhook not found")
		return state.AppWebhook{}, false
	}
	return row, true
}

func (s *server) listPlatformTenantWebhooks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, _, ok := s.tenantWebhookOwner(w, r, acct)
	if !ok {
		return
	}
	if _, ok := platformTenantWebhookLimits(w, acct); !ok {
		return
	}
	rows, err := s.store.ListAppWebhooksForAccount(r.Context(), acct.ID)
	if err != nil {
		s.log.WarnContext(r.Context(), "list platform tenant webhooks", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not list platform tenant webhooks"))
		return
	}
	out := api.PlatformTenantWebhookListResponse{Webhooks: make([]api.PlatformTenantWebhookResponse, 0)}
	for _, row := range rows {
		if row.Scope == state.AppWebhookScopePlatformTenant && row.PlatformTenantID == tenant.ID {
			out.Webhooks = append(out.Webhooks, platformTenantWebhookResponse(row))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createPlatformTenantWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, tenantStore, ok := s.tenantWebhookOwner(w, r, acct)
	if !ok {
		return
	}
	limits, ok := platformTenantWebhookLimits(w, acct)
	if !ok {
		return
	}
	var req api.CreatePlatformTenantWebhookRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrAppWebhookInvalid(err.Error()))
		return
	}
	if prob := validateWebhookURL(req.TargetURL); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := validateWebhookRetryPolicy(req.RetryPolicy); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if req.DeliveryFormat != "" {
		if prob := validateWebhookDeliveryFormat(req.DeliveryFormat); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
	}
	if prob := resolveAndCheckEgress(r.Context(), req.TargetURL); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	sealed, prob := sealAccountReleaseWebhookSecret(req.WebhookSecret)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
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
	row, err := tenantStore.CreatePlatformTenantWebhookIfUnderQuota(r.Context(), state.AppWebhook{
		AccountID: acct.ID, PlatformTenantID: tenant.ID, Scope: state.AppWebhookScopePlatformTenant,
		TargetURL: req.TargetURL, SecretSealed: sealed,
		EventFilter: []string{state.PlatformTenantStatementFinalizedEvent},
		RetryPolicy: state.AppWebhookRetryPolicy(policy), DeliveryFormat: state.AppWebhookDeliveryFormat(format), Enabled: enabled,
	}, limits)
	if err != nil {
		var quota *state.AppWebhookQuotaError
		switch {
		case errors.As(err, &quota):
			api.WriteProblem(w, api.ErrPlanWebhookQuota(acct.Plan, string(quota.Scope), quota.Limit, quota.Observed))
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.ErrAppWebhookInvalid("a platform tenant webhook already targets this URL"))
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "platform tenant not found")
		case errors.Is(err, state.ErrInvalidAppWebhookScope):
			api.WriteProblem(w, api.ErrAppWebhookInvalid("invalid platform tenant webhook scope or event_filter"))
		default:
			s.log.WarnContext(r.Context(), "create platform tenant webhook", slog.String("err", err.Error()))
			api.WriteProblem(w, api.ErrCapacity("could not create platform tenant webhook"))
		}
		return
	}
	s.audit.Emit(r.Context(), "platform_tenant.webhook_created", &acct.ID, map[string]any{
		"tenant_id": tenant.ID, "webhook_id": row.ID, "target_url": row.TargetURL,
	})
	writeJSON(w, http.StatusCreated, platformTenantWebhookResponse(row))
}

func (s *server) getPlatformTenantWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, _, ok := s.tenantWebhookOwner(w, r, acct)
	if !ok {
		return
	}
	if _, ok := platformTenantWebhookLimits(w, acct); !ok {
		return
	}
	row, ok := s.platformTenantWebhookByRequest(w, r, acct, tenant)
	if ok {
		writeJSON(w, http.StatusOK, platformTenantWebhookResponse(row))
	}
}

func (s *server) updatePlatformTenantWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, _, ok := s.tenantWebhookOwner(w, r, acct)
	if !ok {
		return
	}
	if _, ok := platformTenantWebhookLimits(w, acct); !ok {
		return
	}
	if _, ok := s.platformTenantWebhookByRequest(w, r, acct, tenant); !ok {
		return
	}
	var req api.UpdatePlatformTenantWebhookRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrAppWebhookInvalid(err.Error()))
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
	if req.RetryPolicy != nil {
		if prob := validateWebhookRetryPolicy(*req.RetryPolicy); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		value := state.AppWebhookRetryPolicy(*req.RetryPolicy)
		params.RetryPolicy = &value
	}
	if req.DeliveryFormat != nil {
		if prob := validateWebhookDeliveryFormat(*req.DeliveryFormat); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		value := state.AppWebhookDeliveryFormat(*req.DeliveryFormat)
		params.DeliveryFormat = &value
	}
	params.Enabled = req.Enabled
	row, err := s.store.UpdateAppWebhook(r.Context(), r.PathValue("webhook_id"), params)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrAppWebhookInvalid("a platform tenant webhook already targets this URL"))
			return
		}
		s.log.WarnContext(r.Context(), "update platform tenant webhook", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not update platform tenant webhook"))
		return
	}
	s.audit.Emit(r.Context(), "platform_tenant.webhook_updated", &acct.ID, map[string]any{
		"tenant_id": tenant.ID, "webhook_id": row.ID, "target_url": row.TargetURL, "enabled": row.Enabled,
	})
	writeJSON(w, http.StatusOK, platformTenantWebhookResponse(row))
}

func (s *server) deletePlatformTenantWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, _, ok := s.tenantWebhookOwner(w, r, acct)
	if !ok {
		return
	}
	if _, ok := platformTenantWebhookLimits(w, acct); !ok {
		return
	}
	row, ok := s.platformTenantWebhookByRequest(w, r, acct, tenant)
	if !ok {
		return
	}
	if err := s.store.DeleteAppWebhook(r.Context(), row.ID); err != nil {
		s.log.WarnContext(r.Context(), "delete platform tenant webhook", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not delete platform tenant webhook"))
		return
	}
	s.audit.Emit(r.Context(), "platform_tenant.webhook_deleted", &acct.ID, map[string]any{"tenant_id": tenant.ID, "webhook_id": row.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) rotatePlatformTenantWebhookSecret(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, _, ok := s.tenantWebhookOwner(w, r, acct)
	if !ok {
		return
	}
	if _, ok := platformTenantWebhookLimits(w, acct); !ok {
		return
	}
	var req api.RotateAppWebhookSecretRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrAppWebhookInvalid(err.Error()))
		return
	}
	row, ok := s.platformTenantWebhookByRequest(w, r, acct, tenant)
	if !ok {
		return
	}
	sealed, prob := sealAccountReleaseWebhookSecret(req.WebhookSecret)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	updated, err := s.store.UpdateAppWebhook(r.Context(), row.ID, state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed})
	if err != nil {
		s.log.WarnContext(r.Context(), "rotate platform tenant webhook secret", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not rotate platform tenant webhook secret"))
		return
	}
	s.audit.Emit(r.Context(), "platform_tenant.webhook_secret_rotated", &acct.ID, map[string]any{"tenant_id": tenant.ID, "webhook_id": row.ID})
	writeJSON(w, http.StatusOK, api.RotateAppWebhookSecretResponse{
		RotatedAt: api.FormatAlertTime(updated.UpdatedAt), WebhookSecretSealedMasked: api.AppWebhookSecretMasked,
	})
}

func (s *server) listPlatformTenantWebhookDeliveries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.tenantWebhookOwner(w, r, acct)
	if !ok {
		return
	}
	if _, ok := platformTenantWebhookLimits(w, acct); !ok {
		return
	}
	row, ok := s.platformTenantWebhookByRequest(w, r, acct, tenant)
	if !ok {
		return
	}
	pageSize := 50
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			pageSize = n
		}
	}
	deliveries, next, err := store.ListPlatformTenantWebhookDeliveries(r.Context(), acct.ID, tenant.ID, row.ID, pageSize, r.URL.Query().Get("page_token"))
	if err != nil {
		s.log.WarnContext(r.Context(), "list platform tenant webhook deliveries", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not list platform tenant webhook deliveries"))
		return
	}
	out := api.AppWebhookDeliveryListResponse{Deliveries: make([]api.AppWebhookDeliveryResponse, 0, len(deliveries)), NextToken: next}
	for _, delivery := range deliveries {
		out.Deliveries = append(out.Deliveries, appWebhookDeliveryResponse(delivery))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) retryPlatformTenantWebhookDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, _, ok := s.tenantWebhookOwner(w, r, acct)
	if !ok {
		return
	}
	if _, ok := platformTenantWebhookLimits(w, acct); !ok {
		return
	}
	row, ok := s.platformTenantWebhookByRequest(w, r, acct, tenant)
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
		s.log.WarnContext(r.Context(), "retry platform tenant webhook delivery", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not retry platform tenant webhook delivery"))
		return
	}
	delivery, err := s.store.AppWebhookDeliveryByID(r.Context(), did)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load delivery after retry"))
		return
	}
	s.audit.Emit(r.Context(), "platform_tenant.webhook_delivery_retried", &acct.ID,
		map[string]any{"tenant_id": tenant.ID, "webhook_id": row.ID, "delivery_id": did})
	writeJSON(w, http.StatusOK, api.AppWebhookRetryDeliveryResponse{Delivery: appWebhookDeliveryResponse(delivery)})
}
