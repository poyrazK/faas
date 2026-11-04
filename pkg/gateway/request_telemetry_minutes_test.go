// adr: 20261101085552
package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type minuteCollapseFixture struct {
	recorder  *requestTelemetryRecorder
	publisher *requestTelemetryPublisher
	now       time.Time

	mu       sync.Mutex
	shipped  []RequestTelemetryRow
	coverage RequestTelemetryCoverage
}

func newMinuteCollapseFixture(t *testing.T, minuteCollapse bool) *minuteCollapseFixture {
	t.Helper()
	f := &minuteCollapseFixture{now: time.Date(2026, 11, 1, 12, 0, 10, 0, time.UTC)}
	f.recorder = NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 4096}, nil)
	f.publisher = NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{
		Enabled:        true,
		FlushBatchSize: 256,
		MaxRetries:     1,
		MinuteCollapse: minuteCollapse,
		Now:            func() time.Time { return f.now },
		OnCoverage: func(_ context.Context, report RequestTelemetryCoverage) error {
			f.mu.Lock()
			f.coverage = report
			f.mu.Unlock()
			return nil
		},
	}, f.recorder, func(_ context.Context, rows []RequestTelemetryRow) error {
		f.mu.Lock()
		f.shipped = append(f.shipped, rows...)
		f.mu.Unlock()
		return nil
	}, nil)
	return f
}

func (f *minuteCollapseFixture) record(app uuid.UUID, at time.Time, n int) {
	for range n {
		f.recorder.RecordFromObserve(RequestTelemetryRow{
			AccountID: app, AppID: app, DeploymentID: app,
			Route: "GET /", Method: "GET", Status: 200, LatencyMS: 12,
			ReceivedAt: at, Count: 1,
		})
	}
}

func (f *minuteCollapseFixture) tickAt(at time.Time) {
	f.now = at
	f.publisher.tick(context.Background())
}

func shippedRequests(rows []RequestTelemetryRow) (n int) {
	for _, row := range rows {
		n += row.Count
	}
	return n
}

// A bucket that stays busy for a minute ships once, after the minute and its
// grace have passed. The per-batch path ships it once per drained batch.
func TestRequestTelemetryMinuteCollapseShipsEachBucketOncePerMinute(t *testing.T) {
	app := uuid.New()
	minute := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name           string
		minuteCollapse bool
		wantRows       int
	}{
		{name: "per batch", minuteCollapse: false, wantRows: 3},
		{name: "per minute", minuteCollapse: true, wantRows: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMinuteCollapseFixture(t, tc.minuteCollapse)
			for i, second := range []int{20, 40, 59} {
				f.record(app, minute.Add(time.Duration(second-1)*time.Second), 100)
				f.tickAt(minute.Add(time.Duration(second) * time.Second))
				if tc.minuteCollapse && len(f.shipped) != 0 {
					t.Fatalf("tick %d shipped %d rows while the minute was open", i, len(f.shipped))
				}
			}
			f.tickAt(minute.Add(time.Minute + 4*time.Second)) // inside the grace
			if tc.minuteCollapse && len(f.shipped) != 0 {
				t.Fatalf("shipped %d rows inside the grace", len(f.shipped))
			}
			f.tickAt(minute.Add(time.Minute + 6*time.Second))
			if len(f.shipped) != tc.wantRows || shippedRequests(f.shipped) != 300 {
				t.Fatalf("shipped %d rows for %d requests, want %d rows for 300", len(f.shipped), shippedRequests(f.shipped), tc.wantRows)
			}
		})
	}
}

func TestRequestTelemetryMinuteCollapseShipsExactRowsAtOnce(t *testing.T) {
	f := newMinuteCollapseFixture(t, true)
	f.recorder.RecordFromObserve(RequestTelemetryRow{AppID: uuid.New(), Route: "GET /", Status: 200, ReceivedAt: f.now, Count: 1, preserveExact: true})
	f.tickAt(f.now.Add(time.Second))
	if len(f.shipped) != 1 {
		t.Fatalf("exact row: shipped %d rows, want 1 on the next tick", len(f.shipped))
	}
}

func TestRequestTelemetryMinuteCollapseCountsOpenRequestsAsPending(t *testing.T) {
	f := newMinuteCollapseFixture(t, true)
	app := uuid.New()
	f.record(app, f.now, 7)
	f.tickAt(f.now.Add(time.Second))
	if f.coverage.PendingCount != 7 {
		t.Fatalf("coverage pending = %d, want 7 requests held in the open minute", f.coverage.PendingCount)
	}
	if len(f.coverage.AppGaps) != 1 || f.coverage.AppGaps[0].AppID != app || f.coverage.AppGaps[0].PendingCount != 7 {
		t.Fatalf("app gaps = %+v, want 7 pending for %s", f.coverage.AppGaps, app)
	}
}

func TestRequestTelemetryMinuteCollapseFinalFlushShipsOpenMinutes(t *testing.T) {
	f := newMinuteCollapseFixture(t, true)
	f.record(uuid.New(), f.now, 5)
	f.tickAt(f.now.Add(time.Second))
	f.publisher.tickMinutes(context.Background(), true)
	if shippedRequests(f.shipped) != 5 || !f.publisher.minutes.empty() {
		t.Fatalf("final flush shipped %d requests and left open=%v, want 5 and nothing open", shippedRequests(f.shipped), !f.publisher.minutes.empty())
	}
}

func TestRequestTelemetryMinutesBoundShipsOldestMinuteEarly(t *testing.T) {
	m := newRequestTelemetryMinutes()
	first := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i, at := range []time.Time{first, first, first, first.Add(time.Minute), first.Add(time.Minute)} {
		m.add(RequestTelemetryRow{AppID: uuid.New(), Route: "GET /", Status: 200 + i, ReceivedAt: at, Count: 1})
	}
	m.close(first.Add(30*time.Second), 5*time.Second, 2)
	if len(m.ready) != 3 || len(m.rows) != 2 {
		t.Fatalf("ready=%d open=%d, want the oldest minute's 3 rows ready and 2 open", len(m.ready), len(m.rows))
	}
	for _, row := range m.rows {
		if !row.ReceivedAt.Equal(first.Add(time.Minute)) {
			t.Fatalf("open row from %s, want only the newer minute", row.ReceivedAt)
		}
	}
	if dropped := m.trimReady(1); len(dropped) != 2 || len(m.ready) != 1 {
		t.Fatalf("trim dropped %d and kept %d, want 2 dropped and 1 kept", len(dropped), len(m.ready))
	}
}
