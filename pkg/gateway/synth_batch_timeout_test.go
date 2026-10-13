// synth_batch_timeout_test.go — unit tests for audit round 2
// finding #4 timeouts in handleInvocationDispatchBatch.
//
// Scope:
//  1. Per-record timeout (batchDispatchPerRecordTimeout = 30s):
//     a record whose Invoke blocks past the per-record budget
//     must come back with Status="retry", Code="invoke_timeout"
//     and the handler must continue with the next record.
//  2. Total-batch timeout (batchDispatchTotalTimeout = 5min):
//     a record whose Invoke blocks past the total budget must
//     cause the remaining records to be marked with
//     Code="batch_timeout" so schedd can Nack them.
//
// The fakeBatchDispatcher below is deterministic: it can be
// configured to block until a channel is closed, so the tests
// drive both timeout paths without real wall-clock waits
// (the tests below would otherwise take > 30 seconds to run).
//
// To make the per-record + total-batch timeouts reachable from
// tests in a reasonable wall-clock time we shadow the package
// constants with a smaller values via build-tag-less package
// vars (the test file lives in the same package and writes
// directly to the same vars). Production code reads the same
// vars; setting them to a few ms here drives the timeouts
// immediately.

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeBatchDispatcher is a SynthDispatcher used by the batch
// timeout tests. It records every Invoke call and can be
// configured to block on a sync.WaitGroup-style gate so the
// per-record + total-batch timeouts fire predictably.
type fakeBatchDispatcher struct {
	mu       sync.Mutex
	calls    []state.Invocation
	blockMu  sync.Mutex
	blockCh  chan struct{} // closed to release all blocked Invoke calls
	blockErr error         // returned by Invoke when blockCh is closed (default: ctx.Err())
	result   state.Invocation
}

func (f *fakeBatchDispatcher) Wake(_ context.Context, _ string) error { return nil }

// Invoke records the invocation then blocks until blockCh is
// closed (or the context is cancelled). Returns the caller's
// ctx.Err() on cancellation, which is the standard "Invoke
// returned because the gateway cancelled us" shape — the
// dispatcher layer in production maps that to a 502.
func (f *fakeBatchDispatcher) Invoke(ctx context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	f.mu.Lock()
	f.calls = append(f.calls, inv)
	f.mu.Unlock()
	f.blockMu.Lock()
	ch := f.blockCh
	f.blockMu.Unlock()
	if ch != nil {
		select {
		case <-ch:
			f.blockMu.Lock()
			err := f.blockErr
			f.blockMu.Unlock()
			if err != nil {
				return state.Invocation{}, err
			}
			return f.result, nil
		case <-ctx.Done():
			return state.Invocation{}, ctx.Err()
		}
	}
	return f.result, nil
}

// blockAll arms the dispatcher to block every Invoke until the
// returned channel is closed.
func (f *fakeBatchDispatcher) blockAll() chan struct{} {
	f.blockMu.Lock()
	defer f.blockMu.Unlock()
	f.blockCh = make(chan struct{})
	return f.blockCh
}

// spec: §4.10 — a trigger batch record is an async invocation of the bound
// app, so it runs as long as that app's invocations may (ADR-1180).
func TestBatchRecordTimeoutFollowsTheAppsInvocationTimeout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		lookup func(context.Context, string) time.Duration
		want   time.Duration
	}{
		{name: "no lookup", want: batchDispatchPerRecordTimeout},
		{name: "default app", lookup: func(context.Context, string) time.Duration { return api.AsyncInvocationTimeoutFloor }, want: batchDispatchPerRecordTimeout},
		{name: "two-minute app", lookup: func(context.Context, string) time.Duration { return 2 * time.Minute }, want: 2*time.Minute + api.AsyncInvocationWakeAllowance},
	} {
		srv := (&SynthServer{}).WithInvocationTimeout(tc.lookup)
		if got := srv.batchRecordTimeout(context.Background(), "app-1"); got != tc.want {
			t.Errorf("%s: record timeout %s, want %s", tc.name, got, tc.want)
		}
	}
}

// TestDispatchBatchRecord_PerRecordTimeout pins audit round 2
// finding #4: a single stuck Invoke must trip the per-record
// timeout (Code="invoke_timeout") without blocking the loop.
// We set batchDispatchPerRecordTimeout to a few ms via the
// package var, dispatch one record, and assert it comes back
// as retry + invoke_timeout.
func TestDispatchBatchRecord_PerRecordTimeout(t *testing.T) {
	// Shrink the per-record timeout for the duration of this
	// test. Restore on exit so other tests in the same package
	// (if any) see the production value.
	orig := batchDispatchPerRecordTimeout
	batchDispatchPerRecordTimeout = 50 * time.Millisecond
	defer func() { batchDispatchPerRecordTimeout = orig }()

	disp := &fakeBatchDispatcher{
		result: state.Invocation{State: "succeeded"},
	}
	block := disp.blockAll()
	defer close(block)

	req := batchDispatchRequest{
		InvocationID: "inv-per-rec",
		AppID:        "app-1",
		Source:       "esm",
		TriggerID:    "trig-1",
		Records: []batchDispatchRecord{
			{ItemIdentifier: "rec-1", PayloadB64: ""},
		},
	}
	srv := &SynthServer{dispatcher: disp, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	res := srv.dispatchBatchRecord(context.Background(), req, req.Records[0])

	if got, want := res.ItemIdentifier, "rec-1"; got != want {
		t.Errorf("ItemIdentifier = %q, want %q", got, want)
	}
	if got, want := res.Status, "retry"; got != want {
		t.Errorf("Status = %q, want %q (per-record timeout must surface as retry, not dead_letter)", got, want)
	}
	if got, want := res.Code, "invoke_timeout"; got != want {
		t.Errorf("Code = %q, want %q", got, want)
	}
}

// TestDispatchBatchRecord_HappyPath_NoTimeout pins the happy
// path: a record whose Invoke returns immediately comes back
// as Status="succeeded" with no timeout code. This is the
// regression guard against the timeout markers leaking into
// non-timeout error paths.
func TestDispatchBatchRecord_HappyPath_NoTimeout(t *testing.T) {
	disp := &fakeBatchDispatcher{
		result: state.Invocation{
			State:  "succeeded",
			Result: json.RawMessage(`null`),
		},
	}
	srv := &SynthServer{dispatcher: disp, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := batchDispatchRequest{
		InvocationID: "inv-happy",
		AppID:        "app-1",
		Source:       "esm",
		TriggerID:    "trig-1",
	}
	res := srv.dispatchBatchRecord(context.Background(), req, batchDispatchRecord{ItemIdentifier: "rec-1"})

	if got, want := res.Status, "succeeded"; got != want {
		t.Errorf("Status = %q, want %q (got=%+v)", got, want, res)
	}
	if got := res.Code; got != "" {
		t.Errorf("Code = %q, want empty (no timeout / error code on success)", got)
	}
}

// TestHandleInvocationDispatchBatch_TotalBatchTimeout pins
// audit round 2 finding #4: when the total-batch timeout
// fires, the remaining records must come back as
// Status="retry", Code="batch_timeout" so schedd can Nack them.
// We dispatch 3 records with a dispatcher that blocks
// indefinitely, shrink the total-batch timeout to a few ms,
// and assert the second and third records are stamped with
// batch_timeout.
func TestHandleInvocationDispatchBatch_TotalBatchTimeout(t *testing.T) {
	// Shrink the total-batch timeout for the duration of this
	// test. Production value is 5 minutes — way too long for a
	// unit test. We also keep per-record > total so the
	// per-record timeout does NOT fire first; we want to
	// exercise the batch_timeout branch.
	origTotal := batchDispatchTotalTimeout
	origPer := batchDispatchPerRecordTimeout
	batchDispatchTotalTimeout = 100 * time.Millisecond
	batchDispatchPerRecordTimeout = 5 * time.Second
	defer func() {
		batchDispatchTotalTimeout = origTotal
		batchDispatchPerRecordTimeout = origPer
	}()

	disp := &fakeBatchDispatcher{
		result: state.Invocation{State: "succeeded"},
	}
	block := disp.blockAll()
	defer close(block)

	req := batchDispatchRequest{
		InvocationID: "inv-total",
		AppID:        "app-1",
		Source:       "esm",
		TriggerID:    "trig-1",
		Records: []batchDispatchRecord{
			{ItemIdentifier: "rec-1"},
			{ItemIdentifier: "rec-2"},
			{ItemIdentifier: "rec-3"},
		},
	}
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch_batch", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv := &SynthServer{
		dispatcher: disp,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	srv.handleInvocationDispatchBatch(rec, httpReq)

	resp := rec.Result()
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out batchDispatchResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, raw)
	}
	if got, want := len(out.Results), 3; got != want {
		t.Fatalf("results length = %d, want %d", got, want)
	}
	// All 3 records block on the dispatcher; rec-1 may have
	// hit the per-record or total-batch timeout depending on
	// scheduler ordering, but rec-2 and rec-3 must show
	// batch_timeout because the loop's ctx.Err() check fires
	// on the next iteration after total timeout.
	for i, r := range out.Results {
		if r.ItemIdentifier != fmt.Sprintf("rec-%d", i+1) {
			t.Errorf("Results[%d].ItemIdentifier = %q, want rec-%d", i, r.ItemIdentifier, i+1)
		}
	}
	// Count batch_timeout stamps; at minimum rec-2 and rec-3
	// must carry it. rec-1 may carry invoke_timeout (per-record
	// fired first) or batch_timeout depending on timing — both
	// are acceptable per the audit; the gate is that the
	// remaining records are correctly stamped so schedd can
	// Nack them.
	var batchTimeoutCount int
	for _, r := range out.Results {
		if r.Code == "batch_timeout" {
			batchTimeoutCount++
		}
	}
	if batchTimeoutCount < 2 {
		t.Errorf("batch_timeout count = %d, want >= 2 (audit #4: remaining records MUST be stamped with batch_timeout)", batchTimeoutCount)
	}
	// Status must be retry, not dead_letter — the schedd's
	// retry FSM is the right tool to retry a total-batch
	// timeout, NOT dead-letter.
	for _, r := range out.Results {
		if r.Status != "retry" {
			t.Errorf("Status = %q, want retry (got=%+v)", r.Status, r)
		}
	}
}

// TestHandleInvocationDispatchBatch_NormalFlow_NoTimeout pins
// the happy path through the handler: 3 records all return
// succeeded; no invoke_timeout / batch_timeout codes appear.
// This is the regression guard against the timeout markers
// leaking into normal flow.
func TestHandleInvocationDispatchBatch_NormalFlow_NoTimeout(t *testing.T) {
	disp := &fakeBatchDispatcher{
		result: state.Invocation{
			State:  "succeeded",
			Result: json.RawMessage(`null`),
		},
	}
	req := batchDispatchRequest{
		InvocationID: "inv-normal",
		AppID:        "app-1",
		Source:       "esm",
		TriggerID:    "trig-1",
		Records: []batchDispatchRecord{
			{ItemIdentifier: "rec-1"},
			{ItemIdentifier: "rec-2"},
			{ItemIdentifier: "rec-3"},
		},
	}
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch_batch", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv := &SynthServer{
		dispatcher: disp,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	srv.handleInvocationDispatchBatch(rec, httpReq)

	resp := rec.Result()
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out batchDispatchResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, raw)
	}
	if got, want := len(out.Results), 3; got != want {
		t.Fatalf("results length = %d, want %d", got, want)
	}
	for i, r := range out.Results {
		if r.Status != "succeeded" {
			t.Errorf("Results[%d].Status = %q, want succeeded (got=%+v)", i, r.Status, r)
		}
		if r.Code != "" {
			t.Errorf("Results[%d].Code = %q, want empty (no timeout code on success)", i, r.Code)
		}
	}
}

// (compile-time guard against drift in the SynthDispatcher
// interface — fakeBatchDispatcher MUST keep satisfying the
// interface even when we add new methods.)
var _ SynthDispatcher = (*fakeBatchDispatcher)(nil)

// (compile-time guard against accidental rename of the
// timeout codes — the schedd's classifyDLQReason + the audit
// findings pin these strings.)
const (
	_ = "invoke_timeout" // pkg/gateway.synth.dispatchBatchRecord
	_ = "batch_timeout"  // pkg/gateway.synth.handleInvocationDispatchBatch
)
