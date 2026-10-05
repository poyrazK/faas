package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
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
