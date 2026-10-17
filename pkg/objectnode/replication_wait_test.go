package objectnode

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type enteredReplicationWait struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *enteredReplicationWait) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

type replicationWaitResult struct {
	status ReplicationStatus
	err    error
}

func startReplicationWait(t *testing.T, primary *Store, cursor ReplicationWaitCursor) <-chan replicationWaitResult {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	observed := &enteredReplicationWait{Context: ctx, entered: make(chan struct{})}
	done := make(chan replicationWaitResult, 1)
	go func() {
		s, err := primary.WaitForReplicationChange(observed, cursor)
		done <- replicationWaitResult{s, err}
	}()
	select {
	case <-observed.entered:
	case result := <-done:
		t.Fatal("wait returned before becoming idle", result)
	case <-time.After(5 * time.Second):
		t.Fatal("wait never released its snapshot locks")
	}
	return done
}

func requireReplicationWake(t *testing.T, done <-chan replicationWaitResult) replicationWaitResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("replication wait was not released")
		return replicationWaitResult{}
	}
}

func waitCursorAtHead(t *testing.T, primary *Store) ReplicationWaitCursor {
	t.Helper()
	head, err := primary.ReplicationStatus()
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := NewReplicationWaitCursor(head)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

func TestReplicationChangeIgnoresReportsAndWakesForAppliedData(t *testing.T) {
	primary, follower := durableReplicationPair(t)
	primary = registerTestFollower(t, primary, follower)
	putText(t, primary, "first", "pending")
	cursor := waitCursorAtHead(t, primary)
	done := startReplicationWait(t, primary, cursor)
	catchUpTestReplica(t, primary, follower)
	// Both a retention report and a durability confirmation changed after the
	// sampled head. Repeated confirmations also broadcast, without new data.
	for range 3 {
		if _, err := primary.ConfirmReplica(t.Context(), confirmationAt(t, primary, 1)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case result := <-done:
		t.Fatal("acknowledgements woke their own follower", result)
	case <-time.After(30 * time.Millisecond):
	}
	putText(t, primary, "second", "new data")
	result := requireReplicationWake(t, done)
	if result.err != nil || result.status.AppliedSequence != 2 || result.status.Durability.ConfirmedSequence != 1 {
		t.Fatal("notification claimed confirmation or missed publication", result)
	}
	requireReplicaPending(t, primary)
	catchUpTestReplica(t, primary, follower)
	if err := primary.WaitForReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Publication between CatchUp and the wait request must not be lost.
	head, err := primary.WaitForReplicationChange(t.Context(), cursor)
	if err != nil || head.AppliedSequence != 2 {
		t.Fatal("lost a change which preceded the wait request", head, err)
	}
}

func TestReplicationChangeUncertainCheckpointNeverNotifiesSuccess(t *testing.T) {
	for _, boundary := range []string{"before", "after"} {
		t.Run(boundary, func(t *testing.T) {
			primary, follower := replicationPair(t)
			cursor := waitCursorAtHead(t, primary)
			injected := errors.New("lost applied-checkpoint acknowledgement")
			primary.replicationPersist = func(path string, value any) (bool, error) {
				if state, ok := value.(replicationState); ok && state.AppliedSequence == 1 {
					if boundary == "before" {
						return false, injected
					}
					published, err := writeReplicationJSON(path, value)
					if err != nil {
						return published, err
					}
					return published, injected
				}
				return writeReplicationJSON(path, value)
			}
			done := startReplicationWait(t, primary, cursor)
			if _, err := primary.Put(t.Context(), "test-bucket", "key", strings.NewReader(""), 0, PutOptions{}); !errors.Is(err, injected) {
				t.Fatal(err)
			}
			if result := requireReplicationWake(t, done); !errors.Is(result.err, ErrCorrupt) {
				t.Fatal("uncertain persistence produced a successful change", result)
			}
			if err := primary.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := reopenCompactionPrimary(t, primary.root)
			head, err := reopened.WaitForReplicationChange(t.Context(), cursor)
			if err != nil || head.AppliedSequence != 1 {
				t.Fatal("restart did not recover the durable decision", head, err)
			}
			catchUpTestReplica(t, reopened, follower)
		})
	}
}

func TestReplicationChangeWaitReleasesLocksForFenceAndClose(t *testing.T) {
	for _, operation := range []string{"fence", "close"} {
		t.Run(operation, func(t *testing.T) {
			primary, _ := replicationPair(t)
			cursor := waitCursorAtHead(t, primary)
			done := startReplicationWait(t, primary, cursor)
			mutation := make(chan error, 1)
			go func() {
				if operation == "close" {
					mutation <- primary.Close()
					return
				}
				_, err := primary.FencePrimary(t.Context(), FenceRequest{StreamID: cursor.StreamID, PrimaryID: cursor.PrimaryID, Epoch: cursor.Epoch, TargetID: uuid.NewString()})
				mutation <- err
			}()
			select {
			case err := <-mutation:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("idle wait held the lifecycle lock")
			}
			result := requireReplicationWake(t, done)
			if operation == "close" {
				if !errors.Is(result.err, ErrClosed) {
					t.Fatal(result)
				}
			} else if result.err != nil || result.status.Role != "fenced" || result.status.Handoff == nil || result.status.AppliedSequence != cursor.Sequence {
				t.Fatal("fence without a new sequence did not wake the waiter", result)
			}
		})
	}
}

func TestReplicationChangeRejectsInvalidAuthorityPositionAndRetiredHistory(t *testing.T) {
	primary, _ := replicationPair(t)
	cursor := waitCursorAtHead(t, primary)
	for _, field := range []string{"stream", "primary", "epoch", "future", "hash", "negative", "revision"} {
		t.Run(field, func(t *testing.T) {
			bad := cursor
			want := ErrReplicationFork
			switch field {
			case "stream":
				bad.StreamID = uuid.NewString()
			case "primary":
				bad.PrimaryID = uuid.NewString()
			case "epoch":
				bad.Epoch++
			case "future":
				bad.Sequence++
			case "hash":
				bad.Hash = nameHash("wrong hash")
			case "negative":
				bad.Sequence, want = -1, ErrInvalid
			case "revision":
				bad.Revision, want = "invalid", ErrInvalid
			}
			if _, err := primary.WaitForReplicationChange(t.Context(), bad); !errors.Is(err, want) {
				t.Fatal(err)
			}
		})
	}
	putText(t, primary, "key", "retired prefix")
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := CompactJournal(t.Context(), primary.root, -1); err != nil {
		t.Fatal(err)
	}
	reopened := reopenCompactionPrimary(t, primary.root)
	if _, err := reopened.WaitForReplicationChange(t.Context(), cursor); !errors.Is(err, ErrReplicaReseed) {
		t.Fatal("wait silently crossed a compacted floor", err)
	}
}

func TestReplicationChangeWaitHasFixedServerBound(t *testing.T) {
	primary, follower := durableReplicationPair(t)
	catchUpTestReplica(t, primary, follower)
	cursor := waitCursorAtHead(t, primary)
	ctx, cancel := context.WithTimeout(t.Context(), api.ObjectNodeReplicationChangeWaitTimeout+5*time.Second)
	defer cancel()
	reports, stop := context.WithCancel(ctx)
	defer stop()
	confirmation := confirmationAt(t, primary, 0)
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-reports.Done():
				done <- nil
				return
			case <-ticker.C:
				if _, err := primary.ConfirmReplica(reports, confirmation); err != nil {
					done <- err
					return
				}
			}
		}
	}()
	started := time.Now()
	head, err := primary.WaitForReplicationChange(ctx, cursor)
	stop()
	if reportErr := <-done; reportErr != nil && !errors.Is(reportErr, context.Canceled) {
		t.Fatal(reportErr)
	}
	if err != nil || replicationWaitCursor(head) != cursor || time.Since(started) < api.ObjectNodeReplicationChangeWaitTimeout {
		t.Fatal("report broadcasts reset the idle timeout or changed the applied head", head, err, time.Since(started))
	}
	if api.ObjectNodeReplicationChangeWaitTimeout >= api.ObjectNodeReplicationRequestTimeout {
		t.Fatal("server wait leaves no time for the HTTP response")
	}
}

func TestReplicationChangePolicyAndRegistrationWithoutNewData(t *testing.T) {
	primary, follower := durableReplicationPair(t)
	cursor := waitCursorAtHead(t, primary)
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	replacement := uuid.NewString()
	if _, err := ConfigureDurability(t.Context(), primary.root, DurabilityReplica, replacement); err != nil {
		t.Fatal(err)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	head, err := primary.WaitForReplicationChange(t.Context(), cursor)
	if err != nil || head.AppliedSequence != cursor.Sequence || head.Durability.ReplicaID != replacement {
		t.Fatal("replacement policy without new data did not wake the worker", head, err)
	}
	cursor = replicationWaitCursor(head)
	primary = registerTestFollower(t, primary, follower)
	head, err = primary.WaitForReplicationChange(t.Context(), cursor)
	if err != nil || head.AppliedSequence != cursor.Sequence || len(head.Followers) != 1 {
		t.Fatal("new retention registration without new data was missed", head, err)
	}
}
