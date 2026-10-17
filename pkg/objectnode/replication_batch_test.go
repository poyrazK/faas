package objectnode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type batchTestSource struct {
	localReplicationSource
	change func([]JournalEntry) []JournalEntry
	calls  int
}

func (*batchTestSource) EntryBatchLimit() int { return api.ObjectNodeReplicationBatch }

func (s *batchTestSource) Entries(ctx context.Context, stream string, sequence int64, limit int) ([]JournalEntry, error) {
	s.calls++
	entries, err := s.store.ReplicationEntries(ctx, stream, sequence, limit)
	if err == nil && s.change != nil {
		entries = s.change(entries)
	}
	return entries, err
}

// ADR-1347: validate the entire fetched prefix before persisting any of it.
func TestReplicaBatchRejectsInvalidPrefetch(t *testing.T) {
	for _, kind := range []string{"empty", "extra", "gap", "reordered", "previous", "stream", "epoch", "hash"} {
		t.Run(kind, func(t *testing.T) {
			primary, directory := replicationPair(t)
			for n := range 3 {
				putText(t, primary, fmt.Sprint(n), "data")
			}
			follower, err := OpenFollower(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer follower.Close()
			source := &batchTestSource{localReplicationSource: localReplicationSource{primary}, change: func(entries []JournalEntry) []JournalEntry {
				switch kind {
				case "empty":
					return nil
				case "extra":
					return append(entries, entries[2])
				case "gap":
					return []JournalEntry{entries[0], entries[2]}
				case "reordered":
					entries[1], entries[2] = entries[2], entries[1]
				case "previous":
					entries[2].Previous = entries[0].Previous
					entries[2] = sealJournal(entries[2])
				case "stream":
					entries[2].StreamID = primary.replication.NodeID
					entries[2] = sealJournal(entries[2])
				case "epoch":
					entries[2].Epoch++
					entries[2] = sealJournal(entries[2])
				case "hash":
					entries[2].Hash = entries[0].Hash
				}
				return entries
			}}
			p, err := follower.CatchUp(t.Context(), source, pacedTestOptions())
			if !errors.Is(err, ErrReplicationFork) || p.Local.ReceivedSequence != 0 || p.Local.AppliedSequence != 0 || p.ConfirmedSequence != nil || source.calls != 1 {
				t.Fatal("invalid prefetch crossed a durable boundary", p, err)
			}
		})
	}
}

func TestReplicaBatchBoundsBytesAndElements(t *testing.T) {
	primary, directory := replicationPair(t)
	for n := range 8 {
		_, err := primary.Put(t.Context(), "test-bucket", fmt.Sprint(n), strings.NewReader("data"), 4, PutOptions{Headers: http.Header{"X-Test": {strings.Repeat("x", 768<<10)}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	head, err := primary.ReplicationStatus()
	if err != nil {
		t.Fatal(err)
	}
	page, err := primary.ReplicationEntries(t.Context(), head.StreamID, 1, api.ObjectNodeReplicationBatch)
	body, marshalErr := json.Marshal(page)
	if err != nil || marshalErr != nil || len(page) < 1 || len(page) >= 8 || len(body) > api.ObjectNodeReplicationBatchMaxBytes {
		t.Fatal("journal page escaped its byte bound", len(page), len(body), err, marshalErr)
	}
	for _, body := range [][]byte{[]byte("[" + strings.Repeat("{},", api.ObjectNodeReplicationBatch) + "{}]"), []byte("null"), []byte("[{}] "), []byte("[{}]{}"), []byte("[{\"unknown\":1}]")} {
		var entries []JournalEntry
		if err := decodeReplicationBatch(body, &entries); err == nil || len(entries) > api.ObjectNodeReplicationBatch {
			t.Fatal("batch decoder accepted excess/ambiguous data", string(body), err)
		}
	}
	follower, err := OpenFollower(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Close()
	source := &batchTestSource{localReplicationSource: localReplicationSource{primary}}
	p, err := follower.CatchUp(t.Context(), source, pacedTestOptions())
	if err != nil || !p.CaughtUp || p.EntriesApplied != 8 || source.calls < 2 {
		t.Fatal("short byte-bounded page did not continue", p, source.calls, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := primary.ReplicationEntries(ctx, head.StreamID, 1, 1); !errors.Is(err, context.Canceled) {
		t.Fatal("batch read ignored cancellation", err)
	}
}

// A prefetched page must never change the received-one-ahead invariant or
// confirm future records after a partial transfer, persistence error or restart.
func TestReplicaBatchCheckpointFailureAndRestart(t *testing.T) {
	for _, boundary := range []string{"before received", "after received", "before catalog", "after catalog", "before applied", "after applied"} {
		t.Run(boundary, func(t *testing.T) {
			primary, directory := durableReplicationPair(t)
			catchUpTestReplica(t, primary, directory)
			for _, key := range []string{"first", "second", "third"} {
				putText(t, primary, key, "data")
			}
			follower, err := OpenFollower(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = follower.Close() }()
			injected, fired := errors.New("injected batch checkpoint failure"), false
			persist := func(path string, value any, base func(string, any) (bool, error)) (bool, error) {
				stage := ""
				if state, ok := value.(replicationState); ok && state.ReceivedSequence == 2 {
					stage = "received"
					if state.AppliedSequence == 2 {
						stage = "applied"
					}
				}
				if path == filepath.Join(follower.store.objectPath("test-bucket", "second"), "current.json") {
					stage = "catalog"
				}
				if !fired && boundary == "before "+stage {
					fired = true
					return false, injected
				}
				published, err := base(path, value)
				if err == nil && !fired && boundary == "after "+stage {
					fired = true
					return published, injected
				}
				return published, err
			}
			follower.store.replicationPersist = func(path string, value any) (bool, error) { return persist(path, value, writeReplicationJSON) }
			follower.store.persist = func(path string, value any) (bool, error) { return persist(path, value, writeJSON) }
			source := &batchTestSource{localReplicationSource: localReplicationSource{primary}}
			options := pacedTestOptions()
			options.MaxBytes = 2
			p, err := follower.CatchUp(t.Context(), source, options)
			if err != nil || p.Local.ReceivedSequence != 1 || p.Local.AppliedSequence != 0 || p.ConfirmedSequence == nil || *p.ConfirmedSequence != 0 {
				t.Fatal("partial prefetched page confirmed future bytes", p, err)
			}
			p, err = follower.CatchUp(t.Context(), source, pacedTestOptions())
			if !errors.Is(err, injected) || !fired || p.ConfirmedSequence != nil || p.Local.ReceivedSequence > p.Local.AppliedSequence+1 {
				t.Fatal("prefetch bypassed a failed checkpoint", p, err)
			}
			requireReplicaPending(t, primary)
			if err := follower.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenFollower(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			p, err = reopened.CatchUp(t.Context(), source, pacedTestOptions())
			if err != nil || !p.CaughtUp || p.Local.AppliedSequence != 3 || p.ConfirmedSequence == nil || *p.ConfirmedSequence != 3 || followerText(t, reopened.store, "third") != "data" {
				t.Fatal("batch restart lost its contiguous durable prefix", p, err)
			}
			if err = primary.WaitForReplica(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReplicaBatchEntryTokensSurviveSteps(t *testing.T) {
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
	source := &batchTestSource{localReplicationSource: localReplicationSource{primary}}
	o := pacedTestOptions()
	o.WorkEntriesPerSecond = 1
	p, err := follower.CatchUp(t.Context(), source, o)
	if err != nil || p.EntriesApplied != api.ObjectNodeReplicationBatch || source.calls != 1 {
		t.Fatal(p, source.calls, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	p, err = follower.CatchUp(ctx, source, o)
	if err == nil || p.EntriesApplied != 0 || source.calls != 1 || p.Profile.EntryWaitMS <= 0 {
		t.Fatal("entry page bypassed persistent start budget", p, source.calls, err)
	}
	var decoded []JournalEntry
	if err := decodeReplicationBatch(bytes.Repeat([]byte(" "), 8), &decoded); err == nil {
		t.Fatal("non-array page accepted")
	}
}
