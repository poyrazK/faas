package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/approval"
	"github.com/onebox-faas/faas/pkg/apid/freeze"
	"github.com/onebox-faas/faas/pkg/apid/schedule"
	"github.com/onebox-faas/faas/pkg/apid/trafficguard"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/releasemetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// runScheduledReleaseWorker starts due scheduled releases
// (ADR-20261021152201). Concurrent apid processes are safe: a start runs in
// one transaction that rechecks the schedule.
func (s *server) runScheduledReleaseWorker(ctx context.Context) {
	if _, ok := s.scheduledReleases(); !ok {
		return
	}
	ticker := time.NewTicker(api.ScheduledReleaseCheckInterval)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		s.startDueScheduledReleases(bounded, time.Now().UTC())
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) startDueScheduledReleases(ctx context.Context, now time.Time) {
	store, ok := s.scheduledReleases()
	if !ok {
		return
	}
	// ADR 20261022140000: a break-glass pause holds due schedules; they are
	// decided on the first pass after it is lifted.
	if s.platformReleasePauseProblem() != nil {
		s.releaseMetrics.Pass(releasemetrics.WorkerScheduledReleases, now)
		return
	}
	due, err := store.DueScheduledReleases(ctx, now, api.ScheduledReleaseBatch)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("scheduled releases: list due", "err", err)
			s.releaseMetrics.Error(releasemetrics.WorkerScheduledReleases)
		}
		return
	}
	s.releaseMetrics.Pass(releasemetrics.WorkerScheduledReleases, now)
	for _, sr := range due {
		if ctx.Err() != nil {
			return
		}
		if err := s.runScheduledRelease(ctx, store, sr, now); err != nil {
			s.log.Warn("scheduled releases: start", "schedule", sr.ID, "deployment", sr.DeploymentID, "err", err)
			s.releaseMetrics.Error(releasemetrics.WorkerScheduledReleases)
		}
	}
}

// runScheduledRelease decides one due schedule and acts on it.
func (s *server) runScheduledRelease(ctx context.Context, store state.ScheduledReleaseStore, sr state.ScheduledRelease, now time.Time) error {
	app, err := s.store.AppByID(ctx, sr.AppID)
	if errors.Is(err, state.ErrNotFound) || err == nil && app.Status == state.AppDeleted {
		_, err := store.FinishScheduledRelease(ctx, sr.ID, state.ScheduledReleaseCancelled, schedule.CodeAppDeleted, "the app was deleted", now)
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return err
	}
	if err != nil {
		return fmt.Errorf("read app: %w", err)
	}
	d, err := s.store.DeploymentByID(ctx, sr.DeploymentID)
	if err != nil {
		return fmt.Errorf("read release: %w", err)
	}
	siblings, err := s.scheduleSiblings(ctx, d)
	if err != nil {
		return err
	}
	decision := schedule.Decide(sr, d, siblings, now)
	var overridden []string
	if decision.Action == schedule.ActionStart {
		decision, overridden = s.scheduleStartChecks(ctx, sr, app, d, siblings, now)
	}
	switch decision.Action {
	case schedule.ActionWait:
		return nil
	case schedule.ActionEnd:
		return s.endSchedule(ctx, store, sr, app, d, decision, now)
	}
	return s.startSchedule(ctx, store, sr, app, d, siblings, overridden, now)
}

// scheduleSiblings are the release's other live releases in its scope.
func (s *server) scheduleSiblings(ctx context.Context, d state.Deployment) ([]state.Deployment, error) {
	live, err := s.store.LiveDeployments(ctx, d.AppID)
	if err != nil {
		return nil, fmt.Errorf("read live releases: %w", err)
	}
	scope := func(v string) string {
		if v == "" {
			return api.DefaultEnvScope
		}
		return v
	}
	var out []state.Deployment
	for _, other := range live {
		if other.ID != d.ID && scope(other.Scope) == scope(d.Scope) {
			out = append(out, other)
		}
	}
	return out, nil
}

// scheduleStartChecks rechecks freezes, approvals and the canary worker
// before a start. It returns the start decision and the freezes the
// schedule's override covers now.
func (s *server) scheduleStartChecks(ctx context.Context, sr state.ScheduledRelease, app state.App, d state.Deployment, siblings []state.Deployment, now time.Time) (schedule.Decision, []string) {
	active, err := freeze.ActiveFor(ctx, s.store, app.AccountID, app.ID, d.Scope, now)
	if err != nil {
		s.log.Warn("scheduled releases: read freezes", "schedule", sr.ID, "err", err)
		return schedule.WaitOrExpire(sr, now, "Gregale could not read the deploy freezes"), nil
	}
	if blocking := schedule.UnoverriddenFreezes(active, sr.OverrideFreezeIDs); len(blocking) > 0 {
		return schedule.Blocked(api.CodeReleaseFrozen, freeze.Summary(app.Slug, blocking)), nil
	}
	ladder, p := s.scheduleLadder(sr, siblings)
	if p != nil {
		return schedule.Blocked(schedule.CodeStartRefused, p.Detail), nil
	}
	if ladder.TotalSteps == 0 && schedule.Serving(siblings) {
		required, err := approval.CanaryRequired(ctx, s.store, app, d.Scope, "")
		if err != nil {
			return schedule.WaitOrExpire(sr, now, "Gregale could not read the approval policy"), nil
		}
		if required {
			return schedule.Blocked(api.CodeReleaseApprovalCanaryRequired,
				"the app requires approvals, which wait in a canary, and this release has none"), nil
		}
	}
	if ladder.TotalSteps > 0 && !s.canaryWorkerReady(ctx) {
		return schedule.WaitOrExpire(sr, now, "the canary worker was unavailable"), nil
	}
	covered := make([]string, 0, len(active))
	for _, f := range active {
		covered = append(covered, f.ID)
	}
	return schedule.Decision{Action: schedule.ActionStart}, covered
}

// scheduleLadder is the canary a schedule starts with. Without a serving
// release there is nothing to compare a canary with, so the release takes
// all traffic, as a first deploy does.
func (s *server) scheduleLadder(sr state.ScheduledRelease, siblings []state.Deployment) (state.ScheduledReleaseLadder, *api.Problem) {
	spec, err := schedule.Spec(sr.Canary)
	if err != nil {
		return state.ScheduledReleaseLadder{}, api.ErrValidation("its stored canary could not be read")
	}
	ladder, p := schedule.Ladder(spec)
	if p != nil || !schedule.Serving(siblings) {
		return state.ScheduledReleaseLadder{}, p
	}
	return ladder, nil
}

func (s *server) canaryWorkerReady(ctx context.Context) bool {
	leases, ok := s.store.(state.SafeReleaseWorkerLeaseStore)
	if !ok {
		return false
	}
	ready, err := leases.SafeReleaseWorkerLeaseReady(ctx)
	return err == nil && ready
}

// startSchedule gives the release its first traffic.
func (s *server) startSchedule(ctx context.Context, store state.ScheduledReleaseStore, sr state.ScheduledRelease, app state.App, d state.Deployment, siblings []state.Deployment, overridden []string, now time.Time) error {
	ladder, _ := s.scheduleLadder(sr, siblings)
	started, err := store.StartScheduledRelease(ctx, state.ScheduledReleaseStart{
		ScheduleID: sr.ID, DeploymentID: d.ID, Ladder: ladder, Now: now,
	})
	if err != nil {
		return s.scheduleStartFailed(ctx, store, sr, app, d, err, now)
	}
	s.releaseMetrics.Decision(releasemetrics.GuardSchedule, "started")
	if s.notif != nil {
		if err := s.notif.Notify(ctx, db.NotifyDeploymentChanged,
			fmt.Sprintf(`{"kind":"traffic","app_id":"%s","deployment_id":"%s","traffic_percent":%d}`,
				app.ID, started.ID, started.TrafficPercent)); err != nil {
			s.log.Warn("scheduled releases: notify deployment_changed", "deployment", started.ID, "err", err)
		}
	}
	if s.audit != nil {
		s.audit.Emit(ctx, "release.schedule_started", &app.AccountID, map[string]any{
			"app": app.ID, "deployment": started.ID, "schedule": sr.ID, "release_at": sr.ReleaseAt.Format(time.RFC3339),
			"traffic_percent": started.TrafficPercent, "canary_steps": started.CanaryTotalSteps, "scheduled_by": sr.ScheduledBy,
		})
		if len(overridden) > 0 {
			s.audit.Emit(ctx, "release.freeze_overridden", &app.AccountID, map[string]any{
				"app": app.ID, "freezes": overridden, "reason": sr.OverrideFreeze, "actor": sr.ScheduledBy, "schedule": sr.ID,
			})
		}
	}
	s.notifySchedule(ctx, app, started, sr, alertchannels.EventScheduleStarted, "")
	return nil
}

// scheduleStartFailed handles a start the traffic change refused. A release
// that changed under the worker is decided again on the next pass; a traffic
// gate's refusal ends the schedule; anything else is retried until the
// schedule's grace runs out.
func (s *server) scheduleStartFailed(ctx context.Context, store state.ScheduledReleaseStore, sr state.ScheduledRelease, app state.App, d state.Deployment, err error, now time.Time) error {
	switch {
	case errors.Is(err, state.ErrNotFound):
		return nil
	case errors.Is(err, state.ErrTrafficChangeDuringCanary), errors.Is(err, state.ErrDeploymentNotLive),
		errors.Is(err, state.ErrScheduledReleaseNotParked), errors.Is(err, state.ErrTrafficPercentSumInvalid):
		if decision := schedule.WaitOrExpire(sr, now, "it could not take traffic"); decision.Action == schedule.ActionEnd {
			return s.endSchedule(ctx, store, sr, app, d, decision, now)
		}
		return nil
	}
	if detail, refused := scheduleStartRefusal(err); refused {
		return s.endSchedule(ctx, store, sr, app, d, schedule.Blocked(schedule.CodeStartRefused, detail), now)
	}
	if decision := schedule.WaitOrExpire(sr, now, "Gregale could not start it"); decision.Action == schedule.ActionEnd {
		if endErr := s.endSchedule(ctx, store, sr, app, d, decision, now); endErr != nil {
			return endErr
		}
	}
	return err
}

// scheduleStartRefusal names a traffic gate's refusal in words a customer
// sees elsewhere for the same gate.
func scheduleStartRefusal(err error) (string, bool) {
	if p := trafficguard.PromotionProblem(err); p != nil {
		return p.Detail, true
	}
	if errors.Is(err, state.ErrMCPTaskCompatibilityGate) {
		return api.ErrMCPTaskCompatibilityGate(err.Error()).Detail, true
	}
	var removal *state.RouteRemovalBlockedError
	if errors.As(err, &removal) {
		return "the route removal guard refused it: " + removal.Error(), true
	}
	if errors.Is(err, state.ErrConflict) {
		return "the app's releases are managed by an environment, so this release cannot take traffic by itself", true
	}
	return "", false
}

// endSchedule ends a schedule without starting it. Blocked and expired
// schedules notify their channels.
func (s *server) endSchedule(ctx context.Context, store state.ScheduledReleaseStore, sr state.ScheduledRelease, app state.App, d state.Deployment, decision schedule.Decision, now time.Time) error {
	ended, err := store.FinishScheduledRelease(ctx, sr.ID, decision.State, decision.Code, schedule.ClipDetail(decision.Detail), now)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("end schedule: %w", err)
	}
	kind, outcome := "release.schedule_cancelled", "ended"
	if decision.State == state.ScheduledReleaseBlocked || decision.State == state.ScheduledReleaseExpired {
		kind, outcome = "release.schedule_blocked", "blocked"
		s.notifySchedule(ctx, app, d, ended, alertchannels.EventScheduleBlocked, ended.OutcomeDetail)
	}
	s.releaseMetrics.Decision(releasemetrics.GuardSchedule, outcome)
	if s.audit != nil {
		s.audit.Emit(ctx, kind, &app.AccountID, map[string]any{
			"app": app.ID, "deployment": d.ID, "schedule": sr.ID, "state": decision.State, "code": decision.Code,
			"detail": ended.OutcomeDetail, "scheduled_by": sr.ScheduledBy,
		})
	}
	return nil
}

// notifySchedule records a schedule outcome for the schedule's channels; a
// failure to record it never changes the outcome.
func (s *server) notifySchedule(ctx context.Context, app state.App, d state.Deployment, sr state.ScheduledRelease, event, detail string) {
	store, ok := s.store.(state.ReleaseApprovalEventStore)
	if !ok || len(sr.NotifyChannelIDs) == 0 {
		return
	}
	payload, err := json.Marshal(schedule.Notice(event, app, d, sr, detail, s.releaseApprovalReviewURL(d.ID)))
	if err != nil {
		s.log.Warn("scheduled releases: encode notice", "deployment", d.ID, "err", err)
		return
	}
	outboxEvent := state.ReleaseApprovalEventScheduleStarted
	if event == alertchannels.EventScheduleBlocked {
		outboxEvent = state.ReleaseApprovalEventScheduleBlocked
	}
	if _, err := store.EnqueueReleaseApprovalEvent(ctx, state.ReleaseApprovalEvent{
		AccountID: app.AccountID, AppID: app.ID, DeploymentID: d.ID, Event: outboxEvent,
		ChannelIDs: sr.NotifyChannelIDs, Payload: payload,
	}, time.Now()); err != nil {
		s.log.Warn("scheduled releases: record notice", "deployment", d.ID, "event", outboxEvent, "err", err)
	}
}
