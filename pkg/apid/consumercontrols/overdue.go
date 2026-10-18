package consumercontrols

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) overdueStore(w http.ResponseWriter) (state.APIConsumerOverdueStore, bool) {
	store, ok := s.Host.Store().(state.APIConsumerOverdueStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("consumer billing policies are unavailable"))
	}
	return store, ok
}

func policyResponse(p state.APIConsumerBillingPolicy) api.APIConsumerBillingPolicyResponse {
	return api.APIConsumerBillingPolicyResponse{AppID: p.AppID, BlockOverdueAfterDays: p.BlockOverdueAfterDays, UpdatedAt: p.UpdatedAt}
}

func (s *Service) GetBillingPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.AppRoute(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.overdueStore(w)
	if !ok {
		return
	}
	policy, err := store.GetAPIConsumerBillingPolicy(r.Context(), acct.ID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the app has no consumer billing policy")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the consumer billing policy"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, policyResponse(policy))
}

// PutBillingPolicy sets the app's overdue rule; holds follow at the next
// check.
func (s *Service) PutBillingPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.AppRoute(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.overdueStore(w)
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
	policy, err := store.PutAPIConsumerBillingPolicy(r.Context(), state.APIConsumerBillingPolicy{AccountID: acct.ID, AppID: app.ID,
		BlockOverdueAfterDays: *req.BlockOverdueAfterDays})
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not save the consumer billing policy"))
		return
	}
	s.Host.Audit(r.Context(), "api_consumer_billing_policy.updated", acct.ID, map[string]any{"app_id": app.ID,
		"block_overdue_after_days": policy.BlockOverdueAfterDays})
	httpkit.WriteJSON(w, http.StatusOK, policyResponse(policy))
}

func (s *Service) DeleteBillingPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.AppRoute(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.overdueStore(w)
	if !ok {
		return
	}
	err := store.DeleteAPIConsumerBillingPolicy(r.Context(), acct.ID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the app has no consumer billing policy")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not remove the consumer billing policy"))
		return
	}
	s.Host.Audit(r.Context(), "api_consumer_billing_policy.deleted", acct.ID, map[string]any{"app_id": app.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) ListOverdueHolds(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.AppRoute(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.overdueStore(w)
	if !ok {
		return
	}
	holds, err := store.ListAPIConsumerOverdueHolds(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list overdue holds"))
		return
	}
	out := api.APIConsumerOverdueHoldListResponse{Holds: make([]api.APIConsumerOverdueHoldResponse, 0, len(holds))}
	for _, h := range holds {
		out.Holds = append(out.Holds, api.APIConsumerOverdueHoldResponse{ConsumerID: h.ConsumerID, ExportID: h.ExportID, DueAt: h.DueAt, Since: h.Since})
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// Start recomputes consumer and tenant overdue holds every
// api.ConsumerOverdueCheckInterval.
func (s *Service) Start(ctx context.Context, log *slog.Logger) {
	if _, ok := s.Host.Store().(state.APIConsumerOverdueStore); !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(api.ConsumerOverdueCheckInterval)
		defer ticker.Stop()
		for {
			if err := s.tick(ctx); err != nil && ctx.Err() == nil {
				log.Warn("overdue check failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// SyncOverdueOnce places and lifts consumer and tenant holds from the
// exported invoices' payment status (ADR-1504) and audits each change.
func (s *Service) SyncOverdueOnce(ctx context.Context) error {
	return errors.Join(s.syncConsumerOverdueOnce(ctx), s.syncTenantOverdueOnce(ctx))
}

func (s *Service) syncConsumerOverdueOnce(ctx context.Context) error {
	store, ok := s.Host.Store().(state.APIConsumerOverdueStore)
	if !ok {
		return nil
	}
	placed, lifted, err := store.SyncAPIConsumerOverdueHolds(ctx, s.now())
	if err != nil {
		return fmt.Errorf("sync overdue holds: %w", err)
	}
	for _, h := range placed {
		s.Host.Audit(ctx, "api_consumer.overdue_blocked", h.AccountID, map[string]any{"app_id": h.AppID, "consumer_id": h.ConsumerID,
			"export_id": h.ExportID, "due_at": h.DueAt})
	}
	for _, h := range lifted {
		s.Host.Audit(ctx, "api_consumer.overdue_unblocked", h.AccountID, map[string]any{"app_id": h.AppID, "consumer_id": h.ConsumerID,
			"export_id": h.ExportID})
	}
	return nil
}
