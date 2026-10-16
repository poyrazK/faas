package cacheschedule

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ActivityProblem only exposes scoped, compact recovery metadata from the store.
func ActivityProblem(err error) *api.Problem {
	var expired *state.EnvironmentCacheActivityExpiredError
	if !errors.As(err, &expired) {
		return nil
	}
	p := api.NewProblem(http.StatusGone, api.CodeActivityCursorExpired, "Activity cursor expired", "Some schedule activity is no longer retained; rebuild a current schedule baseline")
	p.ActivityRecovery = &expired.Recovery
	p.Hint = "Save restart_cursor before listing current schedules, then resume activity after it; reconcile overlapping schedule identities. Historical outcomes in the expired prefix cannot be recovered."
	return p
}

func RunActivityRetention(ctx context.Context, s state.EnvironmentCacheScheduleActivityPruner, log *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		maintenanceCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := s.PruneEnvironmentCacheScheduleActivity(maintenanceCtx, time.Now().UTC().Add(-time.Duration(api.EnvironmentCacheActivityRetentionSeconds)*time.Second))
		cancel()
		if err != nil && ctx.Err() == nil {
			log.WarnContext(ctx, "environment cache schedule activity cleanup failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
