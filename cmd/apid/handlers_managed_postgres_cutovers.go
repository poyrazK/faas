package main

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) prepareManagedPostgresCutover(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgresCutovers == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var req api.PrepareManagedPostgresCutoverRequest
	if err := decodeJSON(r, &req); err != nil || !validCutoverIDs(req.AppID, req.SourceDatabaseID, req.TargetDatabaseID) {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	app, err := s.store.AppByID(r.Context(), req.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "app not found")
		return
	}
	c, err := s.managedPostgresCutovers.Prepare(r.Context(), managedpostgres.PrepareCutoverRequest{
		AccountID: acct.ID, AppID: req.AppID, Scope: req.Scope, SourceDatabaseID: req.SourceDatabaseID, TargetDatabaseID: req.TargetDatabaseID,
	})
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.writeManagedPostgresCutover(w, r, acct, c, http.StatusAccepted, "preparation_requested")
}
func validCutoverIDs(ids ...string) bool {
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	return true
}
func (s *server) getManagedPostgresCutover(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.cutoverAvailable(w, r) {
		return
	}
	c, err := s.managedPostgresCutovers.Get(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, managedPostgresCutoverView(c, time.Now().UTC()))
}
func (s *server) verifyManagedPostgresCutover(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.cutoverAvailable(w, r) {
		return
	}
	c, err := s.managedPostgresCutovers.Verify(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.writeManagedPostgresCutover(w, r, acct, c, http.StatusAccepted, "verification_requested")
}
func (s *server) cancelManagedPostgresCutover(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.cutoverAvailable(w, r) {
		return
	}
	c, err := s.managedPostgresCutovers.Cancel(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.writeManagedPostgresCutover(w, r, acct, c, http.StatusAccepted, "cancellation_requested")
}
func (s *server) cutoverAvailable(w http.ResponseWriter, r *http.Request) bool {
	if s.managedPostgresCutovers == nil {
		managedPostgresNotConfiguredProblem(w)
		return false
	}
	if !validCutoverIDs(r.PathValue("id")) {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return false
	}
	return true
}
func (s *server) writeManagedPostgresCutover(w http.ResponseWriter, r *http.Request, acct state.Account, c managedpostgres.Cutover, status int, action string) {
	s.audit.Emit(r.Context(), "managed_postgres.cutover."+action, &acct.ID, map[string]any{
		"cutover_id": c.ID, "app_id": c.AppID, "scope": c.Scope, "source_database_id": c.Source.ID, "target_database_id": c.Target.ID,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, managedPostgresCutoverView(c, time.Now().UTC()))
}
func managedPostgresCutoverView(c managedpostgres.Cutover, now time.Time) api.ManagedPostgresCutover {
	out := api.ManagedPostgresCutover{ID: c.ID, SourceDatabaseID: c.Source.ID, TargetDatabaseID: c.Target.ID, AppID: c.AppID, Scope: c.Scope,
		State: string(c.State), LastErrorCode: c.LastErrorCode, VerificationFresh: c.VerificationFresh(now),
		VerificationMaxAgeSeconds: int64(managedpostgres.CutoverVerificationMaxAge / time.Second),
		CreatedAt:                 c.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: c.UpdatedAt.UTC().Format(time.RFC3339Nano),
		Members: make([]api.ManagedPostgresCutoverMember, 0, len(c.Credentials)),
	}
	if !c.VerifiedAt.IsZero() {
		out.VerifiedAt = c.VerifiedAt.UTC().Format(time.RFC3339Nano)
	}
	for _, m := range c.Credentials {
		member := api.ManagedPostgresCutoverMember{SourceBindingID: m.SourceBindingID, EnvironmentKey: m.EnvironmentKey, Access: string(m.Access), State: m.State}
		if !m.VerifiedAt.IsZero() {
			member.VerifiedAt = m.VerifiedAt.UTC().Format(time.RFC3339Nano)
		}
		out.Members = append(out.Members, member)
	}
	return out
}

// Set cache policy outside idempotency so replayed responses retain it too.
func (s *server) cutoverMutation(next accountHandler) accountHandler {
	handler := s.idempotent(next)
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		handler(w, r, acct)
	}
}
