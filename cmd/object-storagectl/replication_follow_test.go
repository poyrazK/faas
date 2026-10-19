package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/s3gateway"
)

func followFixture(t *testing.T, replica bool) (*objectnode.Store, string, *objectnode.HTTPReplicationSource) {
	t.Helper()
	directory, seed, follower := filepath.Join(t.TempDir(), "primary"), filepath.Join(t.TempDir(), "seed"), filepath.Join(t.TempDir(), "follower")
	node, err := objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = node.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if err = node.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = objectnode.EnableReplication(t.Context(), directory); err != nil {
		t.Fatal(err)
	}
	if _, err = objectnode.Backup(t.Context(), directory, seed); err != nil {
		t.Fatal(err)
	}
	if _, err = objectnode.BootstrapReplica(t.Context(), seed, follower); err != nil {
		t.Fatal(err)
	}
	if replica {
		local, err := objectnode.ReadReplicationStatus(follower)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = objectnode.ConfigureDurability(t.Context(), directory, objectnode.DurabilityReplica, local.NodeID); err != nil {
			t.Fatal(err)
		}
	}
	node, err = objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close() })
	h, err := s3gateway.NewDiskHandler(s3gateway.DiskConfig{Store: node, AccessKeyID: "follow-cli", SecretAccessKey: "follow-cli", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	source, err := objectnode.NewHTTPReplicationSource(server.URL, "us-east-1", "follow-cli", "follow-cli")
	if err != nil {
		t.Fatal(err)
	}
	return node, follower, source
}

type followProgressWriter struct {
	receive func(replicationFollowProgress)
}

func (w followProgressWriter) Write(body []byte) (int, error) {
	var p replicationFollowProgress
	if err := json.Unmarshal(body, &p); err != nil {
		return 0, err
	}
	w.receive(p)
	return len(body), nil
}

type observedFollowSource struct {
	*objectnode.HTTPReplicationSource
	wait      func(context.Context, objectnode.ReplicationWaitCursor) (objectnode.ReplicationStatus, error)
	headCalls int
	waitCalls int
}

func (s *observedFollowSource) Head(ctx context.Context) (objectnode.ReplicationStatus, error) {
	s.headCalls++
	return s.HTTPReplicationSource.Head(ctx)
}

func (s *observedFollowSource) WaitForChange(ctx context.Context, cursor objectnode.ReplicationWaitCursor) (objectnode.ReplicationStatus, error) {
	if err := ctx.Err(); err != nil {
		return objectnode.ReplicationStatus{}, err
	}
	s.waitCalls++
	if s.wait != nil {
		return s.wait(ctx, cursor)
	}
	return s.HTTPReplicationSource.WaitForChange(ctx, cursor)
}

func TestFollowReplicaDrainsPartialBatchesBeforeWaiting(t *testing.T) {
	primary, directory, httpSource := followFixture(t, true)
	for _, key := range []string{"first", "second", "third"} {
		if _, err := primary.Put(t.Context(), "test-bucket", key, strings.NewReader(key[:1]+"ayload!"), 8, objectnode.PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	source := &observedFollowSource{HTTPReplicationSource: httpSource}
	var progress []replicationFollowProgress
	writer := followProgressWriter{receive: func(p replicationFollowProgress) {
		progress = append(progress, p)
		if p.CaughtUp {
			cancel()
		}
	}}
	if err := followReplica(ctx, directory, source, objectnode.CatchUpOptions{MaxEntries: 1, MaxBytes: 2}, true, writer); err != nil {
		t.Fatal(err)
	}
	if len(progress) < 12 || !progress[len(progress)-1].CaughtUp || source.waitCalls != 0 {
		t.Fatal("backlog was paced or waited before being drained", len(progress), source.waitCalls)
	}
	for _, p := range progress[:len(progress)-1] {
		if p.WaitCursor != nil || p.CaughtUp || p.Follow.ChangeWaits != 0 || p.BytesTransferred > 2 || p.EntriesApplied > 1 || p.ConfirmedSequence == nil || *p.ConfirmedSequence != p.Local.AppliedSequence {
			t.Fatal("bounded partial work was waited on or confirmed prematurely", p)
		}
	}
	if err := primary.WaitForReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestFollowReplicaIdleChangeConfirmsAppliedData(t *testing.T) {
	primary, directory, httpSource := followFixture(t, true)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	entered := make(chan struct{}, 1)
	source := &observedFollowSource{HTTPReplicationSource: httpSource}
	source.wait = func(ctx context.Context, cursor objectnode.ReplicationWaitCursor) (objectnode.ReplicationStatus, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		return httpSource.WaitForChange(ctx, cursor)
	}
	done := make(chan error, 1)
	writer := followProgressWriter{receive: func(p replicationFollowProgress) {
		if p.CaughtUp && p.Local.AppliedSequence == 1 && p.ConfirmedSequence != nil && *p.ConfirmedSequence == 1 {
			cancel()
		}
	}}
	go func() {
		done <- followReplica(ctx, directory, source, objectnode.CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes}, true, writer)
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("follower never became idle")
	}
	if _, err := primary.Put(t.Context(), "test-bucket", "new", strings.NewReader("new"), 3, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	local, err := objectnode.ReadReplicationStatus(directory)
	if err != nil || local.AppliedSequence != 1 || local.ReceivedSequence != 1 {
		t.Fatal("change notification did not lead to verified catch-up", local, err)
	}
	if err = primary.WaitForReplica(t.Context()); err != nil {
		t.Fatal("applied data was not confirmed", err)
	}
}

func TestFollowReplicaReconnectsAfterWaitFailureWithoutRecopying(t *testing.T) {
	_, directory, httpSource := followFixture(t, false)
	source := &observedFollowSource{HTTPReplicationSource: httpSource}
	source.wait = func(context.Context, objectnode.ReplicationWaitCursor) (objectnode.ReplicationStatus, error) {
		return objectnode.ReplicationStatus{}, io.ErrUnexpectedEOF
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var last replicationFollowProgress
	writer := followProgressWriter{receive: func(p replicationFollowProgress) {
		last = p
		if p.Follow.Reconnects == 1 {
			cancel()
		}
	}}
	if err := followReplica(ctx, directory, source, objectnode.CatchUpOptions{MaxEntries: 1, MaxBytes: 1}, true, writer); err != nil {
		t.Fatal(err)
	}
	if source.headCalls != 2 || source.waitCalls != 1 || last.EntriesApplied != 0 || last.BytesTransferred != 0 || last.Follow.Retries != 1 || last.Follow.Reconnects != 1 || last.Follow.ChangeWaits != 1 || last.Follow.LagRecords != 0 {
		t.Fatal("reconnect lost its position or telemetry", source.headCalls, source.waitCalls, last)
	}
}

func TestFollowReplicaStopsOnPermanentWaitErrors(t *testing.T) {
	for _, failure := range []error{objectnode.ErrReplicaReseed, objectnode.ErrReplicationFork, objectnode.ErrCorrupt, objectnode.ErrInvalid} {
		t.Run(failure.Error(), func(t *testing.T) {
			_, directory, httpSource := followFixture(t, false)
			source := &observedFollowSource{HTTPReplicationSource: httpSource}
			source.wait = func(context.Context, objectnode.ReplicationWaitCursor) (objectnode.ReplicationStatus, error) {
				return objectnode.ReplicationStatus{}, failure
			}
			if err := followReplica(t.Context(), directory, source, objectnode.CatchUpOptions{MaxEntries: 1, MaxBytes: 1}, true, io.Discard); !errors.Is(err, failure) {
				t.Fatal("permanent failure was retried", err)
			}
			if source.headCalls != 1 || source.waitCalls != 1 {
				t.Fatal("permanent failure was retried", source.headCalls, source.waitCalls)
			}
		})
	}
}

type failedProfileSource struct {
	*objectnode.HTTPReplicationSource
}

func (s failedProfileSource) Head(context.Context) (objectnode.ReplicationStatus, error) {
	return objectnode.ReplicationStatus{}, io.ErrUnexpectedEOF
}

func TestFollowReplicaEmitsFailedAttemptProfile(t *testing.T) {
	_, directory, source := followFixture(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var observed replicationFollowProgress
	writer := followProgressWriter{receive: func(p replicationFollowProgress) {
		observed = p
		cancel()
	}}
	if err := followReplica(ctx, directory, failedProfileSource{source}, objectnode.CatchUpOptions{MaxEntries: 1, MaxBytes: 1}, true, writer); err != nil {
		t.Fatal(err)
	}
	if observed.Error == "" || observed.ObservedAt.IsZero() || observed.Profile.SourceCheckMS <= 0 || observed.Profile.TotalMS < observed.Profile.SourceCheckMS || observed.EntriesApplied != 0 || observed.ConfirmedSequence != nil {
		t.Fatal("failed attempt was omitted or became proof", observed)
	}
}
