package main

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) entityRegistryCleanupScope(w http.ResponseWriter, r *http.Request, acct state.Account, inspect bool) (durableentity.ID, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.durableEntityRegistryCleanupEnabled {
		api.WriteProblem(w, api.ErrCapacity("registry cleanup management is disabled"))
		return durableentity.ID{}, false
	}
	id, _, ok := s.entityRegistryScope(w, r, acct, inspect)
	return id, ok
}
func (s *server) inspectEntityRegistryCleanup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityRegistryCleanupScope(w, r, acct, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, err := s.durableEntityTenantQuotas.RegistryCleanupStatus(ctx, id)
	if err != nil {
		writeEntityRegistryProblem(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"cleanup": status})
}
func (s *server) startEntityRegistryCleanup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityRegistryCleanupScope(w, r, acct, false)
	if !ok {
		return
	}
	var request struct {
		OperationID string `json:"operation_id"`
		Revision    string `json:"expected_revision"`
	}
	if !decodeJSONLimit(w, r, &request, 2048) {
		return
	}
	actor := acct.ID
	if p, ok := principalFrom(r); ok && p.Key != nil {
		actor += "/api-key:" + p.Key.ID
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, err := s.durableEntityTenantQuotas.StartRegistryCleanup(ctx, id, request.OperationID, request.Revision, actor)
	if err != nil {
		writeEntityRegistryProblem(w, err)
		return
	}
	s.audit.Emit(ctx, "durable_entity.registry_cleanup_started", &acct.ID, map[string]any{"app_id": id.AppID, "environment_id": id.EnvironmentID, "platform_tenant_id": id.TenantID, "operation_id": status.OperationID, "epoch": status.Epoch})
	writeJSON(w, 200, map[string]any{"cleanup": status})
}
func (s *server) stepEntityRegistryCleanup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityRegistryCleanupScope(w, r, acct, false)
	if !ok {
		return
	}
	var request struct {
		OperationID string `json:"operation_id"`
		Revision    uint64 `json:"expected_revision"`
		Restart     bool   `json:"restart_cleanup"`
	}
	if !decodeJSONLimit(w, r, &request, 2048) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, err := s.durableEntityTenantQuotas.StepRegistryCleanup(ctx, id, request.OperationID, request.Revision, request.Restart)
	if err != nil {
		writeEntityRegistryProblem(w, err)
		return
	}
	if status.Phase == "complete" {
		s.audit.Emit(ctx, "durable_entity.registry_cleanup_completed", &acct.ID, map[string]any{"app_id": id.AppID, "environment_id": id.EnvironmentID, "platform_tenant_id": id.TenantID, "operation_id": status.OperationID, "epoch": status.Epoch, "deleted_objects": status.DeletedObjects})
	}
	writeJSON(w, 200, map[string]any{"cleanup": status})
}
