// adr: 20261019071733 amendment 4 — a company organization owns the freezes
// of its apps.
package apidserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/freeze"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// listAppReleaseFreezes serves GET /v1/apps/{slug}/freezes: the freezes that
// cover the app, its owner's on all apps and on this one.
func (s *server) listAppReleaseFreezes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.releaseFreezes(); !ok {
		api.WriteProblem(w, api.ErrCapacity("deploy freezes are unavailable"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	now := time.Now().UTC()
	freezes, err := freeze.For(r.Context(), s.store, app, now)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list freezes"))
		return
	}
	writeJSON(w, http.StatusOK, api.ListReleaseFreezesResponse{Freezes: s.freezeViews(r.Context(), freezes, now)})
}

// createAppReleaseFreeze serves POST /v1/apps/{slug}/freezes. The app's
// owner owns the freeze: the company organization of a company app.
func (s *server) createAppReleaseFreeze(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.CreateReleaseFreezeRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if slug := strings.TrimSpace(req.App); slug != "" && slug != app.Slug {
		api.WriteProblem(w, api.ErrValidation("app must be empty or "+app.Slug+": this route freezes "+app.Slug))
		return
	}
	owner, err := freeze.Owner(r.Context(), s.store, app)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the app's organization"))
		return
	}
	if p := s.freezeKindProblem(r, owner, req.Kind); p != nil {
		api.WriteProblem(w, p)
		return
	}
	s.storeReleaseFreeze(w, r, req, owner, app.ID, configActor(r, actorAccount(r, acct)), acct)
}

// liftAppReleaseFreeze serves DELETE /v1/apps/{slug}/freezes/{id}: it lifts
// a freeze on this app. Freezes on all apps are lifted on /v1/freezes or
// /v1/orgs/{slug}/freezes.
func (s *server) liftAppReleaseFreeze(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	owner, err := freeze.Owner(r.Context(), s.store, app)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the app's organization"))
		return
	}
	actor := configActor(r, actorAccount(r, acct))
	f, p := s.ownedReleaseFreeze(r.Context(), owner, r.PathValue("id"))
	if p == nil && f.AppID != app.ID {
		p = api.ErrReleaseFreezeNotFound(r.PathValue("id"))
	}
	if p == nil {
		p = s.freezeLiftProblem(r, owner, f, actor)
	}
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	s.liftOwnedReleaseFreeze(w, r, owner, f.ID, actor, acct)
}

// listOrgReleaseFreezes serves GET /v1/orgs/{slug}/freezes: a company
// organization's freezes, on all of its apps and on single apps.
func (s *server) listOrgReleaseFreezes(w http.ResponseWriter, r *http.Request, _ state.Account) {
	org, ok := s.freezeOrg(w, r, authz.OrgActionReadApp)
	if !ok {
		return
	}
	store, ok := s.releaseFreezes()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("deploy freezes are unavailable"))
		return
	}
	now := time.Now().UTC()
	freezes, err := store.ListReleaseFreezes(r.Context(), state.ReleaseFreezeOwner{OrgID: org.ID}, now)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list freezes"))
		return
	}
	writeJSON(w, http.StatusOK, api.ListReleaseFreezesResponse{Freezes: s.freezeViews(r.Context(), freezes, now)})
}

// createOrgReleaseFreeze serves POST /v1/orgs/{slug}/freezes: a freeze on all
// of a company organization's apps.
func (s *server) createOrgReleaseFreeze(w http.ResponseWriter, r *http.Request, acct state.Account) {
	org, ok := s.freezeOrg(w, r, authz.OrgActionDeployApp)
	if !ok {
		return
	}
	var req api.CreateReleaseFreezeRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if strings.TrimSpace(req.App) != "" {
		api.WriteProblem(w, api.ErrValidation("app must be empty: this route freezes all of the organization's apps; freeze one app with POST /v1/apps/{slug}/freezes"))
		return
	}
	owner := state.ReleaseFreezeOwner{OrgID: org.ID}
	if p := s.freezeKindProblem(r, owner, req.Kind); p != nil {
		api.WriteProblem(w, p)
		return
	}
	s.storeReleaseFreeze(w, r, req, owner, "", configActor(r, acct), acct)
}

// liftOrgReleaseFreeze serves DELETE /v1/orgs/{slug}/freezes/{id}.
func (s *server) liftOrgReleaseFreeze(w http.ResponseWriter, r *http.Request, acct state.Account) {
	org, ok := s.freezeOrg(w, r, authz.OrgActionDeployApp)
	if !ok {
		return
	}
	owner := state.ReleaseFreezeOwner{OrgID: org.ID}
	actor := configActor(r, acct)
	f, p := s.ownedReleaseFreeze(r.Context(), owner, r.PathValue("id"))
	if p == nil {
		p = s.freezeLiftProblem(r, owner, f, actor)
	}
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	s.liftOwnedReleaseFreeze(w, r, owner, f.ID, actor, acct)
}

// freezeOrg authorizes action on the organization the route names and
// returns it. A personal organization's apps keep their account's freezes,
// which /v1/freezes holds.
func (s *server) freezeOrg(w http.ResponseWriter, r *http.Request, action authz.OrgAction) (state.Org, bool) {
	if !s.requireOrgAction(w, r, action) {
		return state.Org{}, false
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return state.Org{}, false
	}
	org, err := s.store.OrgByID(r.Context(), mem.OrgID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the organization"))
		return state.Org{}, false
	}
	if org.Personal {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Personal apps use account freezes",
			"A personal organization's apps are frozen by your account's freezes.").
			WithHint("Use /v1/freezes (CLI: gregale freeze without --org)."))
		return state.Org{}, false
	}
	return org, true
}

// freezeKindProblem lets an organization's owners and admins alone create
// standing policies (weekly windows and after-rollback freezes), as with
// release gates and the approval policy; a member who deploys may create a
// window, since stopping releases is the safe direction. An account's own
// freezes need no role.
func (s *server) freezeKindProblem(r *http.Request, owner state.ReleaseFreezeOwner, kind string) *api.Problem {
	if owner.OrgID == "" || kind == api.ReleaseFreezeKindWindow {
		return nil
	}
	return authz.AuthorizeOrgAction(r.Context(), authz.OrgActionManageAppSecurity, s.audit)
}

// freezeLiftProblem lets an organization's owners and admins lift any of
// its freezes, and a member who deploys lift only a window they created.
func (s *server) freezeLiftProblem(r *http.Request, owner state.ReleaseFreezeOwner, f state.ReleaseFreeze, actor string) *api.Problem {
	if owner.OrgID == "" {
		return nil
	}
	if f.Kind == state.ReleaseFreezeWindow && f.Source == state.ReleaseFreezeSourceManual && f.CreatedBy == actor {
		return nil
	}
	return authz.AuthorizeOrgAction(r.Context(), authz.OrgActionManageAppSecurity, s.audit)
}

// ownedReleaseFreeze finds one of owner's unlifted freezes.
func (s *server) ownedReleaseFreeze(ctx context.Context, owner state.ReleaseFreezeOwner, id string) (state.ReleaseFreeze, *api.Problem) {
	store, ok := s.releaseFreezes()
	if !ok {
		return state.ReleaseFreeze{}, api.ErrCapacity("deploy freezes are unavailable")
	}
	freezes, err := store.ListReleaseFreezes(ctx, owner, time.Now().UTC())
	if err != nil {
		return state.ReleaseFreeze{}, api.ErrCapacity("could not read freezes")
	}
	for _, f := range freezes {
		if f.ID == id {
			return f, nil
		}
	}
	return state.ReleaseFreeze{}, api.ErrReleaseFreezeNotFound(id)
}

// auditReleaseFreeze emits a freeze's audit event and, for a company
// organization's freeze, its activity in the organization's timeline. r is
// nil for a freeze Gregale creates on its own.
func (s *server) auditReleaseFreeze(ctx context.Context, r *http.Request, acct state.Account, kind string, f state.ReleaseFreeze, view api.ReleaseFreeze, data map[string]any) {
	if f.OrgID != "" {
		data["org_id"] = f.OrgID
	}
	if s.audit != nil {
		s.audit.Emit(ctx, kind, &acct.ID, appaccess.AuditData(ctx, data))
	}
	orgID, err := uuid.Parse(f.OrgID)
	if err != nil {
		return
	}
	label := view.App
	if label == "" {
		label = "all apps"
	}
	entry := state.OrgActivity{OrgID: orgID, Kind: kind, ResourceType: "release_freeze", ResourceID: f.ID, ResourceLabel: label,
		SourceType: kind, SourceID: kind + ":" + f.ID, Data: activityData(map[string]any{"kind": f.Kind, "reason": f.Reason})}
	if appID, err := uuid.Parse(f.AppID); err == nil {
		entry.AppID = &appID
	}
	if r == nil {
		entry.ActorType, entry.ActorLabel = state.OrgActivityActorSystem, "Gregale"
	} else {
		entry.ActorType, entry.ActorLabel, entry.ActorAccountID = activityActor(r, actorAccount(r, acct))
	}
	s.recordOrgActivity(ctx, entry)
}

// emitFreezeOverride audits an accepted override and, on a company app, adds
// it to the organization's timeline.
func (s *server) emitFreezeOverride(r *http.Request, acct state.Account, overridden map[string]any) {
	ctx := r.Context()
	if s.audit != nil {
		s.audit.Emit(ctx, "release.freeze_overridden", &acct.ID, appaccess.AuditData(ctx, overridden))
	}
	orgID, err := uuid.Parse(freezePayloadString(overridden["org_id"]))
	if err != nil {
		return
	}
	appID := freezePayloadString(overridden["app"])
	label := appID
	if app, err := s.store.AppByID(ctx, appID); err == nil {
		label = app.Slug
	}
	source := freezePayloadString(overridden["deployment"])
	if source == "" {
		source = uuid.NewString()
	}
	entry := state.OrgActivity{OrgID: orgID, Kind: "release.freeze_overridden", ResourceType: "app", ResourceID: appID, ResourceLabel: label,
		SourceType: "release.freeze_overridden", SourceID: "release.freeze_overridden:" + source,
		Data: activityData(map[string]any{"reason": overridden["reason"], "freezes": overridden["freezes"]})}
	if id, err := uuid.Parse(appID); err == nil {
		entry.AppID = &id
	}
	if id, err := uuid.Parse(freezePayloadString(overridden["deployment"])); err == nil {
		entry.DeploymentID = &id
	}
	entry.ActorType, entry.ActorLabel, entry.ActorAccountID = activityActor(r, actorAccount(r, acct))
	s.recordOrgActivity(ctx, entry)
}

func freezePayloadString(v any) string {
	s, _ := v.(string)
	return s
}
