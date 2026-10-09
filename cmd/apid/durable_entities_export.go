// adr: 851
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

func (s *server) exportDurableEntity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "private, no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if s.durableEntities == nil || !s.durableEntityApps[app.ID] {
		writeDurableEntityProblem(w, durableentity.ErrUnsupported)
		return
	}
	request, err := durableEntityInspectionQuery(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("export requires namespace and key with unique supported selectors"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.DurableEntityInvokeTimeout)
	defer cancel()
	id, _, problem := s.resolveDurableEntityIdentity(r.WithContext(ctx), acct, app, api.DurableEntityInvokeRequest{Namespace: request.Namespace, Key: request.Key, Environment: request.Environment, PlatformTenantID: request.PlatformTenantID}, true)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	started := time.Now()
	out, err := s.durableEntities.ExportState(ctx, id)
	s.durableEntityMetrics.observeDuration("export", started)
	s.durableEntityMetrics.observeResult("export", durableentity.Result{}, err)
	if err != nil {
		writeDurableEntityRestoreProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) restoreDurableEntity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "private, no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok || !s.durableEntityRetryAvailable(w, acct, app) {
		return
	}
	var request api.DurableEntityRestoreRequest
	if !decodeJSONLimit(w, r, &request, int64(api.MaxDurableEntityInvocationBytes)) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.DurableEntityInvokeTimeout)
	defer cancel()
	id, scope, problem := s.durableEntityIdentity(r.WithContext(ctx), acct, app, api.DurableEntityInvokeRequest{Namespace: request.Namespace, Key: request.Key, Environment: request.Environment, PlatformTenantID: request.PlatformTenantID})
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	e := request.Export
	started := time.Now()
	result, err := s.performDurableEntityRestore(ctx, acct, app, scope, id, request)
	s.durableEntityMetrics.observeDuration("restore", started)
	s.durableEntityMetrics.observeResult("restore", result, err)
	if err != nil {
		writeDurableEntityRestoreProblem(w, err)
		return
	}
	s.audit.Emit(ctx, "durable_entity.state_restored", &acct.ID, map[string]any{"app_id": app.ID, "environment_id": id.EnvironmentID, "platform_tenant_id": id.TenantID, "state_version": result.Version, "source_version": e.Version, "replayed": result.Replayed, "validation_deployment_id": request.ValidationDeploymentID, "validation_bundle_sha256": request.ValidationBundleSHA256})
	writeJSON(w, http.StatusOK, api.DurableEntityRestoreResponse{Version: result.Version, Replayed: result.Replayed})
}

func writeDurableEntityRestoreProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, durableentity.ErrRestoreRejected) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "durable_entity_restore_rejected", "Restore state rejected", "the selected application deployment rejected this exported state"))
		return
	}
	if errors.Is(err, durableentity.ErrRestoreObsolete) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "durable_entity_restore_conflict", "Entity version changed", "inspect the current version before starting a new restore operation"))
		return
	}
	if errors.Is(err, durableentity.ErrNotFound) && !errors.Is(err, durableentity.ErrCorrupt) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Entity not found", "no committed entity exists in this scope"))
		return
	}
	writeDurableEntityProblem(w, err)
}
