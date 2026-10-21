// adr: 20261019071733 — deploy freezes withhold production releases.
package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/freeze"
	"github.com/onebox-faas/faas/pkg/state"
)

// automaticFreezeActor names Gregale as the creator of automatic freezes.
const automaticFreezeActor = "gregale"

func (s *server) releaseFreezes() (state.ReleaseFreezeStore, bool) {
	store, ok := s.store.(state.ReleaseFreezeStore)
	return store, ok
}

// freezeViews renders freezes with their apps' slugs.
func (s *server) freezeViews(ctx context.Context, freezes []state.ReleaseFreeze, now time.Time) []api.ReleaseFreeze {
	slugs := map[string]string{}
	out := make([]api.ReleaseFreeze, 0, len(freezes))
	for _, f := range freezes {
		slug := ""
		if f.AppID != "" {
			if cached, ok := slugs[f.AppID]; ok {
				slug = cached
			} else if app, err := s.store.AppByID(ctx, f.AppID); err == nil {
				slug = app.Slug
				slugs[f.AppID] = slug
			}
		}
		out = append(out, freeze.View(f, slug, now))
	}
	return out
}

// listReleaseFreezes serves GET /v1/freezes[?app=SLUG].
func (s *server) listReleaseFreezes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.releaseFreezes()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("deploy freezes are unavailable"))
		return
	}
	now := time.Now().UTC()
	freezes, err := store.ListReleaseFreezes(r.Context(), acct.ID, now)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list freezes"))
		return
	}
	if slug := strings.TrimSpace(r.URL.Query().Get("app")); slug != "" {
		app, ok := s.loadApp(w, r, acct, slug)
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
	writeJSON(w, http.StatusOK, api.ListReleaseFreezesResponse{Freezes: s.freezeViews(r.Context(), freezes, now)})
}

// createReleaseFreeze serves POST /v1/freezes.
func (s *server) createReleaseFreeze(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.releaseFreezes()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("deploy freezes are unavailable"))
		return
	}
	var req api.CreateReleaseFreezeRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	appID := ""
	if slug := strings.TrimSpace(req.App); slug != "" {
		app, ok := s.loadApp(w, r, acct, slug)
		if !ok {
			return
		}
		appID = app.ID
	}
	now := time.Now().UTC()
	f, prob := freeze.FromRequest(req, acct.ID, appID, configActor(r, acct), now)
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
	s.audit.Emit(r.Context(), "release.freeze_created", &acct.ID, map[string]any{
		"freeze": created.ID, "app": view.App, "kind": created.Kind, "reason": created.Reason, "actor": created.CreatedBy,
	})
	writeJSON(w, http.StatusCreated, view)
}

// liftReleaseFreeze serves DELETE /v1/freezes/{id}.
func (s *server) liftReleaseFreeze(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.releaseFreezes()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("deploy freezes are unavailable"))
		return
	}
	id := r.PathValue("id")
	now := time.Now().UTC()
	lifted, err := store.LiftReleaseFreeze(r.Context(), acct.ID, id, configActor(r, acct), now)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrReleaseFreezeNotFound(id))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not lift freeze"))
		return
	}
	s.audit.Emit(r.Context(), "release.freeze_lifted", &acct.ID, map[string]any{
		"freeze": lifted.ID, "reason": lifted.Reason, "actor": lifted.LiftedBy,
	})
	writeJSON(w, http.StatusOK, s.freezeViews(r.Context(), []state.ReleaseFreeze{lifted}, now)[0])
}

// getReleaseFreezeStatus serves GET /v1/apps/{slug}/freeze-status.
func (s *server) getReleaseFreezeStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.releaseFreezes()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("deploy freezes are unavailable"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	now := time.Now().UTC()
	freezes, err := store.ListReleaseFreezes(r.Context(), acct.ID, now)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read freezes"))
		return
	}
	status := api.ReleaseFreezeStatus{App: app.Slug, Active: s.freezeViews(r.Context(), freeze.Active(freezes, app.ID, now), now)}
	status.Frozen = len(status.Active) > 0
	var next *state.ReleaseFreeze
	var nextAt time.Time
	for i, f := range freezes {
		if !freeze.Covers(f, app.ID) {
			continue
		}
		if at, ok := freeze.NextStart(f, now); ok && (next == nil || at.Before(nextAt)) {
			next, nextAt = &freezes[i], at
		}
	}
	if next != nil {
		v := s.freezeViews(r.Context(), []state.ReleaseFreeze{*next}, now)[0]
		status.Next = &v
	}
	writeJSON(w, http.StatusOK, status)
}

// releaseFreezeProblem refuses a production release while a freeze covers
// the app, unless override carries a reason; an override is audited. A
// store that cannot read freezes lets the release through: a freeze is a
// guard rail, not an availability dependency.
func (s *server) releaseFreezeProblem(r *http.Request, acct state.Account, app state.App, scope, override string) *api.Problem {
	now := time.Now().UTC()
	active, err := freeze.ActiveFor(r.Context(), s.store, acct.ID, app.ID, scope, now)
	if err != nil {
		s.log.Warn("deploy freeze: could not read freezes; releasing", "app", app.ID, "err", err)
		return nil
	}
	if len(active) == 0 {
		return nil
	}
	if strings.TrimSpace(override) == "" {
		return api.ErrReleaseFrozen(app.Slug, s.freezeViews(r.Context(), active, now))
	}
	if prob := freeze.ValidateReason("override_freeze", override); prob != nil {
		return prob
	}
	ids := make([]string, 0, len(active))
	for _, f := range active {
		ids = append(ids, f.ID)
	}
	s.audit.Emit(r.Context(), "release.freeze_overridden", &acct.ID, map[string]any{
		"app": app.ID, "freezes": ids, "reason": strings.TrimSpace(override), "actor": configActor(r, acct),
	})
	return nil
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
// Failures are logged: the rollback already happened and must not fail.
func (s *server) freezeAfterAutomaticRollback(ctx context.Context, accountID string, app state.App, rolledBackID, cause string) {
	store, ok := s.releaseFreezes()
	if !ok || rolledBackID == "" {
		return
	}
	now := time.Now().UTC()
	freezes, err := store.ListReleaseFreezes(ctx, accountID, now)
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
	created, err := store.CreateRollbackReleaseFreeze(ctx, state.ReleaseFreeze{AccountID: accountID, AppID: app.ID,
		StartsAt: &now, EndsAt: &end, Reason: reason, SourceDeploymentID: rolledBackID, CreatedBy: automaticFreezeActor})
	if err != nil {
		s.log.Warn("deploy freeze: could not freeze after automatic rollback", "app", app.ID, "deployment", rolledBackID, "err", err)
		return
	}
	if created {
		s.audit.Emit(ctx, "release.freeze_created", &accountID, map[string]any{
			"app": app.ID, "kind": state.ReleaseFreezeWindow, "source": state.ReleaseFreezeSourceAutomaticRollback,
			"deployment": rolledBackID, "policy": policy.ID, "ends_at": end.Format(time.RFC3339), "actor": automaticFreezeActor,
		})
	}
}
