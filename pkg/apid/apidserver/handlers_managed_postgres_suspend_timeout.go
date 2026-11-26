package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"

	"github.com/onebox-faas/faas/pkg/apid/managedpg"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// managedPostgresSuspendTimeoutsOrLog reads the suspend timeout in force for
// each scale-to-zero database; a failed read leaves the field out of the view.
func (s *server) managedPostgresSuspendTimeoutsOrLog(ctx context.Context, databases []managedpostgres.Database) map[string]managedpostgres.SuspendTimeout {
	if s.managedPostgres == nil {
		return nil
	}
	timeouts, err := s.managedPostgres.SuspendTimeouts(ctx, databases)
	if err != nil {
		s.log.Warn("managed postgres suspend timeout: read", "err", err)
		return nil
	}
	return timeouts
}

// setManagedPostgresSuspendTimeout serves PUT .../suspend-timeout with the
// service as configured when the request arrives.
func (s *server) setManagedPostgresSuspendTimeout(w http.ResponseWriter, r *http.Request, account state.Account) {
	st := managedpg.SuspendTimeouts{Problem: managedPostgresProblem, NotConfigured: managedPostgresNotConfiguredProblem, Audit: s.audit}
	if s.managedPostgres != nil {
		st.Service = s.managedPostgres
	}
	st.Put(w, r, account)
}

// managedPostgresWakeWithAppOrLog reads which databases start with their
// apps; a failed read leaves the field out of the view.
func (s *server) managedPostgresWakeWithAppOrLog(ctx context.Context, databases []managedpostgres.Database) map[string]bool {
	if s.managedPostgres == nil {
		return nil
	}
	enabled, err := s.managedPostgres.WakeWithApp(ctx, databases)
	if err != nil {
		s.log.Warn("managed postgres wake with app: read", "err", err)
		return nil
	}
	return enabled
}

// setManagedPostgresWakeWithApp serves PUT .../wake-with-app with the
// service as configured when the request arrives.
func (s *server) setManagedPostgresWakeWithApp(w http.ResponseWriter, r *http.Request, account state.Account) {
	h := managedpg.WakeWithApp{Problem: managedPostgresProblem, NotConfigured: managedPostgresNotConfiguredProblem, Audit: s.audit}
	if s.managedPostgres != nil {
		h.Service = s.managedPostgres
	}
	h.Put(w, r, account)
}

// getManagedPostgresActivity serves GET .../activity with the service as
// configured when the request arrives.
func (s *server) getManagedPostgresActivity(w http.ResponseWriter, r *http.Request, account state.Account) {
	h := managedpg.Activity{Problem: managedPostgresProblem, NotConfigured: managedPostgresNotConfiguredProblem}
	if s.managedPostgres != nil {
		h.Service = s.managedPostgres
	}
	h.Get(w, r, account)
}

// managedPostgresDatabaseUsageOrLog splits the month's compute by database;
// a failed read leaves the split out of the view.
func (s *server) managedPostgresDatabaseUsageOrLog(ctx context.Context, account state.Account, since time.Time) []api.ManagedPostgresDatabaseUsage {
	totals, names, err := s.managedPostgres.DatabaseUsage(ctx, account.ResourceOwnerID(), since, time.Now().UTC())
	if err != nil {
		s.log.Warn("managed postgres database usage: read", "err", err)
		return nil
	}
	return managedpg.DatabaseUsageView(totals, names)
}
