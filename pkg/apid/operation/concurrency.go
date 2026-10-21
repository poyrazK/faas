// adr: 20261018210500
package operation

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// The releaser runs on every apid replica; each customer queue releases
// under the account's Operation lock, so replicas never double-start work.
const operationQueueReleaseInterval = 2 * time.Second

// concurrencyStore returns the store when per-customer concurrency is open to
// the account. A closed account sees the routes as absent.
func (s *Service) concurrencyStore(w http.ResponseWriter, accountID string) (state.OperationConcurrencyStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !api.OperationConcurrencyEnabled(accountID) {
		httpkit.NoSuchRoute(w)
		return nil, false
	}
	store, ok := s.Host.Store().(state.OperationConcurrencyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation concurrency storage is unavailable"))
	}
	return store, ok
}

func writeConcurrencyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Policy not found", "the policy, app or customer is unavailable"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("operation names an Operation, scope an environment, and max_running is 1 to 1000"))
	default:
		WriteOperationError(w, err)
	}
}

// PutOperationConcurrencyPolicy creates or replaces one policy of an app.
func (s *Service) PutOperationConcurrencyPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.concurrencyStore(w, acct.ID)
	if !ok {
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.OperationConcurrencyPolicyRequest
	if !DecodeOperationBody(w, r, &req, api.OperationReportBodyMaxBytes) {
		return
	}
	policy, created, err := store.PutOperationConcurrencyPolicy(r.Context(), state.OperationConcurrencyPolicyInput{AccountID: acct.ID, AppID: app.ID, Request: req, Now: time.Now()})
	if err != nil {
		writeConcurrencyError(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "app.operation_concurrency_policy_set", &acct.ID, map[string]any{"policy_id": policy.ID, "app_id": app.ID,
		"scope": policy.Scope, "operation": policy.Operation, "platform_tenant_id": policy.PlatformTenantID, "max_running": policy.MaxRunning})
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpkit.WriteJSON(w, status, policy)
}

func (s *Service) GetOperationConcurrency(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.concurrencyStore(w, acct.ID)
	if !ok {
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	out, err := store.OperationConcurrency(r.Context(), acct.ID, app.ID)
	if err != nil {
		writeConcurrencyError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// DeleteOperationConcurrencyPolicy removes one policy; work it held is
// released on the next pass unless another policy still applies.
func (s *Service) DeleteOperationConcurrencyPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.concurrencyStore(w, acct.ID)
	if !ok {
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if err := store.DeleteOperationConcurrencyPolicy(r.Context(), acct.ID, app.ID, r.PathValue("id")); err != nil {
		writeConcurrencyError(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "app.operation_concurrency_policy_deleted", &acct.ID, map[string]any{"policy_id": r.PathValue("id"), "app_id": app.ID})
	w.WriteHeader(http.StatusNoContent)
}

// GetPlatformTenantSelfOperationQueue lists the customer's queued
// Operations with their positions.
func (s *Service) GetPlatformTenantSelfOperationQueue(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.concurrencyStore(w, acct.ID)
	if !ok {
		return
	}
	tenant, ok := s.activePlatformTenantSelfID(w, r, acct)
	if !ok {
		return
	}
	queued, err := store.CustomerOperationQueue(r.Context(), acct.ID, tenant)
	if err != nil {
		writeConcurrencyError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.OperationQueueResponse{Queued: queued})
}

// RunOperationQueueReleaser starts queued Operations as customers' slots
// free, until ctx ends.
func (s *Service) RunOperationQueueReleaser(ctx context.Context) {
	store, ok := s.Host.Store().(state.OperationConcurrencyStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(operationQueueReleaseInterval)
	defer ticker.Stop()
	for {
		released, err := store.ReleaseQueuedOperations(ctx, time.Now())
		s.Metrics.operationQueueReleases(released)
		if err != nil && ctx.Err() == nil {
			s.Host.Logger().Warn("operation queue release failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
