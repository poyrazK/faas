// request_telemetry_test.go — table-driven tests for the recorder +
// publisher (ADR-127).
//
// Covers:
//   - RecordFromObserve kill-switch pass-through when Enabled=false
//   - Ringbuffer FIFO order under non-overflowing load
//   - Ringbuffer overflow → oldest row overwritten
//   - Concurrent enqueue preserves all rows
//   - DrainBatch nil-when-empty + zero-max guards
//   - Publisher ship-success increments shippedTotal
//   - Publisher ship-error retries with backoff then drops, increments droppedTotal
//   - Publisher Wake() drains immediately (no flush-interval wait)
//   - Publisher Stop drains the final batch synchronously
//   - Publisher nil ship drops rows + counts them
//   - collapseRequestTelemetry aggregation and count preservation
//
// The Handler.observe → Recorder.RecordFromObserve path is
// covered end-to-end by handler_request_telemetry_test.go
// (TestHandlerObserveEnqueuesRow + TestHandlerObserveDropsPrePicker).
// This file exercises the recorder + publisher in isolation so the
// ringbuffer / goroutine state machine is independently testable.
//
// Tests live in package gateway so they can exercise the
// unexported ring buffer + drain plumbing directly.

package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// nopLog silences the slog logger for tests so test output stays clean.
func nopLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// makeRow builds a RequestTelemetryRow with sensible defaults; tests
// override individual fields.
func makeRow() RequestTelemetryRow {
	return RequestTelemetryRow{
		AccountID:    uuid.New(),
		AppID:        uuid.New(),
		DeploymentID: uuid.New(),
		Route:        "GET /v1/checkout",
		Method:       "GET",
		Status:       200,
		LatencyMS:    42,
		ColdBoot:     false,
		ReceivedAt:   time.Now(),
	}
}

// --- recorder tests ---

func TestRequestTelemetryRecorder_RecordFromObserve_DisabledIsNoOp(t *testing.T) {
	// Kill-switch path: when Enabled=false, RecordFromObserve must
	// not enqueue. Mirrors the FAAS_REQUEST_TELEMETRY_ENABLED=false
	// boot posture.
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: false}, nopLog())
	rec.RecordFromObserve(makeRow())
	if rec.PendingCount() != 0 {
		t.Fatalf("expected 0 pending when disabled, got %d", rec.PendingCount())
	}
}

func TestRequestTelemetryRecorder_RingFIFOOrder(t *testing.T) {
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 8}, nopLog())
	for i := 0; i < 5; i++ {
		row := makeRow()
		row.LatencyMS = i // encode ordinal in latency for assertion
		rec.RecordFromObserve(row)
	}
	if rec.PendingCount() != 5 {
		t.Fatalf("expected 5 pending, got %d", rec.PendingCount())
	}
	batch := rec.DrainBatch(8)
	if len(batch) != 5 {
		t.Fatalf("expected batch of 5, got %d", len(batch))
	}
	for i, row := range batch {
		if row.LatencyMS != i {
			t.Errorf("batch[%d]: expected latency %d, got %d", i, i, row.LatencyMS)
		}
	}
	if rec.PendingCount() != 0 {
		t.Fatalf("expected 0 pending after drain, got %d", rec.PendingCount())
	}
}

func TestRequestTelemetryRecorder_RingOverflowOverwritesOldest(t *testing.T) {
	var overwriteCallbacks atomic.Int64
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{
		Enabled:     true,
		RingSize:    4,
		OnOverwrite: func() { overwriteCallbacks.Add(1) },
	}, nopLog())
	// Fill with latency 0..3 (4 rows == capacity).
	for i := 0; i < 4; i++ {
		row := makeRow()
		row.LatencyMS = i
		rec.RecordFromObserve(row)
	}
	// Overflow: 3 more rows (4..6) push out the oldest.
	for i := 4; i < 7; i++ {
		row := makeRow()
		row.LatencyMS = i
		rec.RecordFromObserve(row)
	}
	batch := rec.DrainBatch(8)
	if len(batch) != 4 {
		t.Fatalf("expected batch of 4 after overflow, got %d", len(batch))
	}
	// After overflow: latencies 3,4,5,6 (oldest 0,1,2 overwritten).
	want := []int{3, 4, 5, 6}
	for i, row := range batch {
		if row.LatencyMS != want[i] {
			t.Errorf("batch[%d]: expected latency %d, got %d", i, want[i], row.LatencyMS)
		}
	}
	if got, want := rec.OverwrittenTotal(), int64(3); got != want {
		t.Errorf("OverwrittenTotal = %d, want %d", got, want)
	}
	if got, want := overwriteCallbacks.Load(), int64(3); got != want {
		t.Errorf("overwrite callback count = %d, want %d", got, want)
	}
}

func TestRequestTelemetryRecorder_ConcurrentEnqueuePreservesAllRows(t *testing.T) {
	// Stress: 100 goroutines × 50 enqueues = 5000 rows. Ring is
	// sized larger than the workload so all rows should survive
	// (the drain only fires from the publisher, not here).
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 8192}, nopLog())
	const goroutines = 100
	const perGoroutine = 50
	var wg sync.WaitGroup
	var enqueued atomic.Int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				rec.RecordFromObserve(makeRow())
				enqueued.Add(1)
			}
		}()
	}
	wg.Wait()
	if enqueued.Load() != int64(goroutines*perGoroutine) {
		t.Fatalf("expected %d enqueues, got %d", goroutines*perGoroutine, enqueued.Load())
	}
	if got := rec.PendingCount(); got != goroutines*perGoroutine {
		t.Fatalf("expected pending %d, got %d", goroutines*perGoroutine, got)
	}
}

func TestRequestTelemetryRecorder_DrainBatchEmptyReturnsNil(t *testing.T) {
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true}, nopLog())
	if got := rec.DrainBatch(64); got != nil {
		t.Fatalf("expected nil batch from empty ring, got %v", got)
	}
}

func TestRequestTelemetryRecorder_DrainBatchZeroMaxReturnsNil(t *testing.T) {
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true}, nopLog())
	rec.RecordFromObserve(makeRow())
	if got := rec.DrainBatch(0); got != nil {
		t.Fatalf("expected nil batch when max=0, got %v", got)
	}
	// Row should still be in the ring (drain did not consume it).
	if rec.PendingCount() != 1 {
		t.Fatalf("expected 1 pending after zero-max drain, got %d", rec.PendingCount())
	}
}

// --- publisher tests ---

// fakeShip captures every batch the publisher ships and lets the
// test inject a per-call error.
type fakeShip struct {
	mu      sync.Mutex
	batches [][]RequestTelemetryRow
	err     error // returned by every Ship call
	calls   int
}

func (f *fakeShip) Ship(_ context.Context, rows []RequestTelemetryRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return f.err
	}
	// Copy the slice so later recorder mutations don't leak.
	cp := make([]RequestTelemetryRow, len(rows))
	copy(cp, rows)
	f.batches = append(f.batches, cp)
	return nil
}

func (f *fakeShip) TotalRows() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.batches {
		n += len(b)
	}
	return n
}

func TestRequestTelemetryPublisher_ShipSuccessIncrementsCounter(t *testing.T) {
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 16}, nopLog())
	ship := &fakeShip{}
	pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
		Enabled:        true,
		FlushInterval:  10 * time.Millisecond,
		FlushBatchSize: 8,
		MaxRetries:     1,
	}, rec, ship.Ship, nopLog())
	pub.Start(context.Background())
	defer pub.Stop()

	for i := 0; i < 5; i++ {
		rec.RecordFromObserve(makeRow())
	}
	// Give the goroutine one tick to drain.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ship.TotalRows() == 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := ship.TotalRows(); got != 5 {
		t.Fatalf("expected ship to receive 5 rows, got %d", got)
	}
	if got := pub.ShippedTotal(); got != 5 {
		t.Fatalf("expected ShippedTotal=5, got %d", got)
	}
	if got := pub.DroppedTotal(); got != 0 {
		t.Fatalf("expected DroppedTotal=0, got %d", got)
	}
	if rec.PendingCount() != 0 {
		t.Fatalf("expected ring drained, got %d pending", rec.PendingCount())
	}
}

func TestRequestTelemetryPublisher_RetriesOnTransientErrorThenSucceeds(t *testing.T) {
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 16}, nopLog())
	ship := &flakyShip{failuresBeforeSuccess: 2}
	pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
		Enabled:        true,
		FlushInterval:  10 * time.Millisecond,
		FlushBatchSize: 8,
		MaxRetries:     5, // tolerate 2 failures then succeed on attempt 3
	}, rec, ship.Ship, nopLog())
	pub.Start(context.Background())
	defer pub.Stop()

	rec.RecordFromObserve(makeRow())
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pub.ShippedTotal() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := pub.ShippedTotal(); got != 1 {
		t.Fatalf("expected ShippedTotal=1 after retry-then-success, got %d (calls=%d)", got, ship.calls)
	}
	if got := pub.DroppedTotal(); got != 0 {
		t.Fatalf("expected DroppedTotal=0, got %d", got)
	}
}

func TestRequestTelemetryPublisher_ExhaustsRetriesThenDrops(t *testing.T) {
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 16}, nopLog())
	ship := &alwaysFailShip{err: errors.New("apid unreachable")}
	pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
		Enabled:        true,
		FlushInterval:  10 * time.Millisecond,
		FlushBatchSize: 8,
		MaxRetries:     2, // fail twice ⇒ drop
	}, rec, ship.Ship, nopLog())
	pub.Start(context.Background())
	defer pub.Stop()

	for i := 0; i < 3; i++ {
		rec.RecordFromObserve(makeRow())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pub.DroppedTotal() == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := pub.DroppedTotal(); got != 3 {
		t.Fatalf("expected DroppedTotal=3, got %d (calls=%d)", got, ship.calls)
	}
	if got := pub.ShippedTotal(); got != 0 {
		t.Fatalf("expected ShippedTotal=0, got %d", got)
	}
}

func TestRequestTelemetryPublisher_WakeDrainsImmediately(t *testing.T) {
	// Set a deliberately long flush interval so the only way the
	// rows ship before the test deadline is via Wake().
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 16}, nopLog())
	ship := &fakeShip{}
	pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
		Enabled:        true,
		FlushInterval:  10 * time.Second, // 10s — Wake must shortcut
		FlushBatchSize: 8,
		MaxRetries:     1,
	}, rec, ship.Ship, nopLog())
	pub.Start(context.Background())
	defer pub.Stop()

	rec.RecordFromObserve(makeRow())
	pub.Wake()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ship.TotalRows() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := ship.TotalRows(); got != 1 {
		t.Fatalf("expected ship to receive 1 row via Wake, got %d", got)
	}
}

func TestRequestTelemetryRecorder_WakeHookTriggersNearFull(t *testing.T) {
	// RingSize=4 arms the wake hook at two pending rows. The callback
	// should wake the publisher without waiting for its long ticker.
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 4}, nopLog())
	ship := &fakeShip{}
	pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
		Enabled:        true,
		FlushInterval:  10 * time.Second,
		FlushBatchSize: 8,
		MaxRetries:     1,
	}, rec, ship.Ship, nopLog())
	rec.SetWakeHook(pub.Wake)
	pub.Start(context.Background())
	defer pub.Stop()

	rec.RecordFromObserve(makeRow())
	rec.RecordFromObserve(makeRow())
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ship.TotalRows() == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := ship.TotalRows(); got != 2 {
		t.Fatalf("expected near-full wake to ship 2 rows, got %d", got)
	}
}

func TestRequestTelemetryPublisher_StopDrainsFinalBatch(t *testing.T) {
	// Long flush interval + enqueue + Stop. The final drain in
	// run() must ship the rows synchronously before doneCh closes.
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 16}, nopLog())
	ship := &fakeShip{}
	pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
		Enabled:        true,
		FlushInterval:  10 * time.Second,
		FlushBatchSize: 8,
		MaxRetries:     1,
	}, rec, ship.Ship, nopLog())
	pub.Start(context.Background())

	for i := 0; i < 4; i++ {
		rec.RecordFromObserve(makeRow())
	}
	pub.Stop() // synchronous: must ship before returning

	if got := ship.TotalRows(); got != 4 {
		t.Fatalf("expected ship to receive 4 rows on Stop, got %d", got)
	}
	if got := pub.ShippedTotal(); got != 4 {
		t.Fatalf("expected ShippedTotal=4, got %d", got)
	}
}

func TestRequestTelemetryPublisher_StopDrainsAllBatches(t *testing.T) {
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 32}, nopLog())
	ship := &fakeShip{}
	pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
		Enabled:        true,
		FlushInterval:  10 * time.Second,
		FlushBatchSize: 4,
		MaxRetries:     1,
	}, rec, ship.Ship, nopLog())
	pub.Start(context.Background())
	for i := 0; i < 10; i++ {
		rec.RecordFromObserve(makeRow())
	}
	pub.Stop()

	if got, want := ship.TotalRows(), 10; got != want {
		t.Fatalf("Stop shipped %d rows, want %d", got, want)
	}
	if got, want := pub.ShippedTotal(), int64(10); got != want {
		t.Fatalf("ShippedTotal = %d, want %d", got, want)
	}
}

func TestRequestTelemetryPublisher_NilShipDropsRows(t *testing.T) {
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 16}, nopLog())
	pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
		Enabled:        true,
		FlushInterval:  10 * time.Millisecond,
		FlushBatchSize: 8,
		MaxRetries:     1,
	}, rec, nil, nopLog())
	pub.Start(context.Background())
	defer pub.Stop()

	for i := 0; i < 3; i++ {
		rec.RecordFromObserve(makeRow())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pub.DroppedTotal() == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := pub.DroppedTotal(); got != 3 {
		t.Fatalf("expected DroppedTotal=3 (nil ship drops rows), got %d", got)
	}
}

func TestRequestTelemetryPublisher_CountsOriginalRequests(t *testing.T) {
	row := makeRow()
	row.Count = 10
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 4}, nopLog())
	rec.RecordFromObserve(row)
	ship := &fakeShip{}
	pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
		Enabled:        true,
		FlushBatchSize: 4,
		MaxRetries:     1,
	}, rec, ship.Ship, nopLog())
	pub.tick(context.Background())

	if got, want := pub.ShippedTotal(), int64(10); got != want {
		t.Fatalf("ShippedTotal = %d, want %d original requests", got, want)
	}
	if got, want := pub.DroppedTotal(), int64(0); got != want {
		t.Fatalf("DroppedTotal = %d, want %d", got, want)
	}
}

func TestRequestTelemetryPublisher_DroppedCountsOriginalRequests(t *testing.T) {
	row := makeRow()
	row.Count = 10
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 4}, nopLog())
	rec.RecordFromObserve(row)
	ship := &alwaysFailShip{err: errors.New("apid unreachable")}
	pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
		Enabled:        true,
		FlushBatchSize: 4,
		MaxRetries:     1,
	}, rec, ship.Ship, nopLog())
	pub.tick(context.Background())

	if got, want := pub.DroppedTotal(), int64(10); got != want {
		t.Fatalf("DroppedTotal = %d, want %d original requests", got, want)
	}
}

func TestCollapseRequestTelemetry_AggregatesAndPreservesCounts(t *testing.T) {
	base := makeRow()
	base.ReceivedAt = time.Date(2026, 9, 6, 12, 34, 10, 0, time.UTC)
	first := base
	first.Count = 3
	first.LatencyMS = 20
	second := base
	second.Count = 5
	second.LatencyMS = 80
	third := base
	third.ReceivedAt = base.ReceivedAt.Add(2 * time.Minute)
	third.Count = 2
	rows := []RequestTelemetryRow{first, second, third}
	got := collapseRequestTelemetry(rows)
	if len(got) != 2 {
		t.Fatalf("expected two minute buckets, got %d", len(got))
	}
	if got[0].Count != 8 {
		t.Errorf("aggregated Count = %d, want 8", got[0].Count)
	}
	if got[0].LatencyMS != 80 {
		t.Errorf("aggregated LatencyMS = %d, want max 80", got[0].LatencyMS)
	}
	if got[1].Count != 2 {
		t.Errorf("second bucket Count = %d, want 2", got[1].Count)
	}
}

// --- helpers for flaky/always-fail ship ---

type flakyShip struct {
	mu                    sync.Mutex
	failuresBeforeSuccess int
	calls                 int
}

func (f *flakyShip) Ship(_ context.Context, _ []RequestTelemetryRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failuresBeforeSuccess {
		return errors.New("transient")
	}
	return nil
}

type alwaysFailShip struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (a *alwaysFailShip) Ship(_ context.Context, _ []RequestTelemetryRow) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return a.err
}
