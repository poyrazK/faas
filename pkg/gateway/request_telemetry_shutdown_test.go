// adr: 375
package gateway

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type telemetryShutdownContextKey struct{}

func TestRequestTelemetryShutdownCanceledContext(t *testing.T) {
	recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 32}, nopLog())
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), telemetryShutdownContextKey{}, "correlation"))
	defer cancel()
	shipped := int64(0)
	pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
		Enabled: true, FlushInterval: time.Hour, FlushBatchSize: 2, MaxRetries: 1,
	}, recorder, func(ctx context.Context, rows []RequestTelemetryRow) error {
		if ctx.Value(telemetryShutdownContextKey{}) != "correlation" {
			t.Error("flush lost daemon correlation")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		shipped += requestTelemetryCount(rows)
		return nil
	}, nopLog())
	pub.Start(ctx)
	for i := range 3 {
		row := makeRow()
		row.Count = i + 1
		recorder.RecordFromObserve(row)
	}
	cancel()
	// Producers may complete during the daemon's HTTP drain after cancellation.
	for range 4 {
		recorder.RecordFromObserve(makeRow())
	}
	pub.Stop()
	if shipped != 10 || pub.ShippedTotal() != 10 || pub.DroppedTotal() != 0 || recorder.PendingCount() != 0 {
		t.Fatalf("final flush shipped=%d metric=%d dropped=%d pending=%d", shipped, pub.ShippedTotal(), pub.DroppedTotal(), recorder.PendingCount())
	}
}

func TestRequestTelemetryShutdownInterruptedBatch(t *testing.T) {
	for _, cause := range []string{"parent_cancellation", "explicit_stop"} {
		t.Run(cause, func(t *testing.T) {
			recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 32}, nopLog())
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), telemetryShutdownContextKey{}, "correlation"))
			defer cancel()
			entered, replayed, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			var original []RequestTelemetryRow
			accepted := make(map[uuid.UUID]int64)
			pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
				Enabled: true, FlushInterval: time.Hour, FlushBatchSize: 1, MaxRetries: 1, ShutdownTimeout: time.Second,
			}, recorder, func(ctx context.Context, rows []RequestTelemetryRow) error {
				if ctx.Value(telemetryShutdownContextKey{}) != "correlation" {
					t.Error("flush lost correlation")
				}
				call := calls.Add(1)
				for _, row := range rows {
					accepted[row.EventID] = int64(row.Count)
				}
				if call == 1 {
					original = append([]RequestTelemetryRow(nil), rows...)
					close(entered)
					<-ctx.Done()
					return ctx.Err() // Receiver accepted the row, acknowledgment is ambiguous.
				}
				if call == 2 {
					if !reflect.DeepEqual(rows, original) {
						t.Error("interrupted payload or event ID changed on final replay")
					}
					close(replayed)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			}, nopLog())
			pub.Start(ctx)
			row := makeRow()
			row.Count = 3
			recorder.RecordFromObserve(row)
			pub.Wake()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				cancel()
				pub.Stop()
				t.Fatal("normal RPC did not start")
			}
			if cause == "parent_cancellation" {
				cancel()
			}
			for range 4 {
				recorder.RecordFromObserve(makeRow())
			}
			finished := make(chan struct{}, 8)
			var stops sync.WaitGroup
			for range 8 {
				stops.Go(func() { pub.Stop(); finished <- struct{}{} })
			}
			select {
			case <-replayed:
			case <-time.After(2 * time.Second):
				cancel()
				stops.Wait()
				t.Fatal("final replay did not start")
			}
			select {
			case <-finished:
				t.Error("Stop returned before final RPC joined")
			default:
			}
			close(release)
			stops.Wait()
			total := int64(0)
			for _, count := range accepted {
				total += count
			}
			if total != 7 || pub.ShippedTotal() != 7 || pub.DroppedTotal() != 0 || recorder.PendingCount() != 0 || calls.Load() != 6 {
				t.Fatalf("deduplicated=%d shipped=%d dropped=%d pending=%d RPCs=%d", total, pub.ShippedTotal(), pub.DroppedTotal(), recorder.PendingCount(), calls.Load())
			}
		})
	}
}

func TestRequestTelemetryShutdownBoundedFailure(t *testing.T) {
	for _, mode := range []string{"publisher_deadline", "owner_deadline", "owner_canceled"} {
		t.Run(mode, func(t *testing.T) {
			recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 32}, nopLog())
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			var calls atomic.Int32
			var dropped atomic.Int64
			pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
				Enabled: true, FlushInterval: time.Hour, FlushBatchSize: 1, MaxRetries: 1, ShutdownTimeout: 50 * time.Millisecond,
				OnDropped: func(n int64) { dropped.Add(n) },
			}, recorder, func(ctx context.Context, _ []RequestTelemetryRow) error { calls.Add(1); <-ctx.Done(); return ctx.Err() }, nopLog())
			pub.Start(ctx)
			for range 9 {
				row := makeRow()
				row.Count = 2
				recorder.RecordFromObserve(row)
			}
			owner, end := context.WithCancel(context.WithoutCancel(ctx))
			if mode == "owner_deadline" {
				end()
				owner, end = context.WithTimeout(context.WithoutCancel(ctx), 20*time.Millisecond)
			}
			defer end()
			if mode == "owner_canceled" {
				end()
			}
			start := time.Now()
			if mode == "publisher_deadline" {
				pub.Stop()
			} else {
				pub.StopWithContext(owner)
			}
			maximumCalls := int32(1)
			if mode == "owner_canceled" {
				maximumCalls = 0
			}
			if time.Since(start) > time.Second || calls.Load() != maximumCalls || pub.ShippedTotal() != 0 || pub.DroppedTotal() != 18 || dropped.Load() != 18 || recorder.PendingCount() != 0 {
				t.Fatalf("Stop elapsed=%s RPCs=%d shipped=%d dropped=%d hook=%d pending=%d", time.Since(start), calls.Load(), pub.ShippedTotal(), pub.DroppedTotal(), dropped.Load(), recorder.PendingCount())
			}
		})
	}
}

func TestRequestTelemetryShutdownLifecycle(t *testing.T) {
	for _, mode := range []string{"stop_before_start", "disabled", "start_stop_race"} {
		t.Run(mode, func(t *testing.T) {
			for range 20 {
				recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 8}, nopLog())
				var calls atomic.Int32
				pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{Enabled: mode != "disabled", FlushInterval: time.Hour}, recorder,
					func(context.Context, []RequestTelemetryRow) error { calls.Add(1); return nil }, nopLog())
				if mode == "start_stop_race" {
					var operations sync.WaitGroup
					operations.Go(func() { pub.Start(t.Context()) })
					operations.Go(pub.Stop)
					operations.Wait()
				} else {
					if mode == "stop_before_start" {
						pub.Stop()
					}
					recorder.RecordFromObserve(makeRow())
					pub.Start(t.Context())
					pub.Stop()
				}
				pub.Stop()
				if calls.Load() != 0 {
					t.Fatal("disabled or terminal publisher performed work")
				}
			}
		})
	}
}

func TestRequestTelemetryShutdownConfiguration(t *testing.T) {
	ceiling := time.Duration(api.GatewayRequestTelemetryShutdownTimeoutSeconds) * time.Second
	for _, configured := range []time.Duration{0, -time.Second, 10 * time.Second, 50 * time.Millisecond} {
		t.Run(configured.String(), func(t *testing.T) {
			recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 8}, nopLog())
			pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{Enabled: true, FlushInterval: time.Hour, ShutdownTimeout: configured}, recorder,
				func(ctx context.Context, _ []RequestTelemetryRow) error {
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > ceiling || (configured > 0 && configured < ceiling && time.Until(deadline) > configured) {
						t.Error("shipping context exceeds operational ceiling")
					}
					return nil
				}, nopLog())
			pub.Start(t.Context())
			recorder.RecordFromObserve(makeRow())
			pub.Stop()
			if pub.ShippedTotal() != 1 {
				t.Error("configured final flush did not ship")
			}
		})
	}
}
