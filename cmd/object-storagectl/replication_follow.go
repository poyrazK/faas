package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

type replicationFollowStats struct {
	ChangeWaits int64   `json:"change_waits"`
	WaitMS      float64 `json:"change_wait_ms"`
	Retries     int64   `json:"retries"`
	Reconnects  int64   `json:"reconnects"`
	LagRecords  int64   `json:"source_lag_records"`
	retrying    bool
}

type replicationFollowProgress struct {
	objectnode.ReplicationProgress
	Follow     replicationFollowStats      `json:"follow"`
	Compaction objectnode.CompactionStatus `json:"compaction"`
	ObservedAt time.Time                   `json:"observed_at"`
	Error      string                      `json:"error,omitempty"`
}

func (s *replicationFollowStats) success(p objectnode.ReplicationProgress) {
	s.LagRecords = p.SourceSequence - p.Local.AppliedSequence
	if s.retrying {
		s.Reconnects++
		s.retrying = false
	}
}

func (s *replicationFollowStats) wait(ctx context.Context, source objectnode.ReplicationSource, cursor *objectnode.ReplicationWaitCursor) error {
	waiter, ok := source.(objectnode.ReplicationWaiter)
	if !ok || cursor == nil {
		return replicationPause(ctx)
	}
	s.ChangeWaits++
	started := time.Now()
	_, err := waiter.WaitForChange(ctx, *cursor)
	s.WaitMS += float64(time.Since(started)) / float64(time.Millisecond)
	return err
}

func (s *replicationFollowStats) failure(err error) {
	s.Retries++
	s.retrying = true
	slog.New(slog.NewJSONHandler(os.Stderr, nil)).Warn("replica catch-up paused; retrying", "error", err, "retries", s.Retries, "reconnects", s.Reconnects)
}

func replicationPause(ctx context.Context) error {
	timer := time.NewTimer(api.ObjectNodeReplicationPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func permanentReplicationError(err error) bool {
	for _, permanent := range []error{objectnode.ErrReplicaReseed, objectnode.ErrReplicationFork, objectnode.ErrCorrupt, objectnode.ErrDigest, objectnode.ErrInvalid, objectnode.ErrReplica, objectnode.ErrReplicationOff, objectnode.ErrClosed} {
		if errors.Is(err, permanent) {
			return true
		}
	}
	return false
}
