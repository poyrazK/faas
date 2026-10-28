package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/approval"
	"github.com/onebox-faas/faas/pkg/apid/freeze"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/apid/schedule"
	"github.com/onebox-faas/faas/pkg/state"
)

// Scheduled releases (ADR-20261021152201): a production release built ahead
// of time waits live without traffic, and apid starts its rollout when its
// time comes.

func (s *server) scheduledReleases() (state.ScheduledReleaseStore, bool) {
	store, ok := s.store.(state.ScheduledReleaseStore)
	return store, ok
}

func (s *server) scheduledReleaseStore(w http.ResponseWriter) (state.ScheduledReleaseStore, bool) {
	store, ok := s.scheduledReleases()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("scheduled releases are unavailable"))
	}
	return store, ok
}

// releaseSchedule is a deploy request's scheduled start, prepared before the
// deployment exists.
type releaseSchedule struct {
	at          time.Time
	notifyIDs   []string
	override    string
	overrideIDs []string
	canary      *api.CanaryPresetSpec
}

// releaseFreezeOrSchedule checks a deploy against deploy freezes. An
// immediate release is checked now. A release with release_at is checked at
// that time, and the schedule to record once the deployment exists is
// returned.
func (s *server) releaseFreezeOrSchedule(r *http.Request, acct state.Account, app state.App, req *api.CreateDeploymentRequest) (*releaseSchedule, *api.Problem) {
	if p := s.platformReleasePauseProblem(); p != nil {
		return nil, p
	}
	if strings.TrimSpace(req.ReleaseAt) == "" {
		if len(req.ReleaseNotify) > 0 {
			return nil, api.ErrValidation("release_notify needs release_at")
		}
		return nil, s.releaseFreezeProblem(r, acct, app, req.Scope, req.OverrideFreeze)
	}
	at, p := api.ParseReleaseAt("release_at", req.ReleaseAt, time.Now().UTC())
	if p != nil {
		return nil, p
	}
	if p := scheduledReleaseEligible(app, req.Scope); p != nil {
		return nil, p
	}
	if req.TrafficPercent != nil {
		return nil, api.ErrValidation("release_at parks the release without traffic until its time; drop traffic_percent")
	}
	if p := api.ValidateScheduleNotify("release_notify", req.ReleaseNotify); p != nil {
		return nil, p
	}
	ids, p := s.releaseApprovalChannelIDs(r.Context(), acct.ID, req.ReleaseNotify)
	if p != nil {
		return nil, p
	}
	override, overrideIDs, p := s.scheduledFreezeOverride(r.Context(), acct, app, req.Scope, req.OverrideFreeze, at)
	if p != nil {
		return nil, p
	}
	return &releaseSchedule{at: at, notifyIDs: ids, override: override, overrideIDs: overrideIDs}, nil
}

// scheduledReleaseEligible refuses releases that cannot be scheduled.
func scheduledReleaseEligible(app state.App, scope string) *api.Problem {
	switch {
	case app.PreviewOfSlug != "":
		return api.ErrValidation("preview and developer environments release immediately; release_at is for production releases")
	case !approval.RequestMode(app):
		return api.ErrValidation("services, workers and jobs roll out behind readiness checks; release_at is for request apps")
	case scope != "" && scope != api.DefaultEnvScope:
		return api.ErrValidation("release_at schedules production releases; drop the scope or environment")
	}
	return nil
}

// scheduledFreezeOverride evaluates deploy freezes at a release's time. A
// freeze active then refuses the schedule unless override gives a reason;
// the override then covers exactly those freezes.
func (s *server) scheduledFreezeOverride(ctx context.Context, acct state.Account, app state.App, scope, override string, at time.Time) (string, []string, *api.Problem) {
	active, err := freeze.ActiveFor(ctx, s.store, acct.ID, app.ID, scope, at)
	if err != nil {
		logCustomerFailure(s.log, "read deploy freezes", err)
		return "", nil, api.ErrInternal("Gregale could not read your deploy freezes.").WithHint("Retry in a moment.")
	}
	if len(active) == 0 {
		return "", nil, nil
	}
	override = strings.TrimSpace(override)
	if override == "" {
		p := api.ErrReleaseFrozen(app.Slug, s.freezeViews(ctx, active, at))
		p.Detail = "At " + at.Format(time.RFC3339) + ": " + p.Detail
		return "", nil, p
	}
	if p := freeze.ValidateReason("override_freeze", override); p != nil {
		return "", nil, p
	}
	ids := make([]string, 0, len(active))
	for _, f := range active {
		ids = append(ids, f.ID)
	}
	return override, ids, nil
}

// park keeps the rollout the deploy chose for the schedule and sends the
// release out without traffic.
func (sched *releaseSchedule) park(req *api.CreateDeploymentRequest) {
	if sched == nil {
		return
	}
	sched.canary = req.Canary
	req.Canary = nil
	zero := 0
	req.TrafficPercent = &zero
}

// recordReleaseSchedule stores the schedule of a release that was just
// created parked.
func (s *server) recordReleaseSchedule(r *http.Request, acct state.Account, app state.App, d state.Deployment, sched *releaseSchedule) *api.Problem {
	if sched == nil {
		return nil
	}
	store, ok := s.scheduledReleases()
	if !ok {
		return api.ErrCapacity("scheduled releases are unavailable")
	}
	created, replaced, err := store.ScheduleRelease(r.Context(), state.ScheduledRelease{
		AccountID: app.AccountID, AppID: app.ID, DeploymentID: d.ID, ReleaseAt: sched.at,
		Canary: schedule.Encode(sched.canary), NotifyChannelIDs: sched.notifyIDs, OverrideFreeze: sched.override,
		OverrideFreezeIDs: sched.overrideIDs, ScheduledBy: releaseApprovalActorLabel(r, acct),
	}, schedule.CodeReplaced, "a newer schedule replaced it", time.Now())
	if err != nil {
		logCustomerFailure(s.log, "record scheduled release", err)
		return api.ErrInternal(fmt.Sprintf("Release %s was built without traffic, but its schedule was not recorded.", d.ID)).
			WithHint(fmt.Sprintf("Schedule it with `gregale scheduled-releases set %s --app %s --at %s`.", d.ID, app.Slug, sched.at.Format(time.RFC3339)))
	}
	s.auditScheduleRecorded(r, acct, app, created, replaced)
	return nil
}

func (s *server) auditScheduleRecorded(r *http.Request, acct state.Account, app state.App, created state.ScheduledRelease, replaced []state.ScheduledRelease) {
	if s.audit == nil {
		return
	}
	actor := configActor(r, acct)
	for _, old := range replaced {
		s.audit.Emit(r.Context(), "release.schedule_cancelled", &acct.ID, map[string]any{
			"app": app.ID, "deployment": old.DeploymentID, "schedule": old.ID, "code": old.OutcomeCode, "actor": actor,
		})
	}
	s.audit.Emit(r.Context(), "release.scheduled", &acct.ID, map[string]any{
		"app": app.ID, "deployment": created.DeploymentID, "schedule": created.ID,
		"release_at": created.ReleaseAt.Format(time.RFC3339), "freezes_overridden": created.OverrideFreezeIDs, "actor": actor,
	})
}

// deploymentScheduleView is the schedule shown on a parked release.
func (s *server) deploymentScheduleView(parent context.Context, d state.Deployment, app state.App) *api.ScheduledRelease {
	store, ok := s.scheduledReleases()
	if !ok || !d.TrafficPercentExplicit || d.TrafficPercent != 0 || d.Status.IsTerminal() {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	sr, err := store.LatestScheduledRelease(ctx, d.ID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			s.log.Warn("load scheduled release", "deployment_id", d.ID, "err", err)
		}
		return nil
	}
	view := schedule.View(sr, app.Slug, s.releaseApprovalChannelNames(ctx, app.AccountID, sr.NotifyChannelIDs))
	return &view
}

// scheduledTrafficProblem refuses to give a release traffic by hand
// while it waits for its scheduled start.
func (s *server) scheduledTrafficProblem(ctx context.Context, d state.Deployment) *api.Problem {
	store, ok := s.scheduledReleases()
	if !ok || !d.TrafficPercentExplicit || d.TrafficPercent != 0 {
		return nil
	}
	sr, err := store.LatestScheduledRelease(ctx, d.ID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		logCustomerFailure(s.log, "read scheduled release", err)
		return api.ErrInternal("Gregale could not read this release's schedule.").WithHint("Retry in a moment.")
	}
	if sr.State != state.ScheduledReleasePending {
		return nil
	}
	return api.ErrReleaseScheduled(schedule.Ref(d), sr.ReleaseAt.UTC().Format(time.RFC3339))
}

// getDeploymentSchedule serves GET /v1/deployments/{id}/schedule.
func (s *server) getDeploymentSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.scheduledReleaseStore(w)
	if !ok {
		return
	}
	sr, err := store.LatestScheduledRelease(r.Context(), d.ID)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrScheduledReleaseNotFound(schedule.Ref(d)+" has no schedule."))
		return
	}
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "read scheduled release",
			"Gregale could not read this release's schedule.", "Retry in a moment.", err)
		return
	}
	writeJSON(w, http.StatusOK, schedule.View(sr, app.Slug, s.releaseApprovalChannelNames(r.Context(), app.AccountID, sr.NotifyChannelIDs)))
}

// putDeploymentSchedule serves PUT /v1/deployments/{id}/schedule: schedule,
// or move, the start of a release that waits without traffic.
func (s *server) putDeploymentSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.scheduledReleaseStore(w)
	if !ok {
		return
	}
	var req api.ScheduleReleaseRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	sr, p := s.prepareDeploymentSchedule(r, acct, app, d, store, req)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	created, replaced, err := store.ScheduleRelease(r.Context(), sr, schedule.CodeReplaced, "a newer schedule replaced it", time.Now())
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "record scheduled release",
			"Gregale could not record this schedule.", "Retry in a moment.", err)
		return
	}
	s.auditScheduleRecorded(r, acct, app, created, replaced)
	writeJSON(w, http.StatusOK, schedule.View(created, app.Slug, s.releaseApprovalChannelNames(r.Context(), app.AccountID, created.NotifyChannelIDs)))
}

// prepareDeploymentSchedule validates a schedule for an existing release.
func (s *server) prepareDeploymentSchedule(r *http.Request, acct state.Account, app state.App, d state.Deployment, store state.ScheduledReleaseStore, req api.ScheduleReleaseRequest) (state.ScheduledRelease, *api.Problem) {
	at, p := api.ParseReleaseAt("release_at", req.ReleaseAt, time.Now().UTC())
	if p != nil {
		return state.ScheduledRelease{}, p
	}
	if p := scheduledReleaseEligible(app, d.Scope); p != nil {
		return state.ScheduledRelease{}, p
	}
	if why := notSchedulable(d); why != "" {
		return state.ScheduledRelease{}, api.ErrReleaseNotParked(schedule.Ref(d), why)
	}
	if p := api.ValidateScheduleNotify("notify_channels", req.NotifyChannels); p != nil {
		return state.ScheduledRelease{}, p
	}
	ids, p := s.releaseApprovalChannelIDs(r.Context(), acct.ID, req.NotifyChannels)
	if p != nil {
		return state.ScheduledRelease{}, p
	}
	spec, p := s.scheduleCanary(r.Context(), acct, app, d, store, req.Canary)
	if p != nil {
		return state.ScheduledRelease{}, p
	}
	override, overrideIDs, p := s.scheduledFreezeOverride(r.Context(), acct, app, d.Scope, req.OverrideFreeze, at)
	if p != nil {
		return state.ScheduledRelease{}, p
	}
	return state.ScheduledRelease{
		AccountID: app.AccountID, AppID: app.ID, DeploymentID: d.ID, ReleaseAt: at, Canary: schedule.Encode(spec),
		NotifyChannelIDs: ids, OverrideFreeze: override, OverrideFreezeIDs: overrideIDs,
		ScheduledBy: releaseApprovalActorLabel(r, acct),
	}, nil
}

// notSchedulable says why d cannot be scheduled, or "" when it can: it is
// live without traffic, or still building to go live without traffic.
func notSchedulable(d state.Deployment) string {
	switch {
	case d.Status.IsTerminal():
		return "is " + string(d.Status)
	case d.CanaryTotalSteps > 0:
		return "already rolls out through a canary"
	case d.TrafficPercent > 0:
		return fmt.Sprintf("takes %d%% of traffic", d.TrafficPercent)
	case d.Status != state.DeployLive && !d.TrafficPercentExplicit:
		return "will take traffic when its build finishes"
	}
	return ""
}

// scheduleCanary picks the canary a schedule starts with: the request's, the
// release's pending schedule's, or what the default release policy gives a
// deploy now. An app that requires approvals needs one.
func (s *server) scheduleCanary(ctx context.Context, acct state.Account, app state.App, d state.Deployment, store state.ScheduledReleaseStore, requested *api.CanaryPresetSpec) (*api.CanaryPresetSpec, *api.Problem) {
	spec := requested
	if spec != nil {
		if p := validateDeploymentTrafficOptions(&api.CreateDeploymentRequest{Canary: spec}, acct.Plan); p != nil {
			return nil, p
		}
	} else if pending, err := store.LatestScheduledRelease(ctx, d.ID); err == nil && pending.State == state.ScheduledReleasePending {
		spec, _ = schedule.Spec(pending.Canary)
	} else {
		defaults := &api.CreateDeploymentRequest{Scope: d.Scope}
		release.ApplyDefault(ctx, s.store, s.log, app, acct.Plan, defaults)
		spec = defaults.Canary
	}
	if _, p := schedule.Ladder(spec); p != nil {
		return nil, p
	}
	if ladder, _ := schedule.Ladder(spec); ladder.TotalSteps == 0 {
		required, err := approval.CanaryRequired(ctx, s.store, app, d.Scope, "")
		if err != nil {
			logCustomerFailure(s.log, "read release approval policy", err)
			return nil, api.ErrInternal("Gregale could not read this app's release approval policy.").WithHint("Retry in a moment.")
		}
		if required {
			return nil, api.ErrReleaseApprovalCanaryRequired(app.Slug)
		}
	}
	return spec, nil
}

// deleteDeploymentSchedule serves DELETE /v1/deployments/{id}/schedule. The
// release stays live without traffic.
func (s *server) deleteDeploymentSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.scheduledReleaseStore(w)
	if !ok {
		return
	}
	notFound := api.ErrScheduledReleaseNotFound(schedule.Ref(d) + " has no pending schedule.")
	sr, err := store.LatestScheduledRelease(r.Context(), d.ID)
	if errors.Is(err, state.ErrNotFound) || err == nil && sr.State != state.ScheduledReleasePending {
		api.WriteProblem(w, notFound)
		return
	}
	actor := configActor(r, acct)
	if err == nil {
		sr, err = store.FinishScheduledRelease(r.Context(), sr.ID, state.ScheduledReleaseCancelled, schedule.CodeCancelled, "cancelled by "+actor, time.Now())
	}
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, notFound)
		return
	}
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "cancel scheduled release",
			"Gregale could not cancel this schedule.", "Retry in a moment.", err)
		return
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "release.schedule_cancelled", &acct.ID, map[string]any{
			"app": app.ID, "deployment": d.ID, "schedule": sr.ID, "code": schedule.CodeCancelled, "actor": actor,
		})
	}
	writeJSON(w, http.StatusOK, schedule.View(sr, app.Slug, s.releaseApprovalChannelNames(r.Context(), app.AccountID, sr.NotifyChannelIDs)))
}

// listScheduledReleases serves GET /v1/scheduled-releases[?app=SLUG].
func (s *server) listScheduledReleases(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.scheduledReleaseStore(w)
	if !ok {
		return
	}
	appID := ""
	if slug := strings.TrimSpace(r.URL.Query().Get("app")); slug != "" {
		app, ok := s.loadApp(w, r, acct, slug)
		if !ok {
			return
		}
		appID = app.ID
	}
	rows, err := store.ListScheduledReleases(r.Context(), acct.ID, appID, api.ScheduledReleaseListMax)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "list scheduled releases",
			"Gregale could not list your scheduled releases.", "Retry in a moment.", err)
		return
	}
	hidden, err := s.hiddenAccountApps(r, acct)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "list scheduled releases",
			"Gregale could not list your scheduled releases.", "Retry in a moment.", err)
		return
	}
	rows = slices.DeleteFunc(rows, func(row state.ScheduledRelease) bool { return hidden[row.AppID] })
	writeJSON(w, http.StatusOK, api.ScheduledReleaseListResponse{Items: s.scheduleViews(r.Context(), acct.ID, rows)})
}

// scheduleViews renders schedules with their apps' slugs and channel names.
func (s *server) scheduleViews(ctx context.Context, accountID string, rows []state.ScheduledRelease) []api.ScheduledRelease {
	channelNames := map[string]string{}
	if store, ok := s.store.(state.NotificationChannelStore); ok {
		if channels, err := store.ListNotificationChannels(ctx, accountID); err == nil {
			for _, c := range channels {
				channelNames[c.ID] = c.Name
			}
		}
	}
	slugs := map[string]string{}
	items := make([]api.ScheduledRelease, 0, len(rows))
	for _, sr := range rows {
		slug, seen := slugs[sr.AppID]
		if !seen {
			if app, err := s.store.AppByID(ctx, sr.AppID); err == nil {
				slug = app.Slug
			}
			slugs[sr.AppID] = slug
		}
		names := []string{}
		for _, id := range sr.NotifyChannelIDs {
			if name, ok := channelNames[id]; ok {
				names = append(names, name)
			}
		}
		items = append(items, schedule.View(sr, slug, names))
	}
	return items
}
