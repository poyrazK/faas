package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
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
	if errors.Is(err, managedpostgres.ErrUnavailable) {
		detail = "Managed PostgreSQL is an operator preview. The configured provider is temporarily unavailable; retry later or contact the platform operator."
	} else if status >= 500 {
		detail = "The managed PostgreSQL operation could not be completed."
	}
	problem := api.NewProblem(status, code, title, detail)
	if errors.Is(err, managedpostgres.ErrUnavailable) {
		problem = problem.WithDocs("https://gregale.dev/docs/managed-postgres")
	}
	api.WriteProblem(w, problem)
}

func managedPostgresNotConfiguredProblem(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(
		http.StatusServiceUnavailable,
		"managed_postgres_unavailable",
		"Managed PostgreSQL unavailable",
		"Managed PostgreSQL is an operator preview and is not configured for this environment. The platform operator must qualify and configure a provider before this command can be used.",
	).WithDocs("https://gregale.dev/docs/managed-postgres"))
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
	if !scaleToZero && !limits.AlwaysOnAllowed {
		return managedpostgres.Spec{}, managedpostgres.ErrQuotaExceeded
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
		managedPostgresNotConfiguredProblem(w)
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

// managedPostgresUsageView projects the internal normalized usage summary to
// the customer-safe API shape. In particular, it never serializes policy
// rates, provider cost, backend IDs, or credential material.
func managedPostgresUsageView(summary managedpostgres.UsageSummary, limits api.ManagedPostgresPlanLimits) api.ManagedPostgresUsageResponse {
	storageLimit := managedPostgresUsageCeilings(limits).MaxMonthlyStorageByteSeconds
	remaining := int64(0)
	if storageLimit > summary.Snapshot.StorageByteSeconds {
		remaining = storageLimit - summary.Snapshot.StorageByteSeconds
	}
	state := "disabled"
	if summary.PolicyEnabled {
		switch {
		case !summary.Fresh:
			state = "stale"
		case summary.Exceeded:
			state = "reached"
		default:
			state = "healthy"
		}
	}
	view := api.ManagedPostgresUsageResponse{
		PeriodStart: summary.Snapshot.PeriodStart, PolicyEnabled: summary.PolicyEnabled,
		Fresh: summary.Fresh, GuardrailState: state,
		ReadyDatabases: summary.Snapshot.ReadyDatabases, DatabaseLimit: limits.DatabasesMax,
		StorageLimitBytes:       limits.StorageLimitBytes,
		ComputeUnitSeconds:      summary.Snapshot.ComputeUnitSeconds,
		StorageByteSeconds:      summary.Snapshot.StorageByteSeconds,
		StorageByteSecondsLimit: storageLimit, StorageByteSecondsRemaining: remaining,
		HistoryByteSeconds: summary.Snapshot.HistoryByteSeconds, EgressBytes: summary.Snapshot.EgressBytes,
	}
	if !summary.Snapshot.LastObservedAt.IsZero() {
		observed := summary.Snapshot.LastObservedAt.UTC()
		view.ObservedAt = &observed
	}
	return view
}

func (s *server) getManagedPostgresUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok {
		managedPostgresPlanDenied(w, acct.Plan, "managed PostgreSQL usage is not available for this plan")
		return
	}
	summary, err := s.managedPostgres.UsageSummary(r.Context(), acct.ID, time.Now().UTC(), managedPostgresUsageCeilings(limits))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedPostgresUsageView(summary, limits))
}

func managedPostgresUsageOperatorView(accountID string, summary managedpostgres.UsageSummary, limits api.ManagedPostgresPlanLimits) (api.ManagedPostgresUsageOperatorResponse, error) {
	customer := managedPostgresUsageView(summary, limits)
	lineItems, err := summary.EffectivePolicy.LineItems(summary.Snapshot)
	if err != nil {
		return api.ManagedPostgresUsageOperatorResponse{}, err
	}
	storageLimit := summary.EffectivePolicy.MaxMonthlyStorageByteSeconds
	remaining := int64(0)
	if storageLimit > summary.Snapshot.StorageByteSeconds {
		remaining = storageLimit - summary.Snapshot.StorageByteSeconds
	}
	items := make([]api.ManagedPostgresUsageLineItem, 0, len(lineItems))
	for _, item := range lineItems {
		items = append(items, api.ManagedPostgresUsageLineItem{
			Code: item.Code, Meter: string(item.Meter), Unit: item.Unit,
			Quantity: item.Quantity, CostMillicents: item.CostMillicents,
		})
	}
	return api.ManagedPostgresUsageOperatorResponse{
		AccountID: accountID, PeriodStart: customer.PeriodStart, ObservedAt: customer.ObservedAt,
		PolicyEnabled: customer.PolicyEnabled, Fresh: customer.Fresh, GuardrailState: customer.GuardrailState,
		ReadyDatabases: customer.ReadyDatabases, DatabaseLimit: customer.DatabaseLimit,
		StorageLimitBytes: customer.StorageLimitBytes, ComputeUnitSeconds: customer.ComputeUnitSeconds,
		StorageByteSeconds: customer.StorageByteSeconds, StorageByteSecondsLimit: storageLimit,
		StorageByteSecondsRemaining: remaining, HistoryByteSeconds: customer.HistoryByteSeconds,
		EgressBytes: customer.EgressBytes, CostMillicents: summary.Snapshot.CostMillicents,
		MaxMonthlyCostMillicents:     summary.EffectivePolicy.MaxMonthlyCostMillicents,
		MaxMonthlyComputeUnitSeconds: summary.EffectivePolicy.MaxMonthlyComputeUnitSeconds,
		MaxMonthlyStorageByteSeconds: summary.EffectivePolicy.MaxMonthlyStorageByteSeconds,
		MaxMonthlyHistoryByteSeconds: summary.EffectivePolicy.MaxMonthlyHistoryByteSeconds,
		MaxMonthlyEgressBytes:        summary.EffectivePolicy.MaxMonthlyEgressBytes,
		LineItems:                    items,
	}, nil
}

func (s *server) getManagedPostgresUsageOperator(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if allowed, problem := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, problem)
		return
	}
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	targetID := strings.TrimSpace(r.PathValue("account_id"))
	if _, err := uuid.Parse(targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid account_id", "account_id must be a UUID"))
		return
	}
	target, err := s.store.AccountByID(r.Context(), targetID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", "the target account does not exist"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load target account"))
		return
	}
	limits, ok := api.ManagedPostgresLimitsFor(target.Plan)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "managed_postgres_plan_unknown", "Managed PostgreSQL plan unknown", "the target account uses an unsupported plan"))
		return
	}
	summary, err := s.managedPostgres.UsageSummary(r.Context(), target.ID, time.Now().UTC(), managedPostgresUsageCeilings(limits))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	view, err := managedPostgresUsageOperatorView(target.ID, summary, limits)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) createManagedPostgresDatabase(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
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
	s.audit.Emit(r.Context(), "managed_postgres.database.created", &acct.ID, map[string]any{
		"database_id": database.ID, "name": database.Name, "state": database.State,
		"service_class": database.Spec.Class, "storage_limit_bytes": database.Spec.StorageLimitBytes,
	})
	writeJSON(w, http.StatusCreated, managedPostgresView(database))
}

func (s *server) getManagedPostgresDatabase(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
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
		managedPostgresNotConfiguredProblem(w)
		return
	}
	database, err := s.managedPostgres.Delete(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.database.deleted", &acct.ID, map[string]any{
		"database_id": database.ID, "state": database.State,
	})
	writeJSON(w, http.StatusOK, managedPostgresView(database))
}

func (s *server) restoreManagedPostgresDatabase(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
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
	if !managedPostgresPlanAllows(limits, source.Spec) || (!source.Spec.ScaleToZero && !limits.AlwaysOnAllowed) || source.Spec.StorageLimitBytes > limits.StorageLimitBytes || source.Spec.RestoreWindowSeconds > limits.RestoreWindowSeconds {
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
	s.audit.Emit(r.Context(), "managed_postgres.database.restored", &acct.ID, map[string]any{
		"database_id": database.ID, "source_database_id": source.ID,
		"point_in_time": pit.UTC().Format(time.RFC3339Nano),
	})
	writeJSON(w, http.StatusCreated, managedPostgresView(database))
}

func (s *server) listManagedPostgresBindings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgresBindings == nil {
		managedPostgresNotConfiguredProblem(w)
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
		managedPostgresNotConfiguredProblem(w)
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
	s.audit.Emit(r.Context(), "managed_postgres.binding.created", &acct.ID, map[string]any{
		"binding_id": binding.ID, "database_id": binding.DatabaseID, "app_id": binding.AppID,
		"scope": binding.Scope, "access": binding.Access,
	})
	writeJSON(w, http.StatusCreated, managedPostgresBindingView(binding))
}

func (s *server) getManagedPostgresBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgresBindings == nil {
		managedPostgresNotConfiguredProblem(w)
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
		managedPostgresNotConfiguredProblem(w)
		return
	}
	binding, err := s.managedPostgresBindings.Delete(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.binding.deleted", &acct.ID, map[string]any{
		"binding_id": binding.ID, "database_id": binding.DatabaseID, "app_id": binding.AppID,
	})
	writeJSON(w, http.StatusOK, managedPostgresBindingView(binding))
}
