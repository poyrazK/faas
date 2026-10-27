// adr: 20261026201726
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// cronAnchorBackends runs fn against a MemStore and, when Postgres is
// reachable, a migrated PgStore: the crons trigger and the MemStore mirror
// must agree.
func cronAnchorBackends(t *testing.T, fn func(t *testing.T, ctx context.Context, s state.Store, appID, accountID string)) {
	t.Run("memory", func(t *testing.T) {
		s := state.NewMemStore()
		ctx := context.Background()
		acct, err := s.CreateAccount(ctx, "cron-anchor@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "cron-anchor", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		fn(t, ctx, s, app.ID, acct.ID)
	})
	t.Run("postgres", func(t *testing.T) {
		s, _, ctx := cronPgStore(t)
		appID := seedCronApp(t, ctx, s, "anchor")
		app, err := s.AppByID(ctx, appID)
		if err != nil {
			t.Fatal(err)
		}
		fn(t, ctx, s, appID, app.AccountID)
	})
}

func TestCronScheduleAnchor(t *testing.T) {
	cronAnchorBackends(t, func(t *testing.T, ctx context.Context, s state.Store, appID, _ string) {
		c, err := s.CreateCron(ctx, appID, "* * * * *", "/tick", true)
		if err != nil {
			t.Fatal(err)
		}
		fired := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
		if err := s.MarkCronFired(ctx, c.ID, fired); err != nil {
			t.Fatal(err)
		}
		update := func(schedule, path *string, enabled, skip *bool) state.Cron {
			t.Helper()
			time.Sleep(2 * time.Millisecond) // keep successive anchors distinct
			got, err := s.UpdateCronWithOptions(ctx, c.ID, schedule, path, enabled, nil, skip, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !got.LastFiredAt.Equal(fired) {
				t.Fatalf("last_fired_at = %v, want %v: the anchor never rewrites it", got.LastFiredAt, fired)
			}
			return got
		}
		path, every5, on, off := "/other", "*/5 * * * *", true, false
		if got := update(nil, &path, nil, nil); !got.ScheduleAnchorAt.IsZero() {
			t.Fatalf("path edit anchored the schedule: %v", got.ScheduleAnchorAt)
		}
		edited := update(&every5, nil, nil, nil)
		if edited.ScheduleAnchorAt.IsZero() || !edited.ScheduleCursor().Equal(edited.ScheduleAnchorAt) {
			t.Fatalf("schedule edit: anchor %v cursor %v", edited.ScheduleAnchorAt, edited.ScheduleCursor())
		}
		if got := update(nil, nil, nil, &on); !got.ScheduleAnchorAt.Equal(edited.ScheduleAnchorAt) {
			t.Fatalf("skip_if_running edit moved the anchor to %v", got.ScheduleAnchorAt)
		}
		if got := update(nil, nil, &off, nil); !got.ScheduleAnchorAt.Equal(edited.ScheduleAnchorAt) {
			t.Fatalf("disable moved the anchor to %v", got.ScheduleAnchorAt)
		}
		enabled := update(nil, nil, &on, nil)
		if !enabled.ScheduleAnchorAt.After(edited.ScheduleAnchorAt) {
			t.Fatalf("enable anchor %v, want after %v", enabled.ScheduleAnchorAt, edited.ScheduleAnchorAt)
		}
		if err := s.MarkCronFired(ctx, c.ID, fired); err != nil {
			t.Fatal(err)
		}
		suspender := s.(state.CronSuspensionStore)
		if n, err := suspender.SuspendCronsForApp(ctx, appID, state.CronSuspendedNoLiveDeployment); err != nil || n != 1 {
			t.Fatalf("SuspendCronsForApp = %d, %v", n, err)
		}
		time.Sleep(2 * time.Millisecond)
		reactivator, ok := s.(interface {
			ReactivateCronsForApp(context.Context, string) (int, error)
		})
		if !ok {
			t.Fatal("store cannot reactivate crons")
		}
		if n, err := reactivator.ReactivateCronsForApp(ctx, appID); err != nil || n != 1 {
			t.Fatalf("ReactivateCronsForApp = %d, %v", n, err)
		}
		reactivated, err := s.CronByID(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reactivated.ScheduleAnchorAt.After(enabled.ScheduleAnchorAt) {
			t.Fatalf("reactivation anchor %v, want after %v", reactivated.ScheduleAnchorAt, enabled.ScheduleAnchorAt)
		}
	})
}

func TestFireNowRequestCapAndInvocationOutcome(t *testing.T) {
	cronAnchorBackends(t, func(t *testing.T, ctx context.Context, s state.Store, appID, accountID string) {
		capped, ok := s.(state.CronFireNowInvocationStore)
		if !ok {
			t.Fatal("store does not settle fire-now requests from invocations")
		}
		c, err := s.CreateCron(ctx, appID, "0 0 1 1 *", "/report", true)
		if err != nil {
			t.Fatal(err)
		}
		since := time.Now().Add(-time.Minute)
		first, err := capped.InsertFireNowRequestUnderCap(ctx, c.ID, accountID, 2, since)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := capped.InsertFireNowRequestUnderCap(ctx, c.ID, accountID, 2, since); err != nil {
			t.Fatal(err)
		}
		_, err = capped.InsertFireNowRequestUnderCap(ctx, c.ID, accountID, 2, since)
		var capErr *state.FireNowCapError
		if !errors.As(err, &capErr) || capErr.Limit != 2 || capErr.Observed != 2 || !errors.Is(err, state.ErrQuotaExceeded) {
			t.Fatalf("third request err = %v, want cap 2 observed 2", err)
		}

		claimed, err := s.ClaimPendingFireNowRequest(ctx)
		if err != nil || claimed.ID != first {
			t.Fatalf("claim = %+v, %v; want the oldest request %s", claimed, err, first)
		}
		cronID := c.ID
		inv, err := s.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID, Source: state.InvocationCron,
			Method: "POST", Path: "/report", CronID: &cronID, DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if err := capped.MarkFireNowRequestQueued(ctx, first, inv.ID); err != nil {
			t.Fatal(err)
		}
		running, err := s.GetFireNowRequest(ctx, first)
		if err != nil || running.Status != state.FireNowStatusRunning || running.InvocationID == nil || *running.InvocationID != inv.ID {
			t.Fatalf("queued request = %+v, %v", running, err)
		}
		if _, err := s.ClaimInvocationWithCap(ctx, inv.ID, "", 60, 100); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteInvocation(ctx, inv.ID, nil); err != nil {
			t.Fatal(err)
		}
		done, err := s.GetFireNowRequest(ctx, first)
		if err != nil || done.Status != state.FireNowStatusSucceeded || done.FinishedAt == nil {
			t.Fatalf("settled request = %+v, %v; want succeeded with a finish time", done, err)
		}
		// The settled request no longer counts against the cap.
		if _, err := capped.InsertFireNowRequestUnderCap(ctx, c.ID, accountID, 2, since); err != nil {
			t.Fatalf("settled request still counted: %v", err)
		}

		second, err := s.ClaimPendingFireNowRequest(ctx)
		if err != nil {
			t.Fatal(err)
		}
		failing, err := s.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID, Source: state.InvocationCron,
			Method: "POST", Path: "/report", CronID: &cronID, DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if err := capped.MarkFireNowRequestQueued(ctx, second.ID, failing.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ClaimInvocationWithCap(ctx, failing.ID, "", 60, 100); err != nil {
			t.Fatal(err)
		}
		if err := s.FailInvocation(ctx, failing.ID, "invoke: handler returned 500", 0, 0); err != nil {
			t.Fatal(err)
		}
		failed, err := s.GetFireNowRequest(ctx, second.ID)
		if err != nil || failed.Status != state.FireNowStatusFailed || failed.Error == nil || !strings.Contains(*failed.Error, "handler returned 500") {
			t.Fatalf("failed request = %+v, %v; want failed with the invocation error", failed, err)
		}
		// Requests made before the window do not count: one is still
		// pending, yet a cap of one admits another.
		if _, err := capped.InsertFireNowRequestUnderCap(ctx, c.ID, accountID, 1, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("requests before the window must not count: %v", err)
		}
	})
}
