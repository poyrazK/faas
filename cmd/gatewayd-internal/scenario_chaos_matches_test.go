package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeChaosMatchWriter commits a call's whole batch or none of it, like the
// Postgres store. fail lists, per call, the error to return (nil commits).
type fakeChaosMatchWriter struct {
	calls     int
	fail      []error
	committed map[string]int64
}

func (w *fakeChaosMatchWriter) RecordScenarioTestChaosInjections(_ context.Context, batch []chaos.InjectionBatch) error {
	call := w.calls
	w.calls++
	if call < len(w.fail) && w.fail[call] != nil {
		return w.fail[call]
	}
	for _, injection := range batch {
		w.committed[injection.RuleID] += injection.Count
	}
	return nil
}

func testChaosMatchRecorder(writer *fakeChaosMatchWriter) *scenarioChaosMatchRecorder {
	return &scenarioChaosMatchRecorder{
		store: writer, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		pending: make(map[scenarioChaosMatchKey]int64),
	}
}

func observeChaosMatches(recorder *scenarioChaosMatchRecorder, keys int, perKey int) {
	for i := 0; i < keys; i++ {
		for n := 0; n < perKey; n++ {
			recorder.observeID(strings.Repeat("a", 32), "caller", "generation", fmt.Sprintf("rule-%06d", i))
		}
	}
}

func TestScenarioChaosMatchRecorderRetryCountsEachMatchOnce(t *testing.T) {
	keys := 2*api.ScenarioChaosMatchWriteBatch + 10
	writer := &fakeChaosMatchWriter{fail: []error{nil, context.DeadlineExceeded}, committed: map[string]int64{}}
	recorder := testChaosMatchRecorder(writer)
	observeChaosMatches(recorder, keys, 3)

	recorder.flush(context.Background())
	if writer.calls != 2 || len(writer.committed) != api.ScenarioChaosMatchWriteBatch {
		t.Fatalf("first flush: calls=%d committed=%d, want 2 calls and one committed chunk", writer.calls, len(writer.committed))
	}
	if len(recorder.pending) != keys-api.ScenarioChaosMatchWriteBatch {
		t.Fatalf("requeued %d counters, want only the %d unwritten ones", len(recorder.pending), keys-api.ScenarioChaosMatchWriteBatch)
	}
	recorder.flush(context.Background())
	if len(recorder.pending) != 0 || len(writer.committed) != keys {
		t.Fatalf("retry left pending=%d committed=%d, want 0 and %d", len(recorder.pending), len(writer.committed), keys)
	}
	for rule, count := range writer.committed {
		if count != 3 {
			t.Fatalf("%s persisted %d matches, want 3", rule, count)
		}
	}
}

func TestScenarioChaosMatchRecorderCapsPendingCounters(t *testing.T) {
	writer := &fakeChaosMatchWriter{committed: map[string]int64{}}
	recorder := testChaosMatchRecorder(writer)
	observeChaosMatches(recorder, api.ScenarioChaosMatchMaxPendingKeys+10, 1)
	if len(recorder.pending) != api.ScenarioChaosMatchMaxPendingKeys || recorder.dropped != 10 {
		t.Fatalf("pending=%d dropped=%d, want %d and 10", len(recorder.pending), recorder.dropped, api.ScenarioChaosMatchMaxPendingKeys)
	}
	// Counters already held keep counting at the cap.
	recorder.observeID(strings.Repeat("a", 32), "caller", "generation", fmt.Sprintf("rule-%06d", 0))
	if recorder.pending[scenarioChaosMatchKey{runID: strings.Repeat("a", 32), callerAppID: "caller", generation: "generation", ruleID: "rule-000000"}] != 2 {
		t.Fatal("existing counter stopped counting at the cap")
	}
	recorder.flush(context.Background())
	if recorder.dropped != 0 || len(recorder.pending) != 0 || len(writer.committed) != api.ScenarioChaosMatchMaxPendingKeys {
		t.Fatalf("flush: dropped=%d pending=%d committed=%d", recorder.dropped, len(recorder.pending), len(writer.committed))
	}
}

func TestScenarioChaosMatchRecorderDiscardsInvalidChunk(t *testing.T) {
	keys := api.ScenarioChaosMatchWriteBatch + 5
	writer := &fakeChaosMatchWriter{fail: []error{fmt.Errorf("validate: %w", state.ErrConflict)}, committed: map[string]int64{}}
	recorder := testChaosMatchRecorder(writer)
	observeChaosMatches(recorder, keys, 1)
	recorder.flush(context.Background())
	if writer.calls != 2 || len(writer.committed) != 5 || len(recorder.pending) != 0 {
		t.Fatalf("calls=%d committed=%d pending=%d, want the invalid chunk discarded and the rest written", writer.calls, len(writer.committed), len(recorder.pending))
	}
}
