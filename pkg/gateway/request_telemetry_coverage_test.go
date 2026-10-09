// adr: 826
package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRequestTelemetryCoverage(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		enabled               bool
		rows, capacity, batch int
		fail                  bool
		drops                 int64
		pending               int
	}{
		{"idle", true, 0, 4, 4, false, 0, 0},
		{"disabled", false, 0, 4, 4, false, 0, 0},
		{"delivered", true, 3, 4, 4, false, 0, 0},
		{"backlog", true, 3, 4, 1, false, 0, 2},
		{"rejected", true, 3, 4, 4, true, 3, 0},
		{"overwrite", true, 3, 1, 4, false, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: tc.enabled, RingSize: tc.capacity}, nil)
			for i := 0; i < tc.rows; i++ {
				recorder.RecordFromObserve(makeRow())
			}
			var reports []RequestTelemetryCoverage
			publisher := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{Enabled: true, FlushBatchSize: tc.batch, MaxRetries: 1, Now: func() time.Time { return now },
				OnCoverage: func(_ context.Context, c RequestTelemetryCoverage) error { reports = append(reports, c); return nil },
			}, recorder, func(context.Context, []RequestTelemetryRow) error {
				if tc.fail {
					return errors.New("receiver rejected delivery")
				}
				return nil
			}, nil)
			publisher.tick(context.Background())
			if len(reports) != 1 {
				t.Fatalf("coverage not reported: %+v", reports)
			}
			got := reports[0]
			if got.Enabled != tc.enabled || got.DroppedTotal != tc.drops || got.PendingCount != tc.pending || got.SamplingBasisPoints != 10000 || !got.SourceAt.Equal(now) {
				t.Fatalf("coverage: %+v", got)
			}
		})
	}
}

func TestRequestTelemetryAppIsolation(t *testing.T) {
	good, bad := makeRow(), makeRow()
	recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 4}, nil)
	recorder.RecordFromObserve(good)
	recorder.RecordFromObserve(bad)
	var coverage RequestTelemetryCoverage
	var batches [][]RequestTelemetryRow
	publisher := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{Enabled: true, FlushBatchSize: 4, MaxRetries: 2,
		OnCoverage: func(_ context.Context, c RequestTelemetryCoverage) error { coverage = c; return nil },
	}, recorder, func(_ context.Context, rows []RequestTelemetryRow) error {
		batches = append(batches, append([]RequestTelemetryRow(nil), rows...))
		accepted := map[uuid.UUID]bool{}
		for _, row := range rows {
			if row.AppID == good.AppID {
				accepted[row.EventID] = true
			}
		}
		return &RequestTelemetryDeliveryError{Accepted: accepted, Cause: errors.New("other app rate limited")}
	}, nil)
	publisher.tick(context.Background())
	if len(batches) != 2 || len(batches[1]) != 1 || batches[1][0].AppID != bad.AppID {
		t.Fatalf("retried acknowledged app: %+v", batches)
	}
	if publisher.ShippedTotal() != 1 || publisher.DroppedTotal() != 1 {
		t.Fatalf("shipping counts: %d %d", publisher.ShippedTotal(), publisher.DroppedTotal())
	}
	if !coverage.AppScoped || len(coverage.AppGaps) != 1 || coverage.AppGaps[0].AppID != bad.AppID || coverage.AppGaps[0].DroppedCount != 1 || coverage.UnattributedDroppedTotal != 0 {
		t.Fatalf("unrelated app lost coverage: %+v", coverage)
	}
	publisher.tick(context.Background())
	if len(coverage.AppGaps) != 0 {
		t.Fatal("acknowledged loss delta was replayed")
	}
}

func TestRequestTelemetryAppJournal(t *testing.T) {
	first, second := makeRow(), makeRow()
	recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 1}, nil)
	recorder.RecordFromObserve(first)
	recorder.RecordFromObserve(second)
	var reports []RequestTelemetryCoverage
	fail := true
	publisher := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{Enabled: true,
		OnCoverage: func(_ context.Context, c RequestTelemetryCoverage) error {
			reports = append(reports, c)
			if fail {
				return errors.New("coverage receipt lost")
			}
			// A concurrent ring overwrite after the snapshot must survive its ACK.
			recorder.RecordFromObserve(first)
			recorder.RecordFromObserve(second)
			return nil
		},
	}, recorder, nil, nil)
	publisher.reportCoverage(context.Background())
	fail = false
	publisher.reportCoverage(context.Background())
	if reports[0].AppGaps[0].DroppedCount+reports[0].AppGaps[1].DroppedCount != 1 || reports[1].UnattributedDroppedTotal != 0 {
		t.Fatalf("lost retry journal: %+v", reports)
	}
	// first app's second loss remains; the second app overflow used the global fallback.
	recorder.ringMu.Lock()
	loss := recorder.appLosses[first.AppID]
	unattributed := recorder.unattributedLosses
	recorder.ringMu.Unlock()
	if loss != 1 || unattributed != 1 {
		t.Fatalf("ACK erased concurrent losses: %d %d", loss, unattributed)
	}
}
