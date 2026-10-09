// adr: 852
package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) durableEntityBackupIdentity(w http.ResponseWriter, r *http.Request, acct state.Account, extra string) (durableentity.ID, string, bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return durableentity.ID{}, "", false
	}
	if s.durableEntities == nil || !s.durableEntityApps[app.ID] {
		writeDurableEntityProblem(w, durableentity.ErrUnsupported)
		return durableentity.ID{}, "", false
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query[extra]) > 1 || len(query.Get(extra)) > api.MaxObjectS3ListCursorBytes {
		api.WriteProblem(w, api.ErrValidation("invalid backup selector"))
		return durableentity.ID{}, "", false
	}
	value := query.Get(extra)
	query.Del(extra)
	selectors, err := durableEntityInspectionQuery(query.Encode())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("backup reads require namespace and key and unique supported selectors"))
		return durableentity.ID{}, "", false
	}
	id, _, problem := s.resolveDurableEntityIdentity(r, acct, app, api.DurableEntityInvokeRequest{Namespace: selectors.Namespace, Key: selectors.Key, Environment: selectors.Environment, PlatformTenantID: selectors.PlatformTenantID}, true)
	if problem != nil {
		api.WriteProblem(w, problem)
		return id, "", false
	}
	return id, value, true
}

func (s *server) listDurableEntityBackups(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ctx, cancel := context.WithTimeout(r.Context(), api.DurableEntityInvokeTimeout)
	defer cancel()
	id, cursor, ok := s.durableEntityBackupIdentity(w, r.WithContext(ctx), acct, "cursor")
	if !ok {
		return
	}
	out, err := s.durableEntities.ListBackups(ctx, id, cursor)
	if err != nil {
		writeDurableEntityRestoreProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getDurableEntityBackup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ctx, cancel := context.WithTimeout(r.Context(), api.DurableEntityInvokeTimeout)
	defer cancel()
	id, name, ok := s.durableEntityBackupIdentity(w, r.WithContext(ctx), acct, "backup_id")
	if !ok {
		return
	}
	out, err := s.durableEntities.ReadBackup(ctx, id, name)
	if err != nil {
		writeDurableEntityRestoreProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) previewDurableEntityRestore(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "private, no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if s.durableEntities == nil || !s.durableEntityApps[app.ID] {
		writeDurableEntityProblem(w, durableentity.ErrUnsupported)
		return
	}
	var request api.DurableEntityRestoreRequest
	if !decodeJSONLimit(w, r, &request, int64(api.MaxDurableEntityInvocationBytes)) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.DurableEntityInvokeTimeout)
	defer cancel()
	id, _, problem := s.resolveDurableEntityIdentity(r.WithContext(ctx), acct, app, api.DurableEntityInvokeRequest{Namespace: request.Namespace, Key: request.Key, Environment: request.Environment, PlatformTenantID: request.PlatformTenantID}, true)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	out, err := s.durableEntities.PreviewRestore(ctx, id, request.ExpectedVersion, durableEntityExportFromAPI(request.Export))
	if err != nil {
		writeDurableEntityRestoreProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func durableEntityExportFromAPI(e api.DurableEntityStateExport) durableentity.StateExport {
	return durableentity.StateExport{Format: e.Format, Version: e.Version, Data: e.Data, Checksum: e.Checksum, Entity: durableentity.ID{AccountID: e.Entity.AccountID, AppID: e.Entity.AppID, EnvironmentID: e.Entity.EnvironmentID, TenantID: e.Entity.TenantID, Namespace: e.Entity.Namespace, Key: e.Entity.Key}}
}
