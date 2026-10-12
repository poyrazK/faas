// adr: 941
package durable

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) Export(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "private, no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if s.Engine == nil || !s.Apps[app.ID] {
		WriteProblem(w, durableentity.ErrUnsupported)
		return
	}
	request, err := InspectionQuery(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("export requires namespace and key with unique supported selectors"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.DurableEntityInvokeTimeout)
	defer cancel()
	id, _, problem := s.ResolveIdentity(r.WithContext(ctx), acct, app, api.DurableEntityInvokeRequest{Namespace: request.Namespace, Key: request.Key, Environment: request.Environment, PlatformTenantID: request.PlatformTenantID}, true)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	started := time.Now()
	out, err := s.Engine.ExportState(ctx, id)
	s.Metrics.observeDuration("export", started)
	s.Metrics.observeResult("export", durableentity.Result{}, err)
	if err != nil {
		writeDurableEntityRestoreProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) Restore(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "private, no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok || !s.RetryAvailable(w, acct, app) {
		return
	}
	var request api.DurableEntityRestoreRequest
	if !httpkit.DecodeJSONLimit(w, r, &request, int64(api.MaxDurableEntityInvocationBytes)) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.DurableEntityInvokeTimeout)
	defer cancel()
	id, scope, problem := s.Identity(r.WithContext(ctx), acct, app, api.DurableEntityInvokeRequest{Namespace: request.Namespace, Key: request.Key, Environment: request.Environment, PlatformTenantID: request.PlatformTenantID})
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	e := request.Export
	started := time.Now()
	result, err := s.PerformRestore(ctx, acct, app, scope, id, request)
	s.Metrics.observeDuration("restore", started)
	s.Metrics.observeResult("restore", result, err)
	if err != nil {
		writeDurableEntityRestoreProblem(w, err)
		return
	}
	s.Host.EmitAudit(ctx, "durable_entity.state_restored", &acct.ID, map[string]any{"app_id": app.ID, "environment_id": id.EnvironmentID, "platform_tenant_id": id.TenantID, "state_version": result.Version, "source_version": e.Version, "replayed": result.Replayed, "validation_deployment_id": request.ValidationDeploymentID, "validation_bundle_sha256": request.ValidationBundleSHA256})
	httpkit.WriteJSON(w, http.StatusOK, api.DurableEntityRestoreResponse{Version: result.Version, Replayed: result.Replayed})
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
	WriteProblem(w, err)
}
