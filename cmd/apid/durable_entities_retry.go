// adr: 846
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) retryDurableEntity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "private, no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if !s.durableEntityRetryAvailable(w, acct, app) {
		return
	}
	var request api.DurableEntityRetryRequest
	if !decodeJSONLimit(w, r, &request, int64(api.MaxDurableEntityManifestBytes)) {
		return
	}
	if request.Target != "alarm" && request.Target != "outbox" || request.Target == "alarm" && (request.AlarmAt == nil || request.HeadID != "") || request.Target == "outbox" && (request.HeadID == "" || request.AlarmAt != nil) {
		api.WriteProblem(w, api.ErrValidation("select alarm with alarm_at or outbox with head_id"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.DurableEntityInvokeTimeout)
	defer cancel()
	id, _, problem := s.durableEntityIdentity(r.WithContext(ctx), acct, app, api.DurableEntityInvokeRequest{Namespace: request.Namespace, Key: request.Key, Environment: request.Environment, PlatformTenantID: request.PlatformTenantID})
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	started := time.Now()
	err := s.durableEntities.RetryExhausted(ctx, id, durableentity.Recovery{Version: request.ExpectedVersion, Revision: request.ExpectedRecoveryRevision, MessageID: request.HeadID, AlarmAt: request.AlarmAt})
	s.durableEntityMetrics.observeResult("retry_"+request.Target, durableentity.Result{}, err)
	s.durableEntityMetrics.observeDuration("retry_"+request.Target, started)
	if err != nil {
		writeDurableEntityRetryProblem(w, err)
		return
	}
	s.audit.Emit(ctx, "durable_entity.retry_rearmed", &acct.ID, map[string]any{"app_id": app.ID, "environment_id": id.EnvironmentID, "platform_tenant_id": id.TenantID, "target": request.Target, "state_version": request.ExpectedVersion, "recovery_revision": request.ExpectedRecoveryRevision, "head_id": request.HeadID, "alarm_at": request.AlarmAt})
	writeJSON(w, http.StatusOK, api.DurableEntityRetryResponse{Version: request.ExpectedVersion, Target: request.Target, Rearmed: true})
}

func (s *server) durableEntityRetryAvailable(w http.ResponseWriter, acct state.Account, app state.App) bool {
	if s.durableEntities == nil || !s.durableEntityApps[app.ID] {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "durable_entities_unavailable", "Durable entities unavailable", "this app is not enabled for the durable entity preview"))
		return false
	}
	if !app.AcceptsRequestInvocations() {
		api.WriteProblem(w, api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode))
		return false
	}
	if acct.AbuseHeld() {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account held", "entity recovery is unavailable while the account is held"))
		return false
	}
	if !api.MustLimitsFor(acct.Plan).AsyncInvokeAllowed {
		api.WriteProblem(w, api.ErrPlanFeatureGated("durable_entities_preview", acct.Plan))
		return false
	}
	return true
}

func writeDurableEntityRetryProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, durableentity.ErrRecoveryObsolete) || errors.Is(err, durableentity.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "durable_entity_recovery_conflict", "Recovery observation changed", "inspect the entity again; the selected work changed or is not exhausted"))
		return
	}
	if errors.Is(err, durableentity.ErrNotFound) && !errors.Is(err, durableentity.ErrCorrupt) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Entity not found", "no entity exists in the selected scope"))
		return
	}
	if errors.Is(err, durableentity.ErrBusy) {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "durable_entity_busy", "Entity busy", "wait for the current owner to release or expire, then inspect again"))
		return
	}
	if errors.Is(err, durableentity.ErrInvalid) {
		api.WriteProblem(w, api.ErrValidation("retry requires valid selectors, positive expected_version, expected_recovery_revision and exactly one target"))
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, "durable_entity_timeout", "Recovery deadline elapsed", "inspect before deciding whether to retry recovery"))
		return
	}
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "durable_entity_recovery_unavailable", "Recovery outcome unavailable", "inspect before deciding whether to retry recovery"))
}
