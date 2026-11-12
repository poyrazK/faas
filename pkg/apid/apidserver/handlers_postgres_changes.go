package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Postgres change workers (ADR-20261112140000): an app declares feeds of a
// database; changes are captured into the database's Commit outbox and the
// relay enqueues them into one of the app's queues.

const postgresChangesBodyLimit = 64 << 10

func postgresChangesSourceName(app state.App) string {
	return "postgres-changes-" + app.ID
}

// postgresChangesStore gates the request on the release flag and the plan
// and returns the store surface the handlers need.
func (s *server) postgresChangesStore(w http.ResponseWriter, acct state.Account) (state.CommitChangesStore, bool) {
	if !s.platformFlagEnabled(platformflags.PostgresChanges, acct) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "postgres_changes_unavailable",
			"Postgres change workers unavailable", "Postgres change workers are not released to this account yet"))
		return nil, false
	}
	if api.MustLimitsFor(acct.Plan).MaxQueueDepth == 0 {
		api.WriteProblem(w, api.ErrPlanFeatureGated("postgres_changes", acct.Plan))
		return nil, false
	}
	store, ok := s.store.(state.CommitChangesStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("postgres changes store unavailable"))
		return nil, false
	}
	return store, true
}

func (s *server) putPostgresChanges(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.postgresChangesStore(w, acct)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.PutPostgresChangesRequest
	if !decodeJSONLimit(w, r, &req, postgresChangesBodyLimit) {
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	ctx := r.Context()
	var database managedpostgres.Database
	if req.Database != "" {
		var problem *api.Problem
		if ctx, database, problem = s.postgresChangesDatabase(ctx, acct, req.Database); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	feeds, err := json.Marshal(req.Feeds)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("encode feeds"))
		return
	}
	src, err := store.UpsertCommitChangesSource(ctx, acct.ID, app.ID, postgresChangesSourceName(app), state.CommitChangesConfig{
		QueueName: req.QueueName, Feeds: feeds, ManagedDatabaseID: database.ID,
		PollIntervalMS: int(req.PollInterval().Milliseconds()),
	})
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "postgres_changes_conflict", "Postgres changes conflict",
			"this app already has a different Commit source; remove it first"))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("save postgres changes"))
		return
	}
	if !src.Enabled {
		if _, err := store.SetCommitSourceEnabled(ctx, acct.ID, src.ID, true); err != nil {
			api.WriteProblem(w, api.ErrCapacity("resume postgres changes"))
			return
		}
	}
	if database.ID != "" {
		if problem := s.installManagedPostgresChanges(ctx, store, acct, database, src.ID, req.Feeds); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	s.writePostgresChanges(w, r, acct, app, http.StatusOK)
}

// postgresChangesDatabase resolves a managed database the account owns, by
// name or ID, under the same ownership scope as manifest dependencies.
func (s *server) postgresChangesDatabase(ctx context.Context, acct state.Account, reference string) (context.Context, managedpostgres.Database, *api.Problem) {
	if s.managedPostgres == nil {
		return ctx, managedpostgres.Database{}, api.ErrCapacity("managed PostgreSQL unavailable")
	}
	if managedpostgres.Organization(ctx) == "" {
		org, err := s.store.OrgByPersonalAccount(ctx, acct.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return ctx, managedpostgres.Database{}, api.ErrCapacity("could not resolve database ownership")
		}
		ctx = managedpostgres.WithOwnership(ctx, org.ID, acct.ID)
	}
	databases, err := s.managedPostgres.List(ctx, acct.ID)
	if err != nil {
		return ctx, managedpostgres.Database{}, managedPostgresManifestProblem(err, "could not list managed PostgreSQL databases")
	}
	database, err := resolveManagedPostgresDatabaseRecord(databases, reference)
	if err != nil {
		return ctx, managedpostgres.Database{}, api.ErrValidation(err.Error())
	}
	return ctx, database, nil
}

// installManagedPostgresChanges installs capture as the customer schema owner
// and seals the least-privilege relay login it creates. The owner credential
// never leaves the provider's pipeline session.
func (s *server) installManagedPostgresChanges(ctx context.Context, store state.CommitChangesStore, acct state.Account, database managedpostgres.Database, sourceID string, feeds []api.PostgresChangeFeed) *api.Problem {
	password, err := commitwork.NewRelayPassword()
	if err != nil {
		return api.ErrCapacity("generate relay credential")
	}
	var relayURL string
	var installErr error
	err = s.managedPostgres.WithPipelineSQL(ctx, acct.ID, database.ID, func(conn *pgx.Conn, owner string) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		res, err := commitwork.InstallChanges(ctx, tx, commitwork.ChangesInstall{SourceID: sourceID, Feeds: feeds, RelayPassword: password, SchemaOwner: owner})
		if err != nil {
			installErr = err
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		cfg := conn.Config()
		relayURL, err = commitwork.ChangesRelayURL(cfg.Host, cfg.Port, cfg.Database, res.RelayRole, password)
		return err
	})
	switch {
	case errors.Is(installErr, commitwork.ErrDatabaseBound):
		return api.NewProblem(http.StatusConflict, "postgres_changes_database_bound", "Database already bound",
			"this database already feeds another app's Postgres changes or Commit source")
	case installErr != nil:
		// The database's own refusal (a missing table, a primary key that
		// is not allowlisted) is about the customer's schema.
		return api.NewProblem(http.StatusUnprocessableEntity, "postgres_changes_install_failed", "Install failed", installErr.Error())
	case err != nil:
		return managedPostgresManifestProblem(err, "could not install Postgres change capture")
	}
	return s.sealPostgresChangesConnection(ctx, store, acct, sourceID, relayURL)
}

func (s *server) sealPostgresChangesConnection(ctx context.Context, store state.CommitChangesStore, acct state.Account, sourceID, raw string) *api.Problem {
	recipients, err := commitConnectionRecipients()
	if err != nil {
		return api.ErrCapacity("credential encryption unavailable")
	}
	blob, err := commitwork.SealConnectionTo(recipients, sourceID, raw)
	if err != nil {
		return api.ErrCapacity("seal relay credential")
	}
	if err := store.SetCommitSourceConnection(ctx, acct.ID, sourceID, blob); err != nil {
		return api.ErrCapacity("store relay credential")
	}
	return nil
}

func (s *server) getPostgresChanges(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.postgresChangesStore(w, acct); !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	s.writePostgresChanges(w, r, acct, app, http.StatusOK)
}

func (s *server) writePostgresChanges(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, status int) {
	store, ok := s.store.(state.CommitChangesStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("postgres changes store unavailable"))
		return
	}
	src, cfg, err := store.CommitChangesSourceForApp(r.Context(), acct.ID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "postgres_changes_not_found", "No Postgres changes",
			"this app does not capture Postgres changes"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("read postgres changes"))
		return
	}
	role, _ := commitwork.ChangesRelayRole(src.ID)
	resp := api.PostgresChangesResponse{
		SourceID: src.ID, QueueName: cfg.QueueName, Managed: cfg.ManagedDatabaseID != "",
		PollIntervalSeconds: cfg.PollIntervalMS / 1000, Enabled: src.Enabled, Connected: cfg.Connected,
		RelayRole: role, RelayStatus: src.RelayStatus, LastCheckedAt: src.LastCheckedAt,
		PendingEvents: src.PendingEvents, BlockedEvents: src.BlockedEvents, OldestPendingAt: src.OldestPendingAt,
	}
	if err := json.Unmarshal(cfg.Feeds, &resp.Feeds); err != nil {
		api.WriteProblem(w, api.ErrCapacity("decode feeds"))
		return
	}
	if resp.Managed && s.managedPostgres != nil {
		if database, err := s.managedPostgres.Get(r.Context(), acct.ID, cfg.ManagedDatabaseID); err == nil {
			resp.Database = database.Name
		}
	}
	writeJSON(w, status, resp)
}

// putPostgresChangesConnection stores the relay login a BYO install created.
// Only the source's relay role is accepted, so an owner credential pasted by
// mistake is refused rather than stored.
func (s *server) putPostgresChangesConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.postgresChangesStore(w, acct)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	src, cfg, err := store.CommitChangesSourceForApp(r.Context(), acct.ID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "postgres_changes_not_found", "No Postgres changes",
			"declare the app's Postgres changes before connecting a database"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("read postgres changes"))
		return
	}
	if cfg.ManagedDatabaseID != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "postgres_changes_managed", "Managed database",
			"Gregale connects managed databases itself"))
		return
	}
	var req struct {
		Connection string `json:"connection_url"`
	}
	if !decodeJSONLimit(w, r, &req, 10<<10) {
		return
	}
	if err := commitwork.ValidateConnection(req.Connection); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	role, _ := commitwork.ChangesRelayRole(src.ID)
	if u, err := url.Parse(req.Connection); err != nil || u.User.Username() != role {
		api.WriteProblem(w, api.ErrValidation("connection_url must log in as "+role+", the relay role `gregale postgres-changes install` creates; never send an owner credential"))
		return
	}
	if problem := s.sealPostgresChangesConnection(r.Context(), store, acct, src.ID, req.Connection); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// deletePostgresChanges pauses the app's capture. On a managed database it
// also drops the capture triggers and the relay login; on a database you run,
// `gregale postgres-changes uninstall` does that.
func (s *server) deletePostgresChanges(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.postgresChangesStore(w, acct)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	src, cfg, err := store.CommitChangesSourceForApp(r.Context(), acct.ID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("read postgres changes"))
		return
	}
	if _, err := store.SetCommitSourceEnabled(r.Context(), acct.ID, src.ID, false); err != nil {
		api.WriteProblem(w, api.ErrCapacity("pause postgres changes"))
		return
	}
	if cfg.ManagedDatabaseID != "" && s.managedPostgres != nil {
		ctx := r.Context()
		if managedpostgres.Organization(ctx) == "" {
			if org, err := s.store.OrgByPersonalAccount(ctx, acct.ID); err == nil {
				ctx = managedpostgres.WithOwnership(ctx, org.ID, acct.ID)
			}
		}
		err := s.managedPostgres.WithPipelineSQL(ctx, acct.ID, cfg.ManagedDatabaseID, func(conn *pgx.Conn, owner string) error {
			tx, err := conn.Begin(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := commitwork.RemoveChanges(ctx, tx, src.ID, owner); err != nil {
				return err
			}
			return tx.Commit(ctx)
		})
		if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			api.WriteProblem(w, managedPostgresManifestProblem(err, "paused, but could not remove capture from the database; retry"))
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
