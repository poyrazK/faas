package durable

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) entityQuotaPolicyScope(w http.ResponseWriter, r *http.Request, acct state.Account, inspect bool) (durableentity.ID, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.QuotaPolicyEnabled {
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
func (s *Service) InspectEntityQuotaPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityQuotaPolicyScope(w, r, acct, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status, err := s.TenantQuotas.PolicyStatus(ctx, id)
	if err != nil {
		writeQuotaPolicyProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, status)
}
func (s *Service) PreviewEntityQuotaPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityQuotaPolicyScope(w, r, acct, true)
	if !ok {
		return
	}
	var request entitysocket.QuotaPolicyRequest
	if !httpkit.DecodeJSONLimit(w, r, &request, 4096) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	preview, err := s.TenantQuotas.PreviewPolicy(ctx, id, request)
	if err != nil {
		writeQuotaPolicyProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, preview)
}
func (s *Service) ApplyEntityQuotaPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityQuotaPolicyScope(w, r, acct, false)
	if !ok {
		return
	}
	var request entitysocket.QuotaPolicyRequest
	if !httpkit.DecodeJSONLimit(w, r, &request, 4096) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	actor := acct.ID
	if p, ok := httpkit.PrincipalFrom(r); ok && p.Key != nil {
		actor += "/api-key:" + p.Key.ID
	}
	change, err := s.TenantQuotas.ApplyPolicy(ctx, id, request, actor)
	if err != nil {
		writeQuotaPolicyProblem(w, err)
		return
	}
	s.Host.EmitAudit(ctx, "durable_entity.quota_policy_applied", &acct.ID, map[string]any{"app_id": id.AppID, "environment_id": id.EnvironmentID, "platform_tenant_id": id.TenantID, "operation_id": request.OperationID, "policy_revision": change.Revision, "migration_id": request.MigrationID})
	httpkit.WriteJSON(w, 200, change)
}
