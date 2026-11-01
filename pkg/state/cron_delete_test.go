package state_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Deleting a cron cancels its runs that have not started, so a deleted cron
// never calls the app again; a delete addressed to the wrong app touches
// nothing.
func TestDeleteCronCancelsRunsThatHaveNotStarted(t *testing.T) {
	cronAnchorBackends(t, func(t *testing.T, ctx context.Context, s state.Store, appID, accountID string) {
		c, err := s.CreateCron(ctx, appID, "* * * * *", "/tick", true)
		if err != nil {
			t.Fatal(err)
		}
		cronID := c.ID
		now := time.Now().UTC()
		queued, err := s.EnqueueInvocation(ctx, state.Invocation{
			AppID: appID, AccountID: accountID, Source: state.InvocationCron, CronID: &cronID,
			Method: http.MethodPost, Path: "/tick", DueAt: now, ScheduledAt: &now,
		})
		if err != nil {
			t.Fatalf("EnqueueInvocation: %v", err)
		}

		if err := s.DeleteCron(ctx, cronID, "00000000-0000-0000-0000-000000000001"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("delete with the wrong app: err = %v, want ErrNotFound", err)
		}
		if got, err := s.InvocationByID(ctx, queued.ID); err != nil || got.State != state.InvocationPending {
			t.Fatalf("after a refused delete: state %s (%v), want the run still pending", got.State, err)
		}

		if err := s.DeleteCron(ctx, cronID, appID); err != nil {
			t.Fatalf("DeleteCron: %v", err)
		}
		got, err := s.InvocationByID(ctx, queued.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != state.InvocationCancelled {
			t.Fatalf("queued run of a deleted cron: state %s, want cancelled", got.State)
		}
	})
}
