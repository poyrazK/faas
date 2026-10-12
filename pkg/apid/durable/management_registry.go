package durable

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) entityRegistryScope(w http.ResponseWriter, r *http.Request, acct state.Account, inspect bool) (durableentity.ID, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.RegistryEnabled {
		api.WriteProblem(w, api.ErrCapacity("entity registry management is disabled"))
		return durableentity.ID{}, "", false
	}
	return s.entityQuotaMigrationScope(w, r, acct, inspect, inspect)
}
func (s *Service) InspectEntityRegistry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, cursor, ok := s.entityRegistryScope(w, r, acct, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	page, err := s.TenantQuotas.ListRegistry(ctx, id, cursor)
	if err != nil {
		writeEntityRegistryProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, page)
}
func (s *Service) EnableEntityRegistry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, _, ok := s.entityRegistryScope(w, r, acct, false)
	if !ok {
		return
	}
	var request struct {
		OperationID string `json:"operation_id"`
		Revision    string `json:"expected_revision"`
	}
	if !httpkit.DecodeJSONLimit(w, r, &request, 2048) {
		return
	}
	actor := acct.ID
	if p, ok := httpkit.PrincipalFrom(r); ok && p.Key != nil {
		actor += "/api-key:" + p.Key.ID
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.TenantQuotas.EnableRegistry(ctx, id, request.OperationID, request.Revision, actor); err != nil {
		writeEntityRegistryProblem(w, err)
		return
	}
	status, err := s.TenantQuotas.RegistryStatus(ctx, id)
	if err != nil {
		writeEntityRegistryProblem(w, err)
		return
	}
	s.Host.EmitAudit(ctx, "durable_entity.registry_enabled", &acct.ID, map[string]any{"app_id": id.AppID, "environment_id": id.EnvironmentID, "platform_tenant_id": id.TenantID, "operation_id": request.OperationID})
	httpkit.WriteJSON(w, 200, status)
}

func writeEntityRegistryProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, durableentity.ErrConflict) || errors.Is(err, durableentity.ErrRequestConflict) {
		api.WriteProblem(w, api.NewProblem(409, "durable_entity_registry_stale", "Registry changed", "restart listing or inspect registry status and replay the exact migration request"))
		return
	}
	if errors.Is(err, durableentity.ErrBusy) || errors.Is(err, durableentity.ErrInventoryPending) {
		api.WriteProblem(w, api.NewProblem(409, "durable_entity_registry_blocked", "Registry migration blocked", "drain connections, complete accounting migration and resolve pending retirements"))
		return
	}
	if errors.Is(err, durableentity.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, "durable_entity_registry_not_found", "Registry not found", "this scope has no quota ledger"))
		return
	}
	WriteTenantQuotaProblem(w, err)
}
