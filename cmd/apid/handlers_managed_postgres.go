package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func managedPostgresProblem(w http.ResponseWriter, err error) {
	status, code, title := http.StatusInternalServerError, "managed_postgres_error", "Managed PostgreSQL error"
	switch {
	case errors.Is(err, managedpostgres.ErrUnavailable):
		status, code, title = http.StatusServiceUnavailable, "managed_postgres_unavailable", "Managed PostgreSQL unavailable"
	case errors.Is(err, managedpostgres.ErrNotFound):
		status, code, title = http.StatusNotFound, "managed_postgres_not_found", "Managed PostgreSQL resource not found"
	case errors.Is(err, managedpostgres.ErrConflict):
		status, code, title = http.StatusConflict, "managed_postgres_conflict", "Managed PostgreSQL resource conflict"
	case errors.Is(err, managedpostgres.ErrInvalid):
		status, code, title = http.StatusBadRequest, "managed_postgres_invalid", "Invalid managed PostgreSQL request"
	case errors.Is(err, managedpostgres.ErrUnsupported):
		status, code, title = http.StatusUnprocessableEntity, "managed_postgres_unsupported", "Managed PostgreSQL feature unsupported"
	case errors.Is(err, managedpostgres.ErrQuotaExceeded):
		status, code, title = http.StatusForbidden, "managed_postgres_quota_exceeded", "Managed PostgreSQL quota exceeded"
	case errors.Is(err, managedpostgres.ErrUsageStale):
		status, code, title = http.StatusServiceUnavailable, "managed_postgres_usage_stale", "Managed PostgreSQL usage is stale"
	}
	detail := err.Error()
	if status >= 500 {
		detail = "The managed PostgreSQL operation could not be completed."
	}
	api.WriteProblem(w, api.NewProblem(status, code, title, detail))
}

func managedPostgresPlanDenied(w http.ResponseWriter, plan api.Plan, detail string) {
	api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "managed_postgres_not_in_plan", "Managed PostgreSQL is not included in this plan", fmt.Sprintf("plan %q: %s", plan, detail)))
}

func managedPostgresPlanAllows(limits api.ManagedPostgresPlanLimits, spec managedpostgres.Spec) bool {
	switch spec.Class {
	case managedpostgres.ClassDevelopment:
		return limits.DevelopmentAllowed
	case managedpostgres.ClassBurstable:
		return limits.BurstableAllowed
	case managedpostgres.ClassProduction:
		return limits.ProductionAllowed
	default:
		return false
	}
}

func managedPostgresSpecFromRequest(req api.CreateManagedPostgresDatabaseRequest, limits api.ManagedPostgresPlanLimits) (managedpostgres.Spec, error) {
	class := managedpostgres.ServiceClass(req.ServiceClass)
	if class == "" {
		class = managedpostgres.ClassDevelopment
	}
	availability := managedpostgres.Availability(req.Availability)
	if availability == "" {
		availability = managedpostgres.AvailabilitySingleZone
	}
	scaleToZero := true
	if req.ScaleToZero != nil {
		scaleToZero = *req.ScaleToZero
	}
	storage := req.StorageLimitBytes
	if storage == 0 {
		storage = limits.StorageLimitBytes
	}
	restoreWindow := req.RestoreWindowSeconds
	if restoreWindow == 0 {
		restoreWindow = limits.RestoreWindowSeconds
	}
	spec := managedpostgres.Spec{Region: req.Region, PostgresMajor: req.PostgresMajor, Class: class, Availability: availability, ScaleToZero: scaleToZero, StorageLimitBytes: storage, RestoreWindowSeconds: restoreWindow}
	if spec.PostgresMajor == 0 {
		spec.PostgresMajor = 16
	}
	if !managedPostgresPlanAllows(limits, spec) || storage > limits.StorageLimitBytes || restoreWindow > limits.RestoreWindowSeconds {
		return managedpostgres.Spec{}, managedpostgres.ErrQuotaExceeded
	}
	if err := spec.Validate(); err != nil {
		return managedpostgres.Spec{}, err
	}
	return spec, nil
}

func managedPostgresView(database managedpostgres.Database) api.ManagedPostgresDatabase {
	out := api.ManagedPostgresDatabase{
		ID: database.ID, Name: database.Name, Region: database.Spec.Region, PostgresMajor: database.Spec.PostgresMajor,
		ServiceClass: string(database.Spec.Class), Availability: string(database.Spec.Availability), ScaleToZero: database.Spec.ScaleToZero,
		StorageLimitBytes: database.Spec.StorageLimitBytes, RestoreWindowSeconds: database.Spec.RestoreWindowSeconds,
		RestoreSourceDatabaseID: database.RestoreSourceDatabaseID, State: string(database.State), LastErrorCode: database.LastErrorCode,
		CreatedAt: database.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: database.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if !database.RestorePointInTime.IsZero() {
		out.RestorePointInTime = database.RestorePointInTime.UTC().Format(time.RFC3339Nano)
	}
	if database.DeletedAt != nil {
		out.DeletedAt = database.DeletedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func managedPostgresBindingView(binding managedpostgres.Binding) api.ManagedPostgresBinding {
	return api.ManagedPostgresBinding{ID: binding.ID, DatabaseID: binding.DatabaseID, AppID: binding.AppID, Scope: binding.Scope, EnvironmentKey: binding.EnvironmentKey, Access: string(binding.Access), CredentialGeneration: binding.CredentialGeneration, State: string(binding.State), LastErrorCode: binding.LastErrorCode, CreatedAt: binding.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: binding.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func (s *server) listManagedPostgresDatabases(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresProblem(w, managedpostgres.ErrUnavailable)
		return
	}
	databases, err := s.managedPostgres.List(r.Context(), acct.ID)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	items := make([]api.ManagedPostgresDatabase, 0, len(databases))
	for _, database := range databases {
		items = append(items, managedPostgresView(database))
	}
	writeJSON(w, http.StatusOK, api.ManagedPostgresDatabaseList{Items: items})
}

func (s *server) createManagedPostgresDatabase(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresProblem(w, managedpostgres.ErrUnavailable)
		return
	}
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok || limits.DatabasesMax == 0 {
		managedPostgresPlanDenied(w, acct.Plan, "upgrade to a paid plan to create a database")
		return
	}
	var req api.CreateManagedPostgresDatabaseRequest
	if err := decodeJSON(r, &req); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	spec, err := managedPostgresSpecFromRequest(req, limits)
	if err != nil {
		if errors.Is(err, managedpostgres.ErrQuotaExceeded) {
			managedPostgresPlanDenied(w, acct.Plan, "requested storage, restore window, or service class exceeds the plan allowance")
		} else {
			managedPostgresProblem(w, err)
		}
		return
	}
	database, err := s.managedPostgres.Create(r.Context(), managedpostgres.CreateRequest{AccountID: acct.ID, Name: req.Name, Spec: spec})
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, managedPostgresView(database))
}

func (s *server) getManagedPostgresDatabase(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresProblem(w, managedpostgres.ErrUnavailable)
		return
	}
	database, err := s.managedPostgres.Get(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedPostgresView(database))
}

func (s *server) deleteManagedPostgresDatabase(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresProblem(w, managedpostgres.ErrUnavailable)
		return
	}
	database, err := s.managedPostgres.Delete(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedPostgresView(database))
}

func (s *server) restoreManagedPostgresDatabase(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresProblem(w, managedpostgres.ErrUnavailable)
		return
	}
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok || limits.DatabasesMax == 0 {
		managedPostgresPlanDenied(w, acct.Plan, "upgrade to a paid plan to restore a database")
		return
	}
	source, err := s.managedPostgres.Get(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	if !managedPostgresPlanAllows(limits, source.Spec) || source.Spec.StorageLimitBytes > limits.StorageLimitBytes || source.Spec.RestoreWindowSeconds > limits.RestoreWindowSeconds {
		managedPostgresPlanDenied(w, acct.Plan, "the source database exceeds the current plan allowance")
		return
	}
	var req api.RestoreManagedPostgresDatabaseRequest
	if err := decodeJSON(r, &req); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	pit, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.PointInTime))
	if err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	database, err := s.managedPostgres.Restore(r.Context(), managedpostgres.RestoreDatabaseRequest{AccountID: acct.ID, SourceDatabaseID: source.ID, Name: req.Name, PointInTime: pit})
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, managedPostgresView(database))
}

func (s *server) listManagedPostgresBindings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgresBindings == nil {
		managedPostgresProblem(w, managedpostgres.ErrUnavailable)
		return
	}
	bindings, err := s.managedPostgresBindings.List(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	items := make([]api.ManagedPostgresBinding, 0, len(bindings))
	for _, binding := range bindings {
		items = append(items, managedPostgresBindingView(binding))
	}
	writeJSON(w, http.StatusOK, api.ManagedPostgresBindingList{Items: items})
}

func (s *server) createManagedPostgresBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgresBindings == nil {
		managedPostgresProblem(w, managedpostgres.ErrUnavailable)
		return
	}
	var req api.CreateManagedPostgresBindingRequest
	if err := decodeJSON(r, &req); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	app, err := s.store.AppByID(r.Context(), req.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "app not found")
		return
	}
	binding, err := s.managedPostgresBindings.Create(r.Context(), managedpostgres.CreateBindingRequest{AccountID: acct.ID, DatabaseID: r.PathValue("id"), AppID: req.AppID, Scope: req.Scope, EnvironmentKey: req.EnvironmentKey, Access: managedpostgres.CredentialAccess(req.Access)})
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, managedPostgresBindingView(binding))
}

func (s *server) getManagedPostgresBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgresBindings == nil {
		managedPostgresProblem(w, managedpostgres.ErrUnavailable)
		return
	}
	binding, err := s.managedPostgresBindings.Get(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedPostgresBindingView(binding))
}

func (s *server) deleteManagedPostgresBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgresBindings == nil {
		managedPostgresProblem(w, managedpostgres.ErrUnavailable)
		return
	}
	binding, err := s.managedPostgresBindings.Delete(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedPostgresBindingView(binding))
}
