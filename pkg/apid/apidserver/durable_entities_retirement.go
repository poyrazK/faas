package apidserver

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/durable"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) entityRetirementScope(w http.ResponseWriter, r *http.Request, acct state.Account, inspect bool) (durableentity.ID, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.durable.RetirementEnabled {
		api.WriteProblem(w, api.ErrCapacity("entity retirement preview is disabled"))
		return durableentity.ID{}, false
	}
	id, _, ok := s.entityQuotaMigrationScope(w, r, acct, inspect, false)
	return id, ok
}
func writeEntityRetirementProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, durableentity.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, "durable_entity_not_found", "Entity not found", "retirement requires an existing entity"))
		return
	}
	if errors.Is(err, durableentity.ErrConflict) || errors.Is(err, durableentity.ErrRequestConflict) {
		api.WriteProblem(w, api.NewProblem(409, "durable_entity_retirement_stale", "Retirement changed", "inspect retirement and use its exact operation identity and current revision"))
		return
	}
	if errors.Is(err, durableentity.ErrBusy) || errors.Is(err, durableentity.ErrInventoryPending) {
		api.WriteProblem(w, api.NewProblem(409, "durable_entity_retirement_blocked", "Retirement blocked", "drain owners and connections, resolve alarms and pending work, establish accounting and wait for the retirement boundary"))
		return
	}
	durable.WriteTenantQuotaProblem(w, err)
}
func (s *server) inspectEntityRetirement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityRetirementScope(w, r, acct, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	preview, err := s.durable.Engine.PreviewRetirement(ctx, id)
	if err != nil {
		writeEntityRetirementProblem(w, err)
		return
	}
	quota, err := s.durable.TenantQuotas.PreviewRetirement(ctx, id)
	if err != nil {
		writeEntityRetirementProblem(w, err)
		return
	}
	reclaimable := quota.ReservedBytes - preview.ProjectedRecoveryBytes
	if reclaimable < 0 {
		reclaimable = 0
	}
	writeJSON(w, 200, map[string]any{"entity": preview, "quota": quota, "ready_to_retire": preview.Ready && quota.Ready && preview.ProjectedRecoveryBytes <= quota.ReservedBytes, "projected_reclaimable_bytes": reclaimable})
}
func (s *server) startEntityRetirement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityRetirementScope(w, r, acct, false)
	if !ok {
		return
	}
	var request durableentity.RetirementRequest
	if !decodeJSONLimit(w, r, &request, 2048) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	quota, err := s.durable.TenantQuotas.PreviewRetirement(ctx, id)
	if err != nil {
		writeEntityRetirementProblem(w, err)
		return
	}
	if !quota.Ready && (quota.Retirement == nil || quota.Retirement.OperationID != request.OperationID) {
		writeEntityRetirementProblem(w, durableentity.ErrInventoryPending)
		return
	}
	preview, err := s.durable.Engine.PreviewRetirement(ctx, id)
	if err != nil {
		writeEntityRetirementProblem(w, err)
		return
	}
	if preview.Retirement == nil && (!preview.Ready || preview.ProjectedRecoveryBytes > quota.ReservedBytes) {
		writeEntityRetirementProblem(w, durableentity.ErrInventoryPending)
		return
	}
	actor := acct.ID
	if p, ok := principalFrom(r); ok && p.Key != nil {
		actor += "/api-key:" + p.Key.ID
	}
	status, err := s.durable.Engine.StartRetirement(ctx, id, request, actor)
	if err != nil {
		writeEntityRetirementProblem(w, err)
		return
	}
	receipt, err := s.durable.TenantQuotas.TrackRetirement(ctx, id, request.OperationID, s.durable.Engine)
	if err != nil {
		writeEntityRetirementProblem(w, err)
		return
	}
	s.audit.Emit(ctx, "durable_entity.retirement_started", &acct.ID, map[string]any{"app_id": id.AppID, "environment_id": id.EnvironmentID, "platform_tenant_id": id.TenantID, "operation_id": request.OperationID})
	writeJSON(w, 200, map[string]any{"retirement": status, "quota": receipt})
}
func (s *server) stepEntityRetirement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.entityRetirementScope(w, r, acct, false)
	if !ok {
		return
	}
	var request struct {
		OperationID    string `json:"operation_id"`
		Revision       uint64 `json:"expected_revision"`
		RestartCleanup bool   `json:"restart_cleanup"`
	}
	if !decodeJSONLimit(w, r, &request, 2048) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	// Resume the manifest/ledger gap after a lost tracking acknowledgement.
	receipt, err := s.durable.TenantQuotas.TrackRetirement(ctx, id, request.OperationID, s.durable.Engine)
	if err != nil {
		writeEntityRetirementProblem(w, err)
		return
	}
	var status *durableentity.RetirementStatus
	if request.RestartCleanup {
		status, err = s.durable.Engine.RestartRetirementCleanup(ctx, id, request.OperationID, request.Revision)
	} else {
		status, err = s.durable.Engine.StepRetirement(ctx, id, request.OperationID, request.Revision)
	}
	if err != nil {
		writeEntityRetirementProblem(w, err)
		return
	}
	if status.Phase == "complete" {
		receipt, err = s.durable.TenantQuotas.ReclaimRetirement(ctx, id, request.OperationID, s.durable.Engine)
		if err != nil {
			writeEntityRetirementProblem(w, err)
			return
		}
		s.audit.Emit(ctx, "durable_entity.retirement_reclaimed", &acct.ID, map[string]any{"app_id": id.AppID, "operation_id": request.OperationID, "released_bytes": receipt.ReleasedBytes, "retained_bytes": receipt.RetainedBytes})
	}
	writeJSON(w, 200, map[string]any{"retirement": status, "quota": receipt})
}
