package apidserver

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) activateManagedPostgresMigration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if !s.capabilityEnabledFor("database-rollbacks", acct, app) {
		writeMissingRoute(w, r)
		return
	}
	switches, ok := s.databaseSwitchStore()
	if !ok || s.databaseSwitchWorker() == nil || s.managedPostgresBindings == nil {
		api.WriteProblem(w, api.ErrDatabaseSwitchUnavailable("Managed PostgreSQL switches are unavailable.", ""))
		return
	}
	p, ok := principalFrom(r)
	if !ok || !principalHasScope(p, api.ScopesManagedPostgresManageSurface) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Insufficient scope",
			"moving managed PostgreSQL bindings requires the postgres:manage scope"))
		return
	}
	if !actorAccount(r, acct).EmailVerified() {
		api.WriteProblem(w, api.ErrEmailVerificationRequired())
		return
	}
	var req api.ActivateManagedPostgresMigrationRequest
	if err := decodeJSONSized(r, &req, 8<<10); err != nil || !req.SourceWritesPaused ||
		!dbguest.ValidOperationID(req.SourceDatabaseID) || !dbguest.ValidOperationID(req.TargetDatabaseID) ||
		!dbguest.ValidOperationID(req.OperationID) || !dbguest.ValidOperationID(req.VerificationID) {
		api.WriteProblem(w, api.ErrValidation("provide both database IDs, the migration and verification UUIDs, and acknowledge paused source writes"))
		return
	}
	req.Scope = strings.TrimSpace(req.Scope)
	if req.Scope == "" {
		req.Scope = state.DefaultEnvScope
	}
	ctx := r.Context()
	if existing, err := switches.DatabaseSwitch(ctx, acct.ID, req.VerificationID); err == nil {
		writeMigrationSwitchRetry(w, s.migrationDatabaseSwitchView(ctx, existing), existing, app, req)
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, databaseSwitchCreateProblem(app, err))
		return
	}
	if !s.databaseSwitchCanVerify(w, r, app) {
		return
	}
	sw, problem := s.migrationSwitchFor(r, acct, app, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	created, err := switches.CreateDatabaseSwitch(ctx, sw)
	if errors.Is(err, state.ErrConflict) {
		if existing, readErr := switches.DatabaseSwitch(ctx, acct.ID, req.VerificationID); readErr == nil {
			writeMigrationSwitchRetry(w, s.migrationDatabaseSwitchView(ctx, existing), existing, app, req)
			return
		}
	}
	if problem := databaseSwitchCreateProblem(app, err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(ctx, "app.database_switch_requested", &acct.ID, map[string]any{
		"app_id": app.ID, "switch_id": created.ID, "mode": "live_migration",
		"source_database_id": created.SourceDatabaseID, "target_database_id": created.TargetDatabaseID,
		"migration_operation_id": created.MigrationOperationID,
	})
	s.appendDatabaseSwitchAudit(ctx, created, "requested")
	writeJSON(w, http.StatusAccepted, s.migrationDatabaseSwitchView(ctx, created))
}

func writeMigrationSwitchRetry(w http.ResponseWriter, view api.MigrationDatabaseSwitch, sw state.DatabaseSwitch, app state.App, req api.ActivateManagedPostgresMigrationRequest) {
	if sw.AppID != app.ID || sw.Scope != req.Scope || sw.MigrationOperationID != req.OperationID ||
		sw.SourceDatabaseID != req.SourceDatabaseID || sw.TargetDatabaseID != req.TargetDatabaseID {
		api.WriteProblem(w, api.ErrDatabaseSwitchUnavailable("This verification ID already names another database switch.", ""))
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

func (s *server) migrationSwitchFor(r *http.Request, acct state.Account, app state.App, req api.ActivateManagedPostgresMigrationRequest) (state.DatabaseSwitch, *api.Problem) {
	ctx := r.Context()
	source, err := s.managedPostgres.Get(ctx, acct.ID, req.SourceDatabaseID)
	if err != nil {
		return state.DatabaseSwitch{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such managed PostgreSQL source")
	}
	target, err := s.managedPostgres.Get(ctx, acct.ID, req.TargetDatabaseID)
	if err != nil {
		return state.DatabaseSwitch{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such managed PostgreSQL target")
	}
	if err := s.managedPostgres.ValidateMigrationActivationDatabases(source, target); err != nil {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable("Activation requires a supported managed source and a ready single-zone native target.", "")
	}
	sourceBindings, err := s.managedPostgresBindings.List(ctx, acct.ID, source.ID)
	if err != nil {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable("Gregale could not read the source bindings.", "")
	}
	var bound bool
	for _, b := range sourceBindings {
		if b.State == managedpostgres.BindingStateDeleted {
			continue
		}
		if b.AppID != app.ID || recoveryScope(b.Scope) != req.Scope || b.State != managedpostgres.BindingStateReady {
			return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable("Activation requires ready source bindings dedicated to this app environment.", "")
		}
		bound = true
	}
	if !bound {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable("The app needs a managed binding to the source in this environment.", "")
	}
	targetBindings, err := s.managedPostgresBindings.List(ctx, acct.ID, target.ID)
	if err != nil {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable("Gregale could not read the target bindings.", "")
	}
	for _, b := range targetBindings {
		if b.State != managedpostgres.BindingStateDeleted {
			return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable("Activation requires a dedicated target with no existing app bindings.", "")
		}
	}
	live, err := s.store.LiveDeploymentForScope(ctx, app.ID, req.Scope)
	if err != nil {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable("The environment needs a live deployment.", "")
	}
	if _, split := s.trafficSplitEarlierRelease(ctx, app.ID, req.Scope); split {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable("Return all traffic to one release before activating the migration.", "")
	}
	progress, err := s.managedPostgres.CustomerMigrationStatus(ctx, acct.ID, target.ID)
	if err != nil {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable("Gregale could not read the native migration receipt.", "")
	}
	now := time.Now().UTC()
	v := progress.Verification
	if v == nil || progress.OperationID != req.OperationID || progress.State == dbmigration.ResyncRequired ||
		progress.State == dbmigration.Stopped || progress.State == dbmigration.Activated ||
		!dbmigration.FreshVerification(&dbmigration.Verification{ID: v.ID, State: v.State, VerifiedAt: v.VerifiedAt}, req.VerificationID, now) {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable("A fresh verification receipt for this migration is required.", "Keep source writers paused and run migration verification.")
	}
	return state.DatabaseSwitch{
		ID: req.VerificationID, AccountID: acct.ID, AppID: app.ID, Scope: req.Scope,
		FromDeploymentID: live.ID, TargetDeploymentID: live.ID, SourceDatabaseID: source.ID, TargetDatabaseID: target.ID,
		TargetName: target.Name, MigrationOperationID: req.OperationID, RequestedBy: releaseApprovalActorLabel(r, acct), CreatedAt: now,
	}, nil
}
