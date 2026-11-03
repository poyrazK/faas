package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// A manual run claimed but never linked to a queued run is failed with the
// given reason once it is old enough; a pending one, a fresh one and one that
// queued its run are left alone (crons audit 2026-11-02).
func TestFailInterruptedFireNowRequests(t *testing.T) {
	cronAnchorBackends(t, func(t *testing.T, ctx context.Context, s state.Store, appID, accountID string) {
		recovery, ok := s.(state.FireNowRecoveryStore)
		if !ok {
			t.Fatal("store does not settle interrupted manual runs")
		}
		cron, err := s.CreateCron(ctx, appID, "0 3 * * *", "/nightly", true)
		if err != nil {
			t.Fatal(err)
		}
		claim := func() string {
			t.Helper()
			id, err := s.InsertFireNowRequest(ctx, cron.ID, accountID)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.ClaimPendingFireNowRequest(ctx)
			if err != nil || got.ID != id {
				t.Fatalf("claim = %+v, %v; want %s", got, err, id)
			}
			return id
		}
		interrupted := claim()
		queued := claim()
		inv, err := s.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID, CronID: &cron.ID,
			Source: state.InvocationCron, Method: "POST", Path: "/nightly", DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.(state.CronFireNowInvocationStore).MarkFireNowRequestQueued(ctx, queued, inv.ID); err != nil {
			t.Fatal(err)
		}
		pending, err := s.InsertFireNowRequest(ctx, cron.ID, accountID)
		if err != nil {
			t.Fatal(err)
		}

		n, err := recovery.FailInterruptedFireNowRequests(ctx, "", time.Now().Add(time.Minute), "interrupted")
		if err != nil || n != 1 {
			t.Fatalf("failed = %d, %v; want 1", n, err)
		}
		for id, want := range map[string]state.FireNowStatus{interrupted: state.FireNowStatusFailed, queued: state.FireNowStatusRunning, pending: state.FireNowStatusPending} {
			got, err := s.GetFireNowRequest(ctx, id)
			if err != nil || got.Status != want {
				t.Fatalf("request %s = %+v (err %v), want %s", id, got, err, want)
			}
		}
		if got, _ := s.GetFireNowRequest(ctx, interrupted); got.Error == nil || *got.Error != "interrupted" {
			t.Fatalf("interrupted request error = %v, want the reason", got.Error)
		}
		if n, err := recovery.FailInterruptedFireNowRequests(ctx, "", time.Now().Add(-time.Hour), "interrupted"); err != nil || n != 0 {
			t.Fatalf("a sweep of older requests failed %d, %v; want none", n, err)
		}
	})
}
