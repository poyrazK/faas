package durable

import (
	"context"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) entityRegistryCleanupScope(w http.ResponseWriter, r *http.Request, acct state.Account, inspect bool) (durableentity.ID, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.RegistryCleanupEnabled {
		api.WriteProblem(w, api.ErrCapacity("registry cleanup management is disabled"))
		return durableentity.ID{}, false
	}
	id, _, ok := s.entityRegistryScope(w, r, acct, inspect)
	return id, ok
}
func (s *Service) InspectEntityRegistryCleanup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityRegistryCleanupScope(w, r, acct, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, err := s.TenantQuotas.RegistryCleanupStatus(ctx, id)
	if err != nil {
		writeEntityRegistryProblem(w, err)
		return
	}
	worker, err := s.TenantQuotas.RegistryCleanupWorkerStatus(ctx, id)
	var workerError any
	if err != nil {
		worker = nil
		workerError = "unavailable" // Advisory storage must not hide the authoritative checkpoint.
	}
	if worker != nil && (status == nil || worker.OperationID != status.OperationID) {
		worker = nil // Independent observations never describe another operation.
	}
	var seconds *int64
	if worker != nil && !worker.LastSuccessAt.IsZero() {
		age := int64(time.Since(worker.LastSuccessAt).Seconds())
		if age < 0 {
			age = 0
		}
		seconds = &age
	}
	httpkit.WriteJSON(w, 200, map[string]any{"cleanup": status, "worker_enabled": s.RegistryCleanupWorkerEnabled, "worker": worker, "worker_status_error": workerError, "seconds_since_last_success": seconds, "discovery_health": s.registryHealth.snapshot(time.Now(), s.RegistryCleanupWorkerEnabled), "progress_health": registryCleanupProgress(time.Now(), status, worker, workerError != nil, s.RegistryCleanupWorkerEnabled)})
}
func (s *Service) StartEntityRegistryCleanup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityRegistryCleanupScope(w, r, acct, false)
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
	status, err := s.TenantQuotas.StartRegistryCleanup(ctx, id, request.OperationID, request.Revision, actor)
	if err != nil {
		writeEntityRegistryProblem(w, err)
		return
	}
	s.Host.EmitAudit(ctx, "durable_entity.registry_cleanup_started", &acct.ID, map[string]any{"app_id": id.AppID, "environment_id": id.EnvironmentID, "platform_tenant_id": id.TenantID, "operation_id": status.OperationID, "epoch": status.Epoch})
	httpkit.WriteJSON(w, 200, map[string]any{"cleanup": status})
}
func (s *Service) StepEntityRegistryCleanup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityRegistryCleanupScope(w, r, acct, false)
	if !ok {
		return
	}
	var request struct {
		OperationID string `json:"operation_id"`
		Revision    uint64 `json:"expected_revision"`
		Restart     bool   `json:"restart_cleanup"`
	}
	if !httpkit.DecodeJSONLimit(w, r, &request, 2048) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, err := s.TenantQuotas.StepRegistryCleanup(ctx, id, request.OperationID, request.Revision, request.Restart)
	if err != nil {
		writeEntityRegistryProblem(w, err)
		return
	}
	if status.Phase == "complete" {
		s.Host.EmitAudit(ctx, "durable_entity.registry_cleanup_completed", &acct.ID, map[string]any{"app_id": id.AppID, "environment_id": id.EnvironmentID, "platform_tenant_id": id.TenantID, "operation_id": status.OperationID, "epoch": status.Epoch, "deleted_objects": status.DeletedObjects})
	}
	httpkit.WriteJSON(w, 200, map[string]any{"cleanup": status})
}
