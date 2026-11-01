// adr: 1445 amendment 4 — an automatic rollback that did not restore the
// previous release.
package apidserver

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/freeze"
	"github.com/onebox-faas/faas/pkg/state"
)

// automaticRollbackFailed runs when an automatic rollback's outcome is
// failed: the release it meant to replace still serves. The release used to
// read "Rolled back" on its timeline, and the after-rollback freeze stayed,
// holding the fix that now has to ship (release review 2026-11-01).
func (s *server) automaticRollbackFailed(ctx context.Context, r state.RollbackRecord) {
	app, err := s.store.AppByID(ctx, r.AppID)
	if err != nil {
		s.log.Warn("failed rollback: load app", "app", r.AppID, "rollback", r.ID, "err", err)
		return
	}
	s.liftFailedRollbackFreezes(ctx, app, r)
	s.recordFailedAutomaticRollback(ctx, r)
}

// liftFailedRollbackFreezes lifts the after-rollback freezes the failed
// rollback created: they name the release it meant to replace.
func (s *server) liftFailedRollbackFreezes(ctx context.Context, app state.App, r state.RollbackRecord) {
	store, ok := s.releaseFreezes()
	if !ok {
		return
	}
	owner, err := freeze.Owner(ctx, s.store, app)
	if err != nil {
		s.log.Warn("failed rollback: read freeze owner", "app", app.ID, "rollback", r.ID, "err", err)
		return
	}
	now := time.Now().UTC()
	freezes, err := store.ListReleaseFreezes(ctx, owner, now)
	if err != nil {
		s.log.Warn("failed rollback: list freezes", "app", app.ID, "rollback", r.ID, "err", err)
		return
	}
	for _, f := range freezes {
		if f.Source != state.ReleaseFreezeSourceAutomaticRollback || f.SourceDeploymentID != r.FromDeploymentID || f.LiftedAt != nil {
			continue
		}
		lifted, err := store.LiftReleaseFreeze(ctx, owner, f.ID, automaticFreezeActor, now)
		if err != nil {
			s.log.Warn("failed rollback: lift after-rollback freeze", "app", app.ID, "freeze", f.ID, "err", err)
			continue
		}
		view := s.freezeViews(ctx, []state.ReleaseFreeze{lifted}, now)[0]
		s.auditReleaseFreeze(ctx, nil, state.Account{ID: app.AccountID}, "release.freeze_lifted", lifted, view, map[string]any{
			"freeze": lifted.ID, "reason": lifted.Reason, "actor": automaticFreezeActor, "rollback": r.ID,
			"why": "the automatic rollback did not restore the previous release",
		})
	}
}

// recordFailedAutomaticRollback records the failure on a release the
// post-release watch or the first-wake 5xx guard rolled back, as a decision
// with rollback_skipped "rollback_failed", so its timeline shows the
// regression and that it still serves.
func (s *server) recordFailedAutomaticRollback(ctx context.Context, r state.RollbackRecord) {
	if r.Trigger != state.RollbackTriggerAuto5xx && r.Trigger != state.RollbackTriggerPostRelease {
		return
	}
	store, ok := s.store.(state.PostReleaseWatchStore)
	if !ok {
		return
	}
	from, err := s.store.DeploymentByID(ctx, r.FromDeploymentID)
	if err != nil || from.Status != state.DeployLive {
		return
	}
	decision := api.RolloutDecision{Kind: api.RolloutDecisionAbort, Rule: api.RolloutRuleAbortPostRelease5xx,
		Summary: "first-wake 5xx", TrafficPercent: 100, StableDeploymentID: r.TargetDeploymentID}
	var earlier api.RolloutDecision
	if len(from.RolloutDecision) > 0 && json.Unmarshal(from.RolloutDecision, &earlier) == nil && api.PostReleaseRolloutRule(earlier.Rule) {
		decision = earlier
	}
	decision.RollbackSkipped = api.RollbackSkippedFailed
	decision.DecidedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := decision.Validate(); err != nil {
		s.log.Warn("failed rollback: decision", "deployment", from.ID, "err", err)
		return
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		return
	}
	if err := store.RecordPostReleaseRolloutDecision(ctx, from.ID, raw); err != nil {
		s.log.Warn("failed rollback: record decision", "deployment", from.ID, "err", err)
	}
}
