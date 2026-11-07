package apidsource

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// buildQuotaStore is what the monthly build-minute quota reads: the app's
// billing subject and the time its builds ran this month. A store without it
// enforces no quota.
type buildQuotaStore interface {
	state.AppOwnerReader
	state.BuildMinutesStore
	AppByID(ctx context.Context, id string) (state.App, error)
}

// buildMinutesProblem refuses a source build once the app's billing subject's
// builds have run its plan's monthly build minutes (ADR-20261103131742
// amendment 2). An image deployment builds nothing and is never refused. A
// failed read lets the build through: the quota protects the builder, it is
// not a charge.
func buildMinutesProblem(ctx context.Context, store Store, appID string, kind state.DeploymentKind, now time.Time, log *slog.Logger) *api.Problem {
	if kind == state.DeploymentKindImage {
		return nil
	}
	quota, ok := store.(buildQuotaStore)
	if !ok {
		return nil
	}
	app, err := quota.AppByID(ctx, appID)
	if err != nil {
		buildMinutesUnreadable(log, appID, err)
		return nil
	}
	owner, err := state.AppResourceAccount(ctx, quota, app)
	if err != nil {
		buildMinutesUnreadable(log, appID, err)
		return nil
	}
	limits, ok := api.LimitsFor(owner.Plan)
	if !ok || limits.MonthlyBuildMinutes <= 0 {
		return nil
	}
	now = now.UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	seconds, err := quota.BillingSubjectBuildSeconds(ctx, owner.ResourceOwnerID(), month, month.AddDate(0, 1, 0))
	if err != nil {
		buildMinutesUnreadable(log, appID, err)
		return nil
	}
	if used := seconds / 60; used >= int64(limits.MonthlyBuildMinutes) {
		return api.ErrPlanLimitBuildMinutes(limits, used)
	}
	return nil
}

// buildMinutesUnreadable logs a read the build quota could not make; the
// build is admitted.
func buildMinutesUnreadable(log *slog.Logger, appID string, err error) {
	if log != nil {
		log.Warn("apidsource: build minutes unreadable; build admitted", "app_id", appID, "err", err)
	}
}
