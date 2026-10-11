package apidserver

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/durable"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) entityQuotaMigrationScope(w http.ResponseWriter, r *http.Request, acct state.Account, inspect, plan bool) (durableentity.ID, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.durable.QuotaMigrationEnabled || s.durable.TenantQuotas == nil || s.durable.Engine == nil {
		api.WriteProblem(w, api.ErrCapacity("quota migration preview is disabled"))
		return durableentity.ID{}, "", false
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return durableentity.ID{}, "", false
	}
	if !s.durable.Apps[app.ID] {
		api.WriteProblem(w, api.ErrCapacity("app is not enabled for durable entities"))
		return durableentity.ID{}, "", false
	}
	if !inspect && !s.durable.RetryAvailable(w, acct, app) {
		return durableentity.ID{}, "", false
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	cursor := ""
	if plan && err == nil {
		if values, found := query["cursor"]; found {
			if len(values) != 1 || len(values[0]) > api.MaxObjectS3ListCursorBytes {
				err = durableentity.ErrInvalid
			} else {
				cursor = values[0]
				query.Del("cursor")
			}
		}
	}
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid migration selectors"))
		return durableentity.ID{}, "", false
	}
	selectors, err := durable.InspectionQuery(query.Encode())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("provide exact entity scope selectors"))
		return durableentity.ID{}, "", false
	}
	id, _, problem := s.durable.ResolveIdentity(r, acct, app, api.DurableEntityInvokeRequest{Namespace: selectors.Namespace, Key: selectors.Key, Environment: selectors.Environment, PlatformTenantID: selectors.PlatformTenantID}, inspect)
	if problem != nil {
		api.WriteProblem(w, problem)
		return durableentity.ID{}, "", false
	}
	return id, cursor, true
}
func writeQuotaMigrationProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, durableentity.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(409, "durable_entity_quota_migration_stale", "Migration changed", "read migration status and use its current UUID and revision"))
		return
	}
	if errors.Is(err, durableentity.ErrInventoryPending) {
		api.WriteProblem(w, api.NewProblem(409, "durable_entity_quota_migration_blocked", "Migration blocked", "inspect the read-only plan for missing usage, entity caps, reservation coverage or budget shortfalls"))
		return
	}
	durable.WriteTenantQuotaProblem(w, err)
}
func (s *server) planEntityQuotaMigration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, cursor, ok := s.entityQuotaMigrationScope(w, r, acct, true, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	plan, err := s.durable.TenantQuotas.PlanMigration(ctx, id, cursor, s.durable.Engine)
	if err != nil {
		writeQuotaMigrationProblem(w, err)
		return
	}
	writeJSON(w, 200, plan)
}
func (s *server) inspectEntityQuotaMigration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, _, ok := s.entityQuotaMigrationScope(w, r, acct, true, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status, err := s.durable.TenantQuotas.MigrationStatus(ctx, id)
	if err != nil {
		writeQuotaMigrationProblem(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"migration": status})
}
func (s *server) startEntityQuotaMigration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, _, ok := s.entityQuotaMigrationScope(w, r, acct, false, false)
	if !ok {
		return
	}
	var request struct {
		ID         string `json:"migration_id"`
		ExpectedID string `json:"expected_migration_id"`
		Revision   uint64 `json:"expected_revision"`
	}
	if !decodeJSONLimit(w, r, &request, 4096) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status, err := s.durable.TenantQuotas.StartMigration(ctx, id, request.ID, request.ExpectedID, request.Revision)
	if err != nil {
		writeQuotaMigrationProblem(w, err)
		return
	}
	writeJSON(w, 200, status)
}
func (s *server) stepEntityQuotaMigration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, _, ok := s.entityQuotaMigrationScope(w, r, acct, false, false)
	if !ok {
		return
	}
	var request struct {
		ID       string `json:"migration_id"`
		Revision uint64 `json:"expected_revision"`
	}
	if !decodeJSONLimit(w, r, &request, 4096) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, err := s.durable.TenantQuotas.StepMigration(ctx, id, request.ID, request.Revision, s.durable.Engine)
	if err != nil {
		writeQuotaMigrationProblem(w, err)
		return
	}
	writeJSON(w, 200, status)
}
