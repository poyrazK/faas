package objectnode

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func pacedTestOptions() CatchUpOptions {
	return CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes, WorkBytesPerSecond: api.ObjectNodeReplicationDefaultWorkBytes, WorkEntriesPerSecond: api.ObjectNodeReplicationDefaultWorkEntries}
}

func TestReplicaWorkBudgetAccountsForRetainedPayloadReads(t *testing.T) {
	primary, directory := replicationPair(t)
	for _, key := range []string{"first", "second"} {
		if _, err := primary.Put(t.Context(), "test-bucket", key, strings.NewReader("shared payload"), 14, PutOptions{Receipt: uuid.NewString()}); err != nil {
			t.Fatal(err)
		}
	}
	follower, err := OpenFollower(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Close()
	options := pacedTestOptions()
	options.MaxEntries = 3
	first, err := follower.CatchUp(t.Context(), localReplicationSource{primary}, options)
	if err != nil || first.CaughtUp || first.BytesTransferred != 14 || first.WorkBytes <= 2*first.BytesTransferred {
		t.Fatal("local replay reads escaped byte accounting", first, err)
	}
	second, err := follower.CatchUp(t.Context(), localReplicationSource{primary}, options)
	if err != nil || !second.CaughtUp || second.BytesTransferred != 0 || second.WorkBytes < 14 {
		t.Fatal("a retained payload escaped pacing", second, err)
	}
}

func TestReplicaWorkCancellationRetainsPartialDataWithoutConfirmation(t *testing.T) {
	primary, directory := durableReplicationPair(t)
	catchUpTestReplica(t, primary, directory)
	body := strings.Repeat("x", int(api.ObjectNodeReplicationChunkBytes)+1)
	if _, err := receiptPut(t, primary, "key", body, uuid.NewString(), PutOptions{}); err != nil {
		t.Fatal(err)
	}
	follower, err := OpenFollower(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Close()
	options := pacedTestOptions()
	options.WorkBytesPerSecond = 1
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	partial, err := follower.CatchUp(ctx, localReplicationSource{primary}, options)
	if err == nil || partial.Local.AppliedSequence != 0 || partial.Local.ReceivedSequence != 1 || partial.BytesTransferred != api.ObjectNodeReplicationChunkBytes {
		t.Fatal("pacing did not stop at a resumable boundary", partial, err)
	}
	requireReplicaPending(t, primary)
	options = pacedTestOptions()
	final, err := follower.CatchUp(t.Context(), localReplicationSource{primary}, options)
	if err != nil || !final.CaughtUp || final.BytesTransferred != 1 || final.ConfirmedSequence == nil || *final.ConfirmedSequence != final.Local.AppliedSequence {
		t.Fatal("paced work failed to resume its durable prefix", final, err)
	}
	if err = primary.WaitForReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestReplicaEntryBudgetSurvivesBoundedSteps(t *testing.T) {
	primary, directory := replicationPair(t)
	for n := range api.ObjectNodeReplicationBatch + 1 {
		if err := primary.CreateBucket(fmt.Sprintf("bucket-%03d", n)); err != nil {
			t.Fatal(err)
		}
	}
	follower, err := OpenFollower(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Close()
	options := pacedTestOptions()
	options.WorkEntriesPerSecond = 1
	first, err := follower.CatchUp(t.Context(), localReplicationSource{primary}, options)
	if err != nil || first.CaughtUp || first.EntriesApplied != api.ObjectNodeReplicationBatch {
		t.Fatal(first, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	second, err := follower.CatchUp(ctx, localReplicationSource{primary}, options)
	if err == nil || second.Local.AppliedSequence != first.Local.AppliedSequence || second.EntriesApplied != 0 {
		t.Fatal("entry tokens reset between bounded steps", second, err)
	}
	if second.Profile.EntryWaitMS <= 0 || second.Profile.ByteWaitMS != 0 || second.WorkWaitMS != second.Profile.EntryWaitMS {
		t.Fatal("entry pacing was attributed to byte work", second)
	}
	options.WorkEntriesPerSecond = api.ObjectNodeReplicationDefaultWorkEntries
	final, err := follower.CatchUp(t.Context(), localReplicationSource{primary}, options)
	if err != nil || !final.CaughtUp || final.EntriesApplied != 1 {
		t.Fatal(final, err)
	}
}

func TestReplicaWorkOptionsRejectInvalidRates(t *testing.T) {
	_, directory := replicationPair(t)
	follower, err := OpenFollower(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Close()
	for _, field := range []string{"bytes", "entries", "unpaired", "negative"} {
		options := pacedTestOptions()
		switch field {
		case "bytes":
			options.WorkBytesPerSecond = api.ObjectNodeReplicationMaxWorkBytes + 1
		case "entries":
			options.WorkEntriesPerSecond = api.ObjectNodeReplicationMaxWorkEntries + 1
		case "unpaired":
			options.WorkEntriesPerSecond = 0
		case "negative":
			options.WorkBytesPerSecond = -1
		}
		if _, err := follower.CatchUp(t.Context(), localReplicationSource{}, options); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid work rate reached the source", field, err)
		}
	}
}
