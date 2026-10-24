package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/databaseswitch"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state"
)

// Rollbacks that restore the database (ADR-20261023181931).

func (s *server) databaseSwitchStore() (state.DatabaseSwitchStore, bool) {
	store, ok := s.store.(state.DatabaseSwitchStore)
	return store, ok
}

// databaseSwitchDrain reads drain evidence from the store; tests on the
// in-memory store set databaseSwitchDrainOverride.
func (s *server) databaseSwitchDrain() (state.DatabaseSwitchDrainStore, bool) {
	if s.databaseSwitchDrainOverride != nil {
		return s.databaseSwitchDrainOverride, true
	}
	drain, ok := s.store.(state.DatabaseSwitchDrainStore)
	return drain, ok
}

// databaseSwitchWorker is nil when the store or managed PostgreSQL cannot
// run switches.
func (s *server) databaseSwitchWorker() *databaseswitch.Worker {
	switches, ok := s.databaseSwitchStore()
	drain, drainOK := s.databaseSwitchDrain()
	if !ok || !drainOK || s.managedPostgres == nil || s.managedPostgresCutovers == nil {
		return nil
	}
	return &databaseswitch.Worker{
		Store: switches, Drain: drain, Databases: s.managedPostgres, Cutovers: s.managedPostgresCutovers, Releases: s.store,
		InvalidateSnapshots: func(ctx context.Context, appID string) error {
			_, err := state.InvalidateAppSnapshots(ctx, s.store, appID)
			return err
		},
		AppChanged: func(ctx context.Context, appID string) { _ = s.notif.Notify(ctx, db.NotifyAppChanged, appID) },
		Observe:    s.observeDatabaseSwitch,
		Log:        s.log,
	}
}

// runDatabaseSwitchWorker advances switches. Concurrent apid processes are
// safe: each switch is leased before a step.
func (s *server) runDatabaseSwitchWorker(ctx context.Context) {
	worker := s.databaseSwitchWorker()
	if worker == nil {
		return
	}
	worker.Run(ctx)
}

// startDatabaseSwitch records a rollback that restores the database instead
// of preparing a plain rollback. The plan's checks have passed; the switch
// performs the code rollback while the app is fenced.
func (s *server) startDatabaseSwitch(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, req api.RollbackRequest) {
	ctx := r.Context()
	if req.ExpectedCurrentDeploymentID != nil {
		api.WriteProblem(w, api.ErrValidation("restore_databases cannot be combined with expected_current_deployment_id; the switch checks the live release itself"))
		return
	}
	switches, ok := s.databaseSwitchStore()
	if !ok || s.databaseSwitchWorker() == nil || s.managedPostgresBindings == nil {
		api.WriteProblem(w, api.ErrDatabaseSwitchUnavailable("Managed PostgreSQL is not configured on this platform.", ""))
		return
	}
	if p, ok := principalFrom(r); !ok || !principalHasScope(p, api.ScopesManagedPostgresManageSurface) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Insufficient scope",
			"restore_databases restores and rebinds a managed PostgreSQL database; the key needs the postgres:manage scope as well"))
		return
	}
	plan, problem := s.planRollback(r, acct, app, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if earlier, split := s.trafficSplitEarlierRelease(ctx, app.ID, plan.target.Scope); split {
		api.WriteProblem(w, rollbackDuringTrafficSplitProblem(app.Slug, earlier))
		return
	}
	sw, problem := s.databaseSwitchFor(ctx, r, acct, app, plan)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	created, err := switches.CreateDatabaseSwitch(ctx, sw)
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeDatabaseSwitchInProgress, "Database switch in progress",
			"This app already has a database switch that has not finished.").
			WithHint(fmt.Sprintf("Follow it with gregale rollback switches %s, or cancel it before it starts switching.", app.Slug)))
		return
	}
	if err != nil {
		api.WriteProblem(w, customerCapacityProblem(s.log, "record database switch", "Rollback temporarily unavailable",
			"Gregale could not record the database switch.", "Retry the request in a moment; if it continues, contact support.", err))
		return
	}
	s.recordDatabaseSwitchRequested(ctx, r, acct, app, plan, created)
	response := s.deploymentResponse(ctx, plan.target, app)
	view := s.databaseSwitchView(ctx, created)
	response.DatabaseSwitch = &view
	writeJSON(w, http.StatusAccepted, response)
}

// databaseSwitchFor builds the switch for a checked rollback plan: one
// migratable managed database, restored to before the earliest crossed
// release.
func (s *server) databaseSwitchFor(ctx context.Context, r *http.Request, acct state.Account, app state.App, plan rollbackPlan) (state.DatabaseSwitch, *api.Problem) {
	if len(plan.crossed) == 0 {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable(
			"This rollback crosses no release command, so there is nothing to restore.", "Roll back without restore_databases.")
	}
	databases, err := s.releaseMigrationDatabases(ctx, acct, app, plan.target.Scope)
	if err != nil {
		return state.DatabaseSwitch{}, customerCapacityProblem(s.log, "list release migration databases", "Rollback temporarily unavailable",
			"Gregale could not list the databases the release commands migrate.", "Retry the request in a moment.", err)
	}
	switch {
	case len(databases) == 0:
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable(
			"No managed PostgreSQL database receives this scope's migration binding, so Gregale cannot restore what the release commands changed.",
			"Restore your database yourself, then roll back with acknowledge_releases.")
	case len(databases) > 1:
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable(
			fmt.Sprintf("The release commands can migrate %d managed databases; a switch restores one.", len(databases)),
			"Restore each database with gregale postgres restore, then roll back with acknowledge_releases.")
	}
	source, err := s.managedPostgres.Get(ctx, acct.ID, databases[0].ID)
	if err != nil {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable("The database the release commands migrate cannot be read: "+err.Error()+".", "")
	}
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok || limits.DatabasesMax == 0 || !managedPostgresPlanAllows(limits, source.Spec) {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable("The account's plan does not allow restoring this database.", "Upgrade the plan, or restore it another way.")
	}
	point, name := release.SwitchRestore(plan.crossed, databases[0])
	if window := time.Duration(source.Spec.RestoreWindowSeconds) * time.Second; window > 0 && time.Since(point) > window {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable(
			fmt.Sprintf("The restore point %s is outside %s's %s restore window.", point.Format(time.RFC3339), source.Name, window),
			"Roll back with acknowledge_releases and repair the database by hand.")
	}
	undone := make([]string, 0, len(plan.crossed))
	for _, run := range plan.crossed {
		if !slices.Contains(undone, run.DeploymentID) {
			undone = append(undone, run.DeploymentID)
		}
	}
	return state.DatabaseSwitch{AccountID: acct.ID, AppID: app.ID, Scope: plan.target.Scope, FromDeploymentID: plan.current.ID,
		TargetDeploymentID: plan.target.ID, UndoneDeploymentIDs: undone, SourceDatabaseID: source.ID, TargetName: name,
		RestorePoint: point, RequestedBy: releaseApprovalActorLabel(r, acct), CreatedAt: time.Now().UTC()}, nil
}

// releaseMigrationDatabases lists the managed databases whose migration
// binding reaches the app scope (ADR-1448). The PostgreSQL store answers in
// one query; other stores are read through the binding service.
func (s *server) releaseMigrationDatabases(ctx context.Context, acct state.Account, app state.App, scope string) ([]state.ReleaseDatabase, error) {
	if catalog, ok := s.store.(state.ReleaseMigrationDatabaseStore); ok {
		return catalog.ReleaseMigrationDatabases(ctx, app.ID, scope)
	}
	if scope == "" {
		scope = state.DefaultEnvScope
	}
	databases, err := s.managedPostgres.List(ctx, acct.ID)
	if err != nil {
		return nil, err
	}
	var out []state.ReleaseDatabase
	for _, database := range databases {
		bindings, err := s.managedPostgresBindings.List(ctx, acct.ID, database.ID)
		if err != nil {
			return nil, err
		}
		for _, b := range bindings {
			if b.AppID == app.ID && b.Scope == scope && b.Access == managedpostgres.CredentialMigration && b.State != managedpostgres.BindingStateDeleted {
				out = append(out, state.ReleaseDatabase{ID: database.ID, Name: database.Name})
				break
			}
		}
	}
	return out, nil
}

func (s *server) databaseSwitchView(ctx context.Context, sw state.DatabaseSwitch) api.DatabaseSwitch {
	view := databaseswitch.View(sw, func(id string) int {
		d, err := s.store.DeploymentByID(ctx, id)
		if err != nil {
			return 0
		}
		return d.Revision
	})
	if s.managedPostgres != nil {
		if source, err := s.managedPostgres.Get(ctx, sw.AccountID, sw.SourceDatabaseID); err == nil {
			view.SourceDatabaseName = source.Name
		}
	}
	return view
}

func (s *server) listDatabaseSwitches(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	switches, ok := s.databaseSwitchStore()
	if !ok {
		writeJSON(w, http.StatusOK, api.DatabaseSwitchList{Items: []api.DatabaseSwitch{}})
		return
	}
	rows, err := switches.ListDatabaseSwitches(r.Context(), acct.ID, app.ID, api.DatabaseSwitchListMax)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list database switches"))
		return
	}
	out := api.DatabaseSwitchList{Items: make([]api.DatabaseSwitch, 0, len(rows))}
	for _, sw := range rows {
		out.Items = append(out.Items, s.databaseSwitchView(r.Context(), sw))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) loadDatabaseSwitch(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.DatabaseSwitch, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.App{}, state.DatabaseSwitch{}, false
	}
	switches, ok := s.databaseSwitchStore()
	if !ok {
		s.notFound(w, "no such database switch")
		return state.App{}, state.DatabaseSwitch{}, false
	}
	sw, err := switches.DatabaseSwitch(r.Context(), acct.ID, r.PathValue("id"))
	if errors.Is(err, state.ErrNotFound) || (err == nil && sw.AppID != app.ID) {
		s.notFound(w, "no such database switch")
		return state.App{}, state.DatabaseSwitch{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read database switch"))
		return state.App{}, state.DatabaseSwitch{}, false
	}
	return app, sw, true
}

func (s *server) getDatabaseSwitch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, sw, ok := s.loadDatabaseSwitch(w, r, acct)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.databaseSwitchView(r.Context(), sw))
}

// cancelDatabaseSwitch records a cancellation request; the worker unwinds the
// switch on its next step. A switch that started switching cannot be
// cancelled.
func (s *server) cancelDatabaseSwitch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, sw, ok := s.loadDatabaseSwitch(w, r, acct)
	if !ok {
		return
	}
	switches, _ := s.databaseSwitchStore()
	updated, err := switches.RequestDatabaseSwitchCancel(r.Context(), acct.ID, app.ID, sw.ID, time.Now().UTC())
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeDatabaseSwitchNotCancellable, "Database switch cannot be cancelled",
			fmt.Sprintf("The switch is %s; it can be cancelled only before it starts switching the release and the bindings.", updated.Phase)))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not cancel database switch"))
		return
	}
	s.audit.Emit(r.Context(), "app.database_switch_cancel_requested", &acct.ID, map[string]any{"app_id": app.ID, "switch_id": sw.ID})
	writeJSON(w, http.StatusAccepted, s.databaseSwitchView(r.Context(), updated))
}

func (s *server) recordDatabaseSwitchRequested(ctx context.Context, r *http.Request, acct state.Account, app state.App, plan rollbackPlan, sw state.DatabaseSwitch) {
	s.log.Info("app rollback with database switch requested", "app", app.ID, "from", plan.current.ID, "to", plan.target.ID,
		"switch", sw.ID, "source_database", sw.SourceDatabaseID, "restore_point", sw.RestorePoint)
	s.audit.Emit(ctx, "app.database_switch_requested", &acct.ID, map[string]any{
		"app_id": app.ID, "switch_id": sw.ID, "from": plan.current.ID, "to": plan.target.ID, "mode": plan.mode,
		"source_database_id": sw.SourceDatabaseID, "restore_point": sw.RestorePoint.Format(time.RFC3339),
		"undone_deployment_ids": sw.UndoneDeploymentIDs,
	})
	s.appendDatabaseSwitchAudit(ctx, sw, "requested")
	activity := state.OrgActivity{
		Kind: "deploy.rollback_requested", SourceType: "rollback.database_switch", SourceID: activitySourceID(r, sw.ID),
		Data: activityData(map[string]any{"from": plan.current.ID, "to": plan.target.ID, "mode": plan.mode, "phase": "database_switch_requested"}),
	}
	if targetID, err := uuid.Parse(plan.target.ID); err == nil {
		activity.DeploymentID = &targetID
	}
	s.recordAppActivity(ctx, r, acct, app, activity)
}

// observeDatabaseSwitch records the worker's steps in the audit log and on
// the deployment the switch rolls away from.
func (s *server) observeDatabaseSwitch(ctx context.Context, sw state.DatabaseSwitch, event databaseswitch.Event) {
	s.audit.Emit(ctx, "app.database_switch_"+string(event), &sw.AccountID, map[string]any{
		"app_id": sw.AppID, "switch_id": sw.ID, "phase": sw.Phase, "failure_code": sw.FailureCode,
		"target_database_id": sw.TargetDatabaseID, "drain": sw.Drain,
	})
	s.appendDatabaseSwitchAudit(ctx, sw, string(event))
}

func (s *server) appendDatabaseSwitchAudit(ctx context.Context, sw state.DatabaseSwitch, step string) {
	deploymentID := sw.FromDeploymentID
	if deploymentID == "" {
		deploymentID = sw.TargetDeploymentID
	}
	depUUID, err := uuid.Parse(deploymentID)
	if err != nil {
		return
	}
	var acctUUID *uuid.UUID
	if parsed, err := uuid.Parse(sw.AccountID); err == nil {
		acctUUID = &parsed
	}
	data := safetext.JSONObject(map[string]any{
		"from": sw.FromDeploymentID, "to": sw.TargetDeploymentID, "phase": "database_switch_" + step,
		"switch_id": sw.ID, "source_database_id": sw.SourceDatabaseID, "target_database_id": sw.TargetDatabaseID,
		"restore_point": sw.RestorePoint.Format(time.RFC3339), "failure_code": sw.FailureCode,
		"crossed_releases": sw.UndoneDeploymentIDs,
	})
	if _, err := s.store.AppendDeploymentAudit(ctx, state.DeploymentAudit{
		DeploymentID: depUUID, AccountID: acctUUID, Kind: state.DeployRolledBack, Actor: "apid:database_switch",
		At: time.Now().UTC(), Data: json.RawMessage(data),
	}); err != nil {
		s.log.Warn("database switch: append deployment_audit failed", "switch", sw.ID, "err", err)
	}
}
