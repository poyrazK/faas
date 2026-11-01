package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An app has one cron per schedule, path and command
// (crons_app_schedule_path_command_unique). Both stores refuse a second one,
// by create or by update, with ErrConflict; an identical retry of a create
// still returns the existing row.
func TestCronScheduleAndPathConflict(t *testing.T) {
	cronAnchorBackends(t, func(t *testing.T, ctx context.Context, s state.Store, appID, _ string) {
		limits := api.MustLimitsFor(api.PlanPro)
		create := func(path, timezone string) (state.Cron, error) {
			return s.CreateCronIfUnderQuotaWithOptions(ctx, appID, "0 9 * * *", path, true, limits, state.CronOptions{Timezone: timezone})
		}
		first, err := create("/report", "Europe/Istanbul")
		if err != nil {
			t.Fatal(err)
		}
		if again, err := create("/report", "Europe/Istanbul"); err != nil || again.ID != first.ID {
			t.Fatalf("identical retry = %+v, %v; want the existing cron", again, err)
		}
		if _, err := create("/report", "America/New_York"); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("second cron on the same schedule and path: err = %v, want ErrConflict", err)
		}
		other, err := create("/report?tz=newyork", "America/New_York")
		if err != nil {
			t.Fatal(err)
		}
		taken := "/report"
		if _, err := s.UpdateCronWithOptions(ctx, other.ID, nil, &taken, nil, nil, nil, nil); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("update onto a taken schedule and path: err = %v, want ErrConflict", err)
		}
	})
}
