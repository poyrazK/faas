// adr: 20261019071733 — deploy freezes withhold production releases.
package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/approval"
	"github.com/onebox-faas/faas/pkg/apid/freeze"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/releasemetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// automaticFreezeActor names Gregale as the creator of automatic freezes.
const automaticFreezeActor = "gregale"

func (s *server) releaseFreezes() (state.ReleaseFreezeStore, bool) {
	store, ok := s.store.(state.ReleaseFreezeStore)
	return store, ok
}

// freezeViews renders freezes with their apps' and organizations' slugs.
func (s *server) freezeViews(ctx context.Context, freezes []state.ReleaseFreeze, now time.Time) []api.ReleaseFreeze {
	apps, orgs := map[string]string{}, map[string]string{}
	out := make([]api.ReleaseFreeze, 0, len(freezes))
	for _, f := range freezes {
		slug := ""
		if f.AppID != "" {
			if cached, ok := apps[f.AppID]; ok {
				slug = cached
			} else if app, err := s.store.AppByID(ctx, f.AppID); err == nil {
				slug = app.Slug
				apps[f.AppID] = slug
			}
		}
		view := freeze.View(f, slug, now)
		if f.OrgID != "" {
			if cached, ok := orgs[f.OrgID]; ok {
				view.Org = cached
			} else if org, err := s.store.OrgByID(ctx, f.OrgID); err == nil {
				view.Org = org.Slug
				orgs[f.OrgID] = org.Slug
			}
		}
		out = append(out, view)
	}
	return out
}

// accountFreezeApp loads a personal app that a /v1/freezes request names.
// A company app's freezes belong to its organization (ADR-20261019071733
// amendment 4), so naming one is refused with the routes that hold them.
func (s *server) accountFreezeApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return state.App{}, false
	}
	owner, err := freeze.Owner(r.Context(), s.store, app)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the app's organization"))
		return state.App{}, false
	}
	if owner.OrgID != "" {
		org, err := s.store.OrgByID(r.Context(), owner.OrgID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read the app's organization"))
			return state.App{}, false
		}
		api.WriteProblem(w, api.ErrReleaseFreezeCompanyApp(app.Slug, org.Slug))
		return state.App{}, false
	}
	return app, true
}

// listReleaseFreezes serves GET /v1/freezes[?app=SLUG]: the account's
// freezes, which cover its personal apps.
func (s *server) listReleaseFreezes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.releaseFreezes()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("deploy freezes are unavailable"))
		return
	}
	now := time.Now().UTC()
	freezes, err := store.ListReleaseFreezes(r.Context(), state.ReleaseFreezeOwner{AccountID: acct.ID}, now)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list freezes"))
		return
	}
	if slug := strings.TrimSpace(r.URL.Query().Get("app")); slug != "" {
		app, ok := s.accountFreezeApp(w, r, acct, slug)
		if !ok {
			return
		}
		kept := freezes[:0]
		for _, f := range freezes {
			if freeze.Covers(f, app.ID) {
				kept = append(kept, f)
			}
		}
		freezes = kept
	}
	hidden, err := s.hiddenAccountApps(r, acct)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list freezes"))
		return
	}
	freezes = slices.DeleteFunc(freezes, func(f state.ReleaseFreeze) bool { return hidden[f.AppID] })
	writeJSON(w, http.StatusOK, api.ListReleaseFreezesResponse{Freezes: s.freezeViews(r.Context(), freezes, now)})
}

// hiddenAccountApps names the account's apps the request's principal may no
// longer see (appaccess.HiddenApps): a company's apps once their creator has
// left it. Account-wide lists name no app in their URL, so appaccess.Wrap
// never decides for them.
func (s *server) hiddenAccountApps(r *http.Request, acct state.Account) (map[string]bool, error) {
	apps, err := s.store.ListApps(r.Context(), acct.ID)
	if err != nil {
		return nil, err
	}
	return appaccess.HiddenApps(r, s.store, actorAccount(r, acct), apps)
}

// createReleaseFreeze serves POST /v1/freezes: a freeze on the account's
// personal apps, or on one of them.
func (s *server) createReleaseFreeze(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateReleaseFreezeRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	appID := ""
	if slug := strings.TrimSpace(req.App); slug != "" {
		app, ok := s.accountFreezeApp(w, r, acct, slug)
		if !ok {
			return
		}
		appID = app.ID
	}
	s.storeReleaseFreeze(w, r, req, state.ReleaseFreezeOwner{AccountID: acct.ID}, appID, configActor(r, acct), acct)
}

// storeReleaseFreeze validates req and stores the freeze for owner, then
// audits it and answers 201.
func (s *server) storeReleaseFreeze(w http.ResponseWriter, r *http.Request, req api.CreateReleaseFreezeRequest, owner state.ReleaseFreezeOwner, appID, actor string, acct state.Account) {
	store, ok := s.releaseFreezes()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("deploy freezes are unavailable"))
		return
	}
	now := time.Now().UTC()
	f, prob := freeze.FromRequest(req, owner, appID, actor, now)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	created, err := store.CreateReleaseFreeze(r.Context(), f, api.ReleaseFreezesMax, now)
	if errors.Is(err, state.ErrReleaseFreezeLimit) {
		api.WriteProblem(w, api.ErrReleaseFreezeLimit(api.ReleaseFreezesMax))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create freeze"))
		return
	}
	view := s.freezeViews(r.Context(), []state.ReleaseFreeze{created}, now)[0]
	s.auditReleaseFreeze(r.Context(), r, acct, "release.freeze_created", created, view, map[string]any{
		"freeze": created.ID, "app": view.App, "kind": created.Kind, "reason": created.Reason, "actor": created.CreatedBy,
	})
	writeJSON(w, http.StatusCreated, view)
}

// liftReleaseFreeze serves DELETE /v1/freezes/{id}.
func (s *server) liftReleaseFreeze(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.liftOwnedReleaseFreeze(w, r, state.ReleaseFreezeOwner{AccountID: acct.ID}, r.PathValue("id"), configActor(r, acct), acct)
}

// liftOwnedReleaseFreeze lifts one of owner's freezes, then audits it and
// answers with the lifted freeze.
func (s *server) liftOwnedReleaseFreeze(w http.ResponseWriter, r *http.Request, owner state.ReleaseFreezeOwner, id, actor string, acct state.Account) {
	store, ok := s.releaseFreezes()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("deploy freezes are unavailable"))
		return
	}
	now := time.Now().UTC()
	lifted, err := store.LiftReleaseFreeze(r.Context(), owner, id, actor, now)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrReleaseFreezeNotFound(id))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not lift freeze"))
		return
	}
	view := s.freezeViews(r.Context(), []state.ReleaseFreeze{lifted}, now)[0]
	s.auditReleaseFreeze(r.Context(), r, acct, "release.freeze_lifted", lifted, view, map[string]any{
		"freeze": lifted.ID, "reason": lifted.Reason, "actor": lifted.LiftedBy,
	})
	writeJSON(w, http.StatusOK, view)
}

// getReleaseFreezeStatus serves GET /v1/apps/{slug}/freeze-status.
func (s *server) getReleaseFreezeStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.releaseFreezes(); !ok {
		api.WriteProblem(w, api.ErrCapacity("deploy freezes are unavailable"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	status, err := s.releaseFreezeStatus(r.Context(), app)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read freezes"))
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// releaseFreezeStatus reports the freezes covering app now and the next one
// due to start.
func (s *server) releaseFreezeStatus(ctx context.Context, app state.App) (api.ReleaseFreezeStatus, error) {
	now := time.Now().UTC()
	freezes, err := freeze.For(ctx, s.store, app, now)
	if err != nil {
		return api.ReleaseFreezeStatus{}, err
	}
	status := api.ReleaseFreezeStatus{App: app.Slug, Active: s.freezeViews(ctx, freeze.Active(freezes, app.ID, now), now)}
	status.Frozen = len(status.Active) > 0
	var next *state.ReleaseFreeze
	var nextAt time.Time
	for i, f := range freezes {
		if at, ok := freeze.NextStart(f, now); ok && (next == nil || at.Before(nextAt)) {
			next, nextAt = &freezes[i], at
		}
	}
	if next != nil {
		v := s.freezeViews(ctx, []state.ReleaseFreeze{*next}, now)[0]
		status.Next = &v
	}
	return status, nil
}

// releaseFreezeProblem refuses a production release while a freeze covers
// the app, unless override carries a reason; an override is audited. A
// store that cannot read freezes lets the release through: a freeze is a
// guard rail, not an availability dependency.
func (s *server) releaseFreezeProblem(r *http.Request, acct state.Account, app state.App, scope, override string) *api.Problem {
	p, overridden := s.checkReleaseFreeze(r, acct, app, scope, override)
	if overridden != nil {
		s.emitFreezeOverride(r, acct, overridden)
	}
	return p
}

// checkReleaseFreeze is releaseFreezeProblem without the audit: it returns
// the release.freeze_overridden payload of an accepted override.
func (s *server) checkReleaseFreeze(r *http.Request, acct state.Account, app state.App, scope, override string) (*api.Problem, map[string]any) {
	if p := s.platformReleasePauseProblem(); p != nil {
		return p, nil
	}
	now := time.Now().UTC()
	active, err := freeze.ActiveFor(r.Context(), s.store, app, scope, now)
	if err != nil {
		s.log.Warn("deploy freeze: could not read freezes; releasing", "app", app.ID, "err", err)
		return nil, nil
	}
	if len(active) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(override) == "" {
		s.releaseMetrics.Decision(releasemetrics.GuardFreeze, "refused")
		return api.ErrReleaseFrozen(app.Slug, s.freezeViews(r.Context(), active, now)), nil
	}
	if prob := freeze.ValidateReason("override_freeze", override); prob != nil {
		return prob, nil
	}
	for _, f := range active {
		if prob := s.freezeOverrideProblem(r, acct, f); prob != nil {
			s.releaseMetrics.Decision(releasemetrics.GuardFreeze, "refused")
			return prob, nil
		}
	}
	s.releaseMetrics.Decision(releasemetrics.GuardFreeze, "overridden")
	ids := make([]string, 0, len(active))
	for _, f := range active {
		ids = append(ids, f.ID)
	}
	overridden := map[string]any{"app": app.ID, "freezes": ids, "reason": strings.TrimSpace(override), "actor": configActor(r, actorAccount(r, acct))}
	if orgID := active[0].OrgID; orgID != "" {
		overridden["org_id"] = orgID
	}
	return nil, overridden
}

// freezeOverrideProblem holds overriding a company organization's freeze to
// the right to lift it (ADR-20261019071733 amendment 5): an owner or admin,
// or the member who created a window, signed in or with an admin-scoped key.
// A member's deploy:write key, as CI holds, could otherwise ship past a freeze
// an admin set for the whole company. An account's own freezes keep the open
// override: one person both sets and overrides them.
func (s *server) freezeOverrideProblem(r *http.Request, acct state.Account, f state.ReleaseFreeze) *api.Problem {
	if f.OrgID == "" {
		return nil
	}
	if p, ok := principalFrom(r); ok && p.Key != nil && !httpkit.APIKeyCarriesScope(*p.Key, api.ScopeAdmin) {
		return api.ErrReleaseFreezeOverrideNotAllowed(f.ID,
			"Overriding a company's freeze needs a signed-in person or an admin key; this key can only deploy.")
	}
	owner := state.ReleaseFreezeOwner{OrgID: f.OrgID}
	if prob := s.freezeLiftProblem(r, owner, f, configActor(r, actorAccount(r, acct))); prob != nil {
		return api.ErrReleaseFreezeOverrideNotAllowed(f.ID,
			"Only a member who may lift this freeze can override it: an owner or admin, or the member who created the window.")
	}
	return nil
}

// pendingFreezeOverrideKey carries an accepted deploy override until the
// deployment exists.
type pendingFreezeOverrideKey struct{}

// deferFreezeOverrideAudit holds an accepted override's audit until the
// deployment is created (auditFreezeOverride). Emitted at the check, a deploy
// that then failed validation left a release.freeze_overridden for a release
// that never existed (release review 2026-10-28).
func deferFreezeOverrideAudit(r *http.Request, overridden map[string]any) {
	if overridden != nil {
		*r = *r.WithContext(context.WithValue(r.Context(), pendingFreezeOverrideKey{}, overridden))
	}
}

// auditFreezeOverride emits the deferred override of the deploy that created d.
func (s *server) auditFreezeOverride(r *http.Request, acct state.Account, d state.Deployment) {
	overridden, ok := r.Context().Value(pendingFreezeOverrideKey{}).(map[string]any)
	if !ok {
		return
	}
	overridden["deployment"] = d.ID
	s.emitFreezeOverride(r, acct, overridden)
}

// retryReleaseProblem holds a retry to the guards a new production release
// meets (ADR-20261019071733 amendment 1): a retry creates a release from the
// failed one's inputs, so a freeze refuses it, and an app that requires
// approvals needs the retry to keep a canary to wait in. A retry carries no
// override; a deploy with override_freeze releases during a freeze.
func (s *server) retryReleaseProblem(ctx context.Context, app state.App, dep state.Deployment) *api.Problem {
	now := time.Now().UTC()
	active, err := freeze.ActiveFor(ctx, s.store, app, dep.Scope, now)
	if err != nil {
		s.log.Warn("deploy freeze: could not read freezes; retrying", "app", app.ID, "err", err)
	} else if len(active) > 0 {
		s.releaseMetrics.Decision(releasemetrics.GuardFreeze, "refused")
		return api.ErrReleaseFrozen(app.Slug, s.freezeViews(ctx, active, now)).
			WithHint("A retry cannot override a freeze. Wait for it to end, lift it with `gregale freeze lift`, or deploy again with --override-freeze REASON; the override is audited.")
	}
	if dep.CanaryTotalSteps > 0 || (dep.TrafficPercentExplicit && dep.TrafficPercent == 0) {
		return nil
	}
	required, err := approval.CanaryRequired(ctx, s.store, app, dep.Scope, "")
	if err != nil {
		logCustomerFailure(s.log, "read release approval policy", err)
		return api.ErrInternal("Gregale could not read this app's release approval policy.").WithHint("Retry in a moment.")
	}
	if required {
		s.releaseMetrics.Decision(releasemetrics.GuardApproval, "canary_required")
		return api.ErrReleaseApprovalCanaryRequired(app.Slug)
	}
	return nil
}

// darkReleaseFreezeProblem refuses traffic for a release deployed without
// traffic before the active freeze began (ADR-20261019071733 amendment 3). A
// freeze refuses creating production releases, but a release parked dark the
// day before could ship during it through `traffic`, promote or a rollback to
// it, with no override and no audit (release review 2026-10-28). A release
// deployed during the freeze passed the freeze check, or its audited
// override, when it was created; one that completed a rollout served before
// and is a rollback target, which freezes never refuse.
func (s *server) darkReleaseFreezeProblem(ctx context.Context, app state.App, d state.Deployment) *api.Problem {
	if d.Status != state.DeployLive || d.TrafficPercent != 0 || !d.TrafficPercentExplicit || d.RolloutCompletedAt != nil {
		return nil
	}
	now := time.Now().UTC()
	active, err := freeze.ActiveFor(ctx, s.store, app, d.Scope, now)
	if err != nil {
		s.log.Warn("deploy freeze: could not read freezes; releasing", "app", app.ID, "err", err)
		return nil
	}
	var predate []state.ReleaseFreeze
	for _, f := range active {
		if start, ok := freeze.CurrentStart(f, now); ok && d.CreatedAt.Before(start) {
			predate = append(predate, f)
		}
	}
	if len(predate) == 0 {
		return nil
	}
	s.releaseMetrics.Decision(releasemetrics.GuardFreeze, "refused")
	return api.ErrReleaseFrozen(app.Slug, s.freezeViews(ctx, predate, now)).
		WithHint("This release was deployed without traffic before the freeze began and has not served. Wait for the freeze to end, lift it with `gregale freeze lift`, or deploy it again with --override-freeze REASON; the override is audited.")
}

// promotionFreezeProblem applies releaseFreezeProblem to every app a new
// promotion into production would release, in slug order.
func (s *server) promotionFreezeProblem(r *http.Request, acct state.Account, plan projectEnvironmentPromotionPlan, toEnvironment, override string) *api.Problem {
	apps := make([]state.App, 0, len(plan.Apps))
	for _, app := range plan.Apps {
		apps = append(apps, app)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Slug < apps[j].Slug })
	for _, app := range apps {
		if p := s.releaseFreezeProblem(r, acct, app, toEnvironment, override); p != nil {
			return p
		}
	}
	return nil
}

// automaticRollbackCause names what rolled d back, for an automatic
// freeze's reason.
func automaticRollbackCause(trigger string, d state.Deployment) string {
	ref := d.ID
	if d.Revision > 0 {
		ref = fmt.Sprintf("v%d", d.Revision)
	}
	switch trigger {
	case state.RollbackTriggerPostRelease:
		return "the post-release watch rolled back " + ref
	case state.RollbackTriggerAuto5xx:
		return "the first-wake 5xx guard rolled back " + ref
	case state.RollbackTriggerAlert:
		return "an alert rule rolled back " + ref
	case "circuit_breaker":
		return "the rollout circuit breaker aborted " + ref
	}
	return trigger + " rolled back " + ref
}

// freezeAfterAutomaticRollback turns an after-rollback policy covering app
// into a window freeze once Gregale has rolled rolledBackID back on its own.
// The freeze belongs to the policy's owner: the company organization of a
// company app. Failures are logged: the rollback already happened and must
// not fail.
func (s *server) freezeAfterAutomaticRollback(ctx context.Context, app state.App, rolledBackID, cause string) {
	store, ok := s.releaseFreezes()
	if !ok || rolledBackID == "" {
		return
	}
	now := time.Now().UTC()
	freezes, err := freeze.For(ctx, s.store, app, now)
	if err != nil {
		s.log.Warn("deploy freeze: could not read after-rollback policies", "app", app.ID, "err", err)
		return
	}
	policy, ok := freeze.AfterRollbackPolicy(freezes, app.ID)
	if !ok {
		return
	}
	end := now.Add(time.Duration(policy.AfterRollbackMinutes) * time.Minute)
	reason := "Automatic rollback: " + cause
	if len([]rune(reason)) > api.ReleaseFreezeReasonMax {
		reason = string([]rune(reason)[:api.ReleaseFreezeReasonMax])
	}
	f := state.ReleaseFreeze{ID: uuid.NewString(), AccountID: policy.AccountID, OrgID: policy.OrgID, AppID: app.ID,
		Kind: state.ReleaseFreezeWindow, Source: state.ReleaseFreezeSourceAutomaticRollback,
		StartsAt: &now, EndsAt: &end, Reason: reason, SourceDeploymentID: rolledBackID, CreatedBy: automaticFreezeActor, CreatedAt: now}
	created, err := store.CreateRollbackReleaseFreeze(ctx, f)
	if err != nil {
		s.log.Warn("deploy freeze: could not freeze after automatic rollback", "app", app.ID, "deployment", rolledBackID, "err", err)
		return
	}
	if created {
		view := freeze.View(f, app.Slug, now)
		s.auditReleaseFreeze(ctx, nil, state.Account{ID: app.AccountID}, "release.freeze_created", f, view, map[string]any{
			"app": app.ID, "kind": state.ReleaseFreezeWindow, "source": state.ReleaseFreezeSourceAutomaticRollback,
			"deployment": rolledBackID, "policy": policy.ID, "ends_at": end.Format(time.RFC3339), "actor": automaticFreezeActor,
		})
	}
}

// platformReleasePauseProblem refuses any release while the break-glass
// release pause is on (ADR 20261022140000). Unlike a freeze it covers every
// scope and nothing overrides it; rollbacks do not pass through here.
func (s *server) platformReleasePauseProblem() *api.Problem {
	if s.platformReleasePaused() {
		return api.ErrPlatformReleasePaused()
	}
	return nil
}

// platformReleasePaused reports whether the break-glass release pause is on,
// for paths that hold work rather than answer a request.
func (s *server) platformReleasePaused() bool {
	return s.runtimeBool(runtimeConfigPlatformReleasePause, false)
}
