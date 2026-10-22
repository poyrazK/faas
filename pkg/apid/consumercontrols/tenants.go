package consumercontrols

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Platform tenant controls: free trials (ADR-1322) and blocking tenants
// whose invoices are overdue (ADR-1324), with the consumer rules.

func (s *Service) tenantTrialStore(w http.ResponseWriter) (state.PlatformTenantTrialStore, bool) {
	store, ok := s.Host.Store().(state.PlatformTenantTrialStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant trials are unavailable"))
	}
	return store, ok
}

func (s *Service) tenantTrialResponse(t state.PlatformTenantTrial) api.PlatformTenantTrialResponse {
	return api.PlatformTenantTrialResponse{TenantID: t.TenantID, StartsAt: t.StartsAt, EndsAt: t.EndsAt, Active: t.EndsAt.After(s.now())}
}

func (s *Service) GetTenantTrial(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := s.Host.TenantRoute(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.tenantTrialStore(w)
	if !ok {
		return
	}
	trial, err := store.GetPlatformTenantTrial(r.Context(), acct.ID, tenant.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the tenant has no trial")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the tenant trial"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, s.tenantTrialResponse(trial))
}

// PutTenantTrial starts a tenant's one trial at the next minute, or moves
// the end of a running one, as PutTrial does for a consumer.
func (s *Service) PutTenantTrial(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := s.Host.TenantRoute(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.tenantTrialStore(w)
	if !ok {
		return
	}
	var req api.PutAPIConsumerTrialRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	trial, err := store.GetPlatformTenantTrial(r.Context(), acct.ID, tenant.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrInternal("could not load the tenant trial"))
		return
	}
	next, problem := s.nextTrial(state.APIConsumerTrial{StartsAt: trial.StartsAt, EndsAt: trial.EndsAt}, err == nil, req)
	if problem != nil {
		if problem.Status == http.StatusConflict {
			problem.Detail = "a tenant gets one trial, and this one has ended"
		}
		api.WriteProblem(w, problem)
		return
	}
	saved, err := store.PutPlatformTenantTrial(r.Context(), state.PlatformTenantTrial{AccountID: acct.ID, TenantID: tenant.ID,
		StartsAt: next.StartsAt, EndsAt: next.EndsAt})
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not save the tenant trial"))
		return
	}
	s.Host.Audit(r.Context(), "platform_tenant.trial_set", acct.ID, map[string]any{"tenant_id": tenant.ID,
		"starts_at": saved.StartsAt, "ends_at": saved.EndsAt})
	httpkit.WriteJSON(w, http.StatusOK, s.tenantTrialResponse(saved))
}

// EndTenantTrial ends a running trial at the next minute, or removes one
// that has not started.
func (s *Service) EndTenantTrial(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := s.Host.TenantRoute(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.tenantTrialStore(w)
	if !ok {
		return
	}
	trial, err := store.GetPlatformTenantTrial(r.Context(), acct.ID, tenant.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the tenant has no trial")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the tenant trial"))
		return
	}
	next := s.nextMinute()
	switch {
	case !trial.EndsAt.After(next):
		// Already over by the next minute.
	case trial.StartsAt.Before(next):
		trial.EndsAt = next
		_, err = store.PutPlatformTenantTrial(r.Context(), trial)
	default:
		err = store.DeletePlatformTenantTrial(r.Context(), acct.ID, tenant.ID)
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not end the tenant trial"))
		return
	}
	s.Host.Audit(r.Context(), "platform_tenant.trial_ended", acct.ID, map[string]any{"tenant_id": tenant.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) tenantOverdueStore(w http.ResponseWriter) (state.PlatformTenantOverdueStore, bool) {
	store, ok := s.Host.Store().(state.PlatformTenantOverdueStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant billing policies are unavailable"))
	}
	return store, ok
}

func tenantPolicyResponse(p state.PlatformTenantBillingPolicy) api.PlatformTenantBillingPolicyResponse {
	return api.PlatformTenantBillingPolicyResponse{BlockOverdueAfterDays: p.BlockOverdueAfterDays, UpdatedAt: p.UpdatedAt}
}

func (s *Service) GetTenantBillingPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.tenantOverdueStore(w)
	if !ok {
		return
	}
	policy, err := store.GetPlatformTenantBillingPolicy(r.Context(), acct.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the account has no platform tenant billing policy")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the platform tenant billing policy"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, tenantPolicyResponse(policy))
}

// PutTenantBillingPolicy sets the account's overdue rule for tenants;
// holds follow at the next check.
func (s *Service) PutTenantBillingPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.tenantOverdueStore(w)
	if !ok {
		return
	}
	var req api.PutAPIConsumerBillingPolicyRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.BlockOverdueAfterDays == nil || *req.BlockOverdueAfterDays < 0 || *req.BlockOverdueAfterDays > api.ConsumerOverdueBlockMaxDays {
		api.WriteProblem(w, validation("Invalid billing policy", fmt.Sprintf("block_overdue_after_days must be 0 to %d", api.ConsumerOverdueBlockMaxDays)))
		return
	}
	policy, err := store.PutPlatformTenantBillingPolicy(r.Context(), state.PlatformTenantBillingPolicy{AccountID: acct.ID,
		BlockOverdueAfterDays: *req.BlockOverdueAfterDays})
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not save the platform tenant billing policy"))
		return
	}
	s.Host.Audit(r.Context(), "platform_tenant_billing_policy.updated", acct.ID, map[string]any{
		"block_overdue_after_days": policy.BlockOverdueAfterDays})
	httpkit.WriteJSON(w, http.StatusOK, tenantPolicyResponse(policy))
}

func (s *Service) DeleteTenantBillingPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.tenantOverdueStore(w)
	if !ok {
		return
	}
	err := store.DeletePlatformTenantBillingPolicy(r.Context(), acct.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the account has no platform tenant billing policy")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not remove the platform tenant billing policy"))
		return
	}
	s.Host.Audit(r.Context(), "platform_tenant_billing_policy.deleted", acct.ID, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) ListTenantOverdueHolds(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.tenantOverdueStore(w)
	if !ok {
		return
	}
	holds, err := store.ListPlatformTenantOverdueHolds(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list tenant overdue holds"))
		return
	}
	out := api.PlatformTenantOverdueHoldListResponse{Holds: make([]api.PlatformTenantOverdueHoldResponse, 0, len(holds))}
	for _, h := range holds {
		out.Holds = append(out.Holds, api.PlatformTenantOverdueHoldResponse{TenantID: h.TenantID, ExportID: h.ExportID, DueAt: h.DueAt, Since: h.Since})
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// syncTenantOverdueOnce places and lifts tenant holds and audits each
// change.
func (s *Service) syncTenantOverdueOnce(ctx context.Context) error {
	store, ok := s.Host.Store().(state.PlatformTenantOverdueStore)
	if !ok {
		return nil
	}
	placed, lifted, err := store.SyncPlatformTenantOverdueHolds(ctx, s.now())
	if err != nil {
		return fmt.Errorf("sync tenant overdue holds: %w", err)
	}
	for _, h := range placed {
		if !s.released(h.AccountID) {
			continue
		}
		s.Host.Audit(ctx, "platform_tenant.overdue_blocked", h.AccountID, map[string]any{"tenant_id": h.TenantID,
			"export_id": h.ExportID, "due_at": h.DueAt})
	}
	for _, h := range lifted {
		if !s.released(h.AccountID) {
			continue
		}
		s.Host.Audit(ctx, "platform_tenant.overdue_unblocked", h.AccountID, map[string]any{"tenant_id": h.TenantID, "export_id": h.ExportID})
	}
	return nil
}
