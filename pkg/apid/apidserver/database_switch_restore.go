package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Restores that switch the app and keep its code: `gregale postgres restore
// --switch` (ADR-20261023181931 amendment 3). The switch is the one a
// rollback that restores the database runs, with the live release as its
// target, so it fences, drains and moves the bindings the same way and never
// changes the code.

// createDatabaseSwitch handles POST /v1/apps/{slug}/database-switches.
func (s *server) createDatabaseSwitch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	switches, ok := s.databaseSwitchStore()
	if !ok || s.databaseSwitchWorker() == nil || s.managedPostgresBindings == nil {
		api.WriteProblem(w, api.ErrDatabaseSwitchUnavailable("Managed PostgreSQL is not configured on this platform.", ""))
		return
	}
	if !s.databaseSwitchCanVerify(w, r, app) {
		return
	}
	if p, ok := principalFrom(r); !ok || !principalHasScope(p, api.ScopesManagedPostgresManageSurface) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Insufficient scope",
			"a database switch restores and rebinds a managed PostgreSQL database; the key needs the postgres:manage scope as well"))
		return
	}
	// The switch creates a billable restored database, as a restore does.
	if !actorAccount(r, acct).EmailVerified() {
		api.WriteProblem(w, api.ErrEmailVerificationRequired())
		return
	}
	var req api.CreateDatabaseSwitchRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid database switch request: "+err.Error()))
		return
	}
	sw, problem := s.keepReleaseSwitchFor(r, acct, app, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	created, err := switches.CreateDatabaseSwitch(r.Context(), sw)
	if problem := databaseSwitchCreateProblem(app, err); problem != nil {
		if problem.Status >= http.StatusInternalServerError {
			s.log.WarnContext(r.Context(), "database switch: record restore switch", "app", app.ID, "err", err)
		}
		api.WriteProblem(w, problem)
		return
	}
	s.recordKeepReleaseSwitch(r.Context(), acct, app, created)
	writeJSON(w, http.StatusAccepted, s.databaseSwitchView(r.Context(), created))
}

// keepReleaseSwitchFor checks a restore switch and builds it: the live
// release is both where it starts and its target, and it undoes no release.
func (s *server) keepReleaseSwitchFor(r *http.Request, acct state.Account, app state.App, req api.CreateDatabaseSwitchRequest) (state.DatabaseSwitch, *api.Problem) {
	ctx := r.Context()
	name, scope := strings.TrimSpace(req.Name), strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = state.DefaultEnvScope
	}
	point, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.PointInTime))
	switch {
	case strings.TrimSpace(req.DatabaseID) == "":
		return state.DatabaseSwitch{}, api.ErrValidation("database_id is required")
	case name == "" || len(name) > 63:
		return state.DatabaseSwitch{}, api.ErrValidation("name is required: the restored copy's name, 1 to 63 characters")
	case err != nil:
		return state.DatabaseSwitch{}, api.ErrValidation("point_in_time must be an RFC3339 time inside the database's restore range")
	}
	point = point.UTC()
	live, err := s.store.LiveDeploymentForScope(ctx, app.ID, scope)
	if errors.Is(err, state.ErrNotFound) {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable(
			fmt.Sprintf("%s has no live release in the %s scope, so there is nothing to switch.", app.Slug, scope),
			"Restore the database with gregale postgres restore and attach the copy.")
	}
	if err != nil {
		return state.DatabaseSwitch{}, customerCapacityProblem(s.log, "read live deployment", "Database switch temporarily unavailable",
			"Gregale could not read the app's live release.", "Retry the request in a moment.", err)
	}
	if split, ok := s.trafficSplitEarlierRelease(ctx, app.ID, scope); ok {
		return state.DatabaseSwitch{}, api.ErrDatabaseSwitchUnavailable(
			"More than one release serves this scope, so the app cannot be switched as one.",
			fmt.Sprintf("Return all traffic to one release (gregale release traffic rollback %s --to %s), then switch.", app.Slug, split.earlier))
	}
	source, problem := s.keepReleaseSwitchSource(ctx, acct, app, scope, strings.TrimSpace(req.DatabaseID), point)
	if problem != nil {
		return state.DatabaseSwitch{}, problem
	}
	return state.DatabaseSwitch{AccountID: acct.ID, AppID: app.ID, Scope: scope, FromDeploymentID: live.ID,
		TargetDeploymentID: live.ID, UndoneDeploymentIDs: []string{}, SourceDatabaseID: source.ID, TargetName: name,
		RestorePoint: point, RequestedBy: releaseApprovalActorLabel(r, acct), CreatedAt: time.Now().UTC()}, nil
}

// keepReleaseSwitchSource checks the database a restore switch restores: the
// app binds it in the scope, the plan allows restoring it, the copy fits
// inside the plan's database limit, and the point is inside its restore
// range.
func (s *server) keepReleaseSwitchSource(ctx context.Context, acct state.Account, app state.App, scope, databaseID string, point time.Time) (managedpostgres.Database, *api.Problem) {
	source, err := s.managedPostgres.Get(ctx, acct.ID, databaseID)
	if err != nil {
		return managedpostgres.Database{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such managed PostgreSQL database")
	}
	bound, err := s.appBindsDatabase(ctx, acct, app, scope, source.ID)
	if err != nil {
		return managedpostgres.Database{}, customerCapacityProblem(s.log, "list database bindings", "Database switch temporarily unavailable",
			"Gregale could not list the database's bindings.", "Retry the request in a moment.", err)
	}
	if !bound {
		return managedpostgres.Database{}, api.ErrDatabaseSwitchUnavailable(
			fmt.Sprintf("%s has no binding to %s in the %s scope, so there is nothing to move to the copy.", app.Slug, source.Name, scope),
			"Restore the database with gregale postgres restore and attach the copy.")
	}
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok || limits.DatabasesMax == 0 || !managedPostgresPlanAllows(limits, source.Spec) {
		return managedpostgres.Database{}, api.ErrDatabaseSwitchUnavailable("The account's plan does not allow restoring this database.", "Upgrade the plan.")
	}
	if problem := s.databaseSwitchRoom(ctx, acct); problem != nil {
		return managedpostgres.Database{}, problem
	}
	now := time.Now().UTC()
	if source.State != managedpostgres.StateReady {
		return managedpostgres.Database{}, api.ErrDatabaseSwitchUnavailable(
			fmt.Sprintf("%s is %s; only a ready database can be restored.", source.Name, source.State), "")
	}
	// The provider applies the same bounds when the range is unknown here.
	earliest, latest, ok := source.RestoreRange(now)
	if point.After(now) || ok && (point.Before(earliest) || point.After(latest)) {
		detail := fmt.Sprintf("%s is not a past moment %s can be restored to.", point.Format(time.RFC3339), source.Name)
		if ok {
			detail = fmt.Sprintf("%s is outside %s's restore range, %s to %s.", point.Format(time.RFC3339), source.Name,
				earliest.UTC().Format(time.RFC3339), latest.UTC().Format(time.RFC3339))
		}
		return managedpostgres.Database{}, api.ErrDatabaseSwitchUnavailable(detail, "Pick a point inside the range gregale postgres get shows.")
	}
	return source, nil
}

// appBindsDatabase reports whether the app has a live binding to the
// database in the scope; the switch moves exactly those.
func (s *server) appBindsDatabase(ctx context.Context, acct state.Account, app state.App, scope, databaseID string) (bool, error) {
	bindings, err := s.managedPostgresBindings.List(ctx, acct.ID, databaseID)
	if err != nil {
		return false, err
	}
	for _, b := range bindings {
		if b.AppID == app.ID && recoveryScope(b.Scope) == recoveryScope(scope) && b.State != managedpostgres.BindingStateDeleted {
			return true, nil
		}
	}
	return false, nil
}

// databaseSwitchCreateProblem maps a failed switch insert to its answer.
func databaseSwitchCreateProblem(app state.App, err error) *api.Problem {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeDatabaseSwitchInProgress, "Database switch in progress",
			"This app already has a database switch that has not finished.").
			WithHint(fmt.Sprintf("Follow it with gregale rollback switches %s, or cancel it before it starts switching.", app.Slug))
	case errors.Is(err, state.ErrDatabaseSwitchesActive):
		return api.ErrDatabaseSwitchesActive(api.DatabaseSwitchesActivePerAccount)
	}
	return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Database switch temporarily unavailable",
		"Gregale could not record the database switch.").WithHint("Retry the request in a moment; if it continues, contact support.")
}

func (s *server) recordKeepReleaseSwitch(ctx context.Context, acct state.Account, app state.App, sw state.DatabaseSwitch) {
	s.log.Info("database restore switch requested", "app", app.ID, "deployment", sw.TargetDeploymentID,
		"switch", sw.ID, "source_database", sw.SourceDatabaseID, "restore_point", sw.RestorePoint)
	s.audit.Emit(ctx, "app.database_switch_requested", &acct.ID, map[string]any{
		"app_id": app.ID, "switch_id": sw.ID, "from": sw.FromDeploymentID, "to": sw.TargetDeploymentID, "mode": "keep_release",
		"source_database_id": sw.SourceDatabaseID, "restore_point": sw.RestorePoint.Format(time.RFC3339),
	})
	s.appendDatabaseSwitchAudit(ctx, sw, "requested")
}
