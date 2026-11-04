package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestDurablePrimeHandoffSurvivesDeliveryCompletion(t *testing.T) {
	daemonCtx, stopDaemon := context.WithCancel(context.Background())
	defer stopDaemon()
	deliveryCtx, finishDelivery := context.WithCancel(daemonCtx)
	defer finishDelivery()
	var queuedCtx context.Context
	replay := durableReplayHandler(daemonCtx, func(ctx context.Context, _ db.Notification) error {
		queuedCtx = ctx
		return nil // the scheduler worker will run after the delivery completes
	})
	if err := replay(deliveryCtx, db.Notification{Channel: db.NotifySnapshotPrime}); err != nil {
		t.Fatal(err)
	}
	finishDelivery()
	if queuedCtx.Err() != nil {
		t.Fatalf("accepted prime was cancelled by completed delivery: %v", queuedCtx.Err())
	}
	stopDaemon()
	if queuedCtx.Err() != context.Canceled {
		t.Fatal("queued prime lost daemon shutdown cancellation")
	}
}

// A peer's replay of another node's wake steps aside instead of failing it,
// so the row keeps its retry budget for the owner; other errors still fail.
func TestDurableReplayDefersAnotherNodesWake(t *testing.T) {
	for _, channel := range []string{db.NotifyAppWake, db.NotifyRuntimeConfigRestart} {
		t.Run(channel, func(t *testing.T) {
			foreign := fmt.Errorf("sched: EnsureWake: app owned by node n2: %w", sched.ErrAppOwnedByAnotherNode)
			replay := durableReplayHandler(context.Background(), func(context.Context, db.Notification) error { return foreign })
			err := replay(t.Context(), db.Notification{Channel: channel})
			var deferred *db.DeferredNotificationError
			if !errors.As(err, &deferred) || deferred.Delay <= 0 || !errors.Is(err, sched.ErrAppOwnedByAnotherNode) {
				t.Fatalf("replay of another node's wake = %v, want a deferral", err)
			}
			boom := errors.New("vmmd unavailable")
			replay = durableReplayHandler(context.Background(), func(context.Context, db.Notification) error { return boom })
			if err := replay(t.Context(), db.Notification{Channel: channel}); !errors.Is(err, boom) || errors.As(err, &deferred) {
				t.Fatalf("replay failure = %v, want the handler's error unchanged", err)
			}
		})
	}
}

func TestSynchronousDurableHandlersObserveLostDeliveryOwnership(t *testing.T) {
	for _, channel := range []string{db.NotifyAppWake, db.NotifyRuntimeConfigRestart} {
		t.Run(channel, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			var workCtx context.Context
			replay := durableReplayHandler(context.Background(), func(ctx context.Context, _ db.Notification) error { workCtx = ctx; return nil })
			if err := replay(ctx, db.Notification{Channel: channel}); err != nil {
				t.Fatal(err)
			}
			cancel()
			if workCtx.Err() != context.Canceled {
				t.Fatal("synchronous handler ignored delivery cancellation")
			}
		})
	}
}
