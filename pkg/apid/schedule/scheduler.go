package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// CodeSchedulerLostAccess ends a schedule whose scheduler may no longer
// release it: the account or API key that scheduled it is gone, suspended,
// revoked or expired, has lost the right to deploy the app, or may no longer
// lift a freeze the schedule overrides.
const CodeSchedulerLostAccess = "scheduler_lost_access"

// SchedulerStore is what the scheduler check reads.
type SchedulerStore interface {
	appaccess.Store
	GetAPIKey(ctx context.Context, accountID, keyID string) (state.APIKey, error)
}

// SchedulerStanding checks, at release time, that whoever scheduled sr may
// still release it (ADR-20261021152201 amendment, 2026-11-10): a schedule
// must not ship for a member who has left the company, a revoked key, or a
// closed account. active are the freezes in force now; the schedule's
// override covers them only while the scheduler may still lift each company
// freeze, as the override itself required (ADR-20261019071733 amendment 7).
//
// It returns ok=true when the start may go ahead. A refusal ends the
// schedule as blocked, so its channels hear of it and anyone who may deploy
// can schedule the release again. A read failure waits, then expires. A
// schedule recorded before scheduler identities were stored has none and is
// not checked.
func SchedulerStanding(ctx context.Context, store SchedulerStore, sr state.ScheduledRelease, app state.App, active []state.ReleaseFreeze, now time.Time) (Decision, bool) {
	if sr.ScheduledByAccountID == "" {
		return Decision{}, true
	}
	lost := func(why string) (Decision, bool) {
		return Blocked(CodeSchedulerLostAccess, sr.ScheduledBy+" scheduled this release and "+why+
			"; someone who may deploy the app can schedule or release it again"), false
	}
	unreadable := func() (Decision, bool) {
		return WaitOrExpire(sr, now, "Gregale could not check who scheduled it"), false
	}
	actor, err := store.AccountByID(ctx, sr.ScheduledByAccountID)
	if errors.Is(err, state.ErrNotFound) {
		return lost("that account no longer exists")
	}
	if err != nil {
		return unreadable()
	}
	if actor.Status == state.AccountSuspended || actor.Status == state.AccountDeletedPending {
		return lost("that account is " + string(actor.Status))
	}
	if sr.ScheduledByKeyID != "" {
		key, err := store.GetAPIKey(ctx, actor.ID, sr.ScheduledByKeyID)
		switch {
		case errors.Is(err, state.ErrNotFound):
			return lost("its API key was deleted")
		case err != nil:
			return unreadable()
		case key.RevokedAt != nil:
			return lost("its API key was revoked")
		case key.ExpiresAt != nil && !key.ExpiresAt.After(now):
			return lost("its API key expired")
		}
	}
	deploys, err := appaccess.Standing(ctx, store, actor.ID, app, authz.OrgActionDeployApp)
	if err != nil {
		return unreadable()
	}
	if !deploys {
		return lost("may no longer deploy " + app.Slug)
	}
	for _, f := range active {
		if f.OrgID == "" || !covers(sr.OverrideFreezeIDs, f.ID) {
			continue
		}
		// The creator of a manual window may lift it (freezeLiftProblem).
		if f.Kind == state.ReleaseFreezeWindow && f.Source == state.ReleaseFreezeSourceManual && f.CreatedBy == sr.ScheduledBy {
			continue
		}
		lifts, err := appaccess.Standing(ctx, store, actor.ID, app, authz.OrgActionManageAppSecurity)
		if err != nil {
			return unreadable()
		}
		if !lifts {
			return lost("may no longer lift the company freeze " + f.ID + " it overrides")
		}
	}
	return Decision{}, true
}

func covers(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}
