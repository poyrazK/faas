package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/postgresaccess"
	"github.com/onebox-faas/faas/pkg/apid/previewdb"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewDatabasesCapability gates PR preview databases (ADR-20261102123523)
// until the owner has tested them on production.
const previewDatabasesCapability = "preview-databases"

// previewDatabaseManager is nil while managed PostgreSQL is not configured.
func (s *server) previewDatabaseManager() *previewdb.Manager {
	if s.managedPostgres == nil || s.managedPostgresBindings == nil {
		return nil
	}
	return &previewdb.Manager{Databases: s.managedPostgres, Bindings: s.managedPostgresBindings}
}

// preparePreviewDatabases gives a PR preview its own databases for the
// production databases with previews on. It returns previewdb.ErrPending
// while one is provisioning and a *previewdb.RefusedError when setup is
// refused. Organization-owned databases are not set up yet.
func (s *server) preparePreviewDatabases(ctx context.Context, acct state.Account, plan api.Plan, parent, preview state.App) error {
	manager := s.previewDatabaseManager()
	if manager == nil || !s.capabilityEnabledFor(previewDatabasesCapability, acct, parent) {
		return nil
	}
	ctx, err := postgresaccess.PersonalContext(ctx, s.store, parent.AccountID)
	if err != nil {
		return err
	}
	return manager.Prepare(ctx, plan, parent, preview)
}

// prepareBuildPreviewDatabases is preparePreviewDatabases for a preview
// build, which knows only the preview. A preview whose production app is gone
// gets no databases.
func (s *server) prepareBuildPreviewDatabases(ctx context.Context, acct state.Account, plan api.Plan, preview state.App) error {
	if s.previewDatabaseManager() == nil || preview.PreviewOfSlug == "" || preview.PreviewPrNumber <= 0 {
		return nil
	}
	parent, err := s.store.AppBySlug(ctx, preview.PreviewOfSlug)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load production app %s: %w", preview.PreviewOfSlug, err)
	}
	return s.preparePreviewDatabases(ctx, acct, plan, parent, preview)
}

// releasePreviewDatabases removes a torn-down PR preview's bindings and
// purges the environment's databases no other preview binds. It runs with or
// without the capability, so a database set up before it closed is removed.
func (s *server) releasePreviewDatabases(ctx context.Context, preview state.App) error {
	manager := s.previewDatabaseManager()
	if _, _, ok := previewdb.Environment(preview); manager == nil || !ok {
		return nil
	}
	ctx, err := postgresaccess.PersonalContext(ctx, s.store, preview.AccountID)
	if err != nil {
		return err
	}
	return manager.Release(ctx, preview)
}

// previewDatabase reports whether id names a PR preview environment's
// database. A failed read reports no: the caller's own path answers it.
func (s *server) previewDatabase(ctx context.Context, acct state.Account, id string) (managedpostgres.Database, bool) {
	if s.previewDatabaseManager() == nil {
		return managedpostgres.Database{}, false
	}
	database, err := s.managedPostgres.Get(ctx, acct.ID, id)
	return database, err == nil && !database.Preview.IsZero()
}

// previewDatabasesProblem answers the preview API path's setup error. A
// database still provisioning is no error there: the reconcilers finish it
// and its binding delivers DATABASE_URL when it is ready.
func previewDatabasesProblem(err error) *api.Problem {
	var refused *previewdb.RefusedError
	switch {
	case err == nil || errors.Is(err, previewdb.ErrPending):
		return nil
	case errors.As(err, &refused):
		status, code, title := http.StatusConflict, "managed_postgres_conflict", "PR preview database refused"
		if errors.Is(err, managedpostgres.ErrQuotaExceeded) {
			status, code, title = managedpostgres.QuotaProblem(err)
		}
		return api.NewProblem(status, code, title, "The preview's managed PostgreSQL database: "+refused.Reason+".")
	default:
		return api.ErrCapacity("could not set up the preview's managed PostgreSQL database")
	}
}

// setManagedPostgresPreviews changes what a database gives PR preview
// environments (PUT /v1/postgres/databases/{id}/previews).
func (s *server) setManagedPostgresPreviews(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var req api.SetManagedPostgresPreviewsRequest
	if err := decodeJSON(r, &req); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	database, err := s.managedPostgres.SetPreviews(r.Context(), acct.ID, r.PathValue("id"), managedpostgres.PreviewMode(req.Previews))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.database.previews_changed", &acct.ID, map[string]any{
		"database_id": database.ID, "previews": req.Previews,
	})
	writeJSON(w, http.StatusOK, managedPostgresView(database))
}
