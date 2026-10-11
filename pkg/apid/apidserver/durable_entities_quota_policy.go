package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) entityQuotaPolicyScope(w http.ResponseWriter, r *http.Request, acct state.Account, inspect bool) (durableentity.ID, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.durable.QuotaPolicyEnabled {
		api.WriteProblem(w, api.ErrCapacity("quota policy preview is disabled"))
		return durableentity.ID{}, false
	}
	id, _, ok := s.entityQuotaMigrationScope(w, r, acct, inspect, false)
	return id, ok
}
func writeQuotaPolicyProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, durableentity.ErrRequestConflict) {
		api.WriteProblem(w, api.NewProblem(409, "durable_entity_quota_policy_request_conflict", "Policy operation conflict", "operation_id was already used for a different policy request"))
		return
	}
	if errors.Is(err, durableentity.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(409, "durable_entity_quota_policy_stale", "Policy changed", "inspect current policy and migration revisions before retrying"))
		return
	}
	writeQuotaMigrationProblem(w, err)
}
func (s *server) inspectEntityQuotaPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityQuotaPolicyScope(w, r, acct, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status, err := s.durable.TenantQuotas.PolicyStatus(ctx, id)
	if err != nil {
		writeQuotaPolicyProblem(w, err)
		return
	}
	writeJSON(w, 200, status)
}
func (s *server) previewEntityQuotaPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityQuotaPolicyScope(w, r, acct, true)
	if !ok {
		return
	}
	var request entitysocket.QuotaPolicyRequest
	if !decodeJSONLimit(w, r, &request, 4096) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	preview, err := s.durable.TenantQuotas.PreviewPolicy(ctx, id, request)
	if err != nil {
		writeQuotaPolicyProblem(w, err)
		return
	}
	writeJSON(w, 200, preview)
}
func (s *server) applyEntityQuotaPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityQuotaPolicyScope(w, r, acct, false)
	if !ok {
		return
	}
	var request entitysocket.QuotaPolicyRequest
	if !decodeJSONLimit(w, r, &request, 4096) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	actor := acct.ID
	if p, ok := principalFrom(r); ok && p.Key != nil {
		actor += "/api-key:" + p.Key.ID
	}
	change, err := s.durable.TenantQuotas.ApplyPolicy(ctx, id, request, actor)
	if err != nil {
		writeQuotaPolicyProblem(w, err)
		return
	}
	s.audit.Emit(ctx, "durable_entity.quota_policy_applied", &acct.ID, map[string]any{"app_id": id.AppID, "environment_id": id.EnvironmentID, "platform_tenant_id": id.TenantID, "operation_id": request.OperationID, "policy_revision": change.Revision, "migration_id": request.MigrationID})
	writeJSON(w, 200, change)
}
