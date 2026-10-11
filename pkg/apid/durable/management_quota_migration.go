package durable

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) entityQuotaMigrationScope(w http.ResponseWriter, r *http.Request, acct state.Account, inspect, plan bool) (durableentity.ID, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.QuotaMigrationEnabled || s.TenantQuotas == nil || s.Engine == nil {
		api.WriteProblem(w, api.ErrCapacity("quota migration preview is disabled"))
		return durableentity.ID{}, "", false
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return durableentity.ID{}, "", false
	}
	if !s.Apps[app.ID] {
		api.WriteProblem(w, api.ErrCapacity("app is not enabled for durable entities"))
		return durableentity.ID{}, "", false
	}
	if !inspect && !s.RetryAvailable(w, acct, app) {
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
	selectors, err := InspectionQuery(query.Encode())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("provide exact entity scope selectors"))
		return durableentity.ID{}, "", false
	}
	id, _, problem := s.ResolveIdentity(r, acct, app, api.DurableEntityInvokeRequest{Namespace: selectors.Namespace, Key: selectors.Key, Environment: selectors.Environment, PlatformTenantID: selectors.PlatformTenantID}, inspect)
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
	WriteTenantQuotaProblem(w, err)
}
func (s *Service) PlanEntityQuotaMigration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, cursor, ok := s.entityQuotaMigrationScope(w, r, acct, true, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	plan, err := s.TenantQuotas.PlanMigration(ctx, id, cursor, s.Engine)
	if err != nil {
		writeQuotaMigrationProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, plan)
}
func (s *Service) InspectEntityQuotaMigration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, _, ok := s.entityQuotaMigrationScope(w, r, acct, true, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status, err := s.TenantQuotas.MigrationStatus(ctx, id)
	if err != nil {
		writeQuotaMigrationProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, map[string]any{"migration": status})
}
func (s *Service) StartEntityQuotaMigration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, _, ok := s.entityQuotaMigrationScope(w, r, acct, false, false)
	if !ok {
		return
	}
	var request struct {
		ID         string `json:"migration_id"`
		ExpectedID string `json:"expected_migration_id"`
		Revision   uint64 `json:"expected_revision"`
	}
	if !httpkit.DecodeJSONLimit(w, r, &request, 4096) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status, err := s.TenantQuotas.StartMigration(ctx, id, request.ID, request.ExpectedID, request.Revision)
	if err != nil {
		writeQuotaMigrationProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, status)
}
func (s *Service) StepEntityQuotaMigration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, _, ok := s.entityQuotaMigrationScope(w, r, acct, false, false)
	if !ok {
		return
	}
	var request struct {
		ID       string `json:"migration_id"`
		Revision uint64 `json:"expected_revision"`
	}
	if !httpkit.DecodeJSONLimit(w, r, &request, 4096) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, err := s.TenantQuotas.StepMigration(ctx, id, request.ID, request.Revision, s.Engine)
	if err != nil {
		writeQuotaMigrationProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, status)
}
