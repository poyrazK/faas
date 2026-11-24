package apidserver

import (
	"context"
	"net/http"

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
