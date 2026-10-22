// adr: 20261019120000
package sched

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/state"
)

// dispatchRehearsalSweep lists pending rehearsal worker faults and
// suspend/resumes off the loop goroutine. Both are database reads that run
// every trigger tick on every node; a slow read must not hold the loop's
// other cases. The constant key coalesces ticks while one sweep runs.
func (l *Loop) dispatchRehearsalSweep(ctx context.Context) {
	l.submitWork(workRehearsalSweep, "tick", func() {
		l.dispatchScenarioWorkerFaults(ctx)
		l.dispatchScenarioSuspendResumes(ctx)
	})
}

type rehearsalDispatchGuard interface {
	ScenarioWorkerDispatchBlocked(context.Context, string) (bool, error)
}

// rehearsalDispatchBlocked reports whether a rehearsal worker fault,
// suspend/resume or PostgreSQL fault currently holds the app's async
// dispatch. The read is advisory: the claim UPDATE's rehearsal triggers
// (migrations 021, 024 and 026) enforce the same holds, so a failed read
// claims under those guards instead of silently stalling every tenant's
// dispatch while the read keeps failing.
func rehearsalDispatchBlocked(ctx context.Context, store any, inv state.Invocation, log *slog.Logger) bool {
	guard, ok := store.(rehearsalDispatchGuard)
	if !ok {
		return false
	}
	blocked, err := guard.ScenarioWorkerDispatchBlocked(ctx, inv.AppID)
	if err != nil {
		log.Warn("drain: rehearsal dispatch pre-check failed; claiming under database guards",
			"inv", inv.ID, "app_id", inv.AppID, "err", err)
		return false
	}
	return blocked
}
