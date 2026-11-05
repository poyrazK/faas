package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
)

// snapshotFanoutWake turns db.NotifySnapshotFanout into the snapshot replica
// runner's wake signal. The resync a LISTEN reconnect or a dropped delivery
// produces wakes it too, since a fan-out may have been missed meanwhile. One
// pending wake covers any number of notifications: the runner rereads its
// cursor.
func snapshotFanoutWake(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) (<-chan struct{}, error) {
	notes, err := db.SubscribeWithResync(ctx, pool, []string{db.NotifySnapshotFanout}, log)
	if err != nil {
		return nil, err
	}
	return coalesceWakes(notes), nil
}

// coalesceWakes forwards every notification as at most one pending wake.
func coalesceWakes(notes <-chan db.Notification) <-chan struct{} {
	wake := make(chan struct{}, 1)
	go func() {
		for range notes {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}()
	return wake
}
