package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// adr: 127
// A backlog past the wake threshold drains in one tick instead of one batch
// per FlushInterval: the wake hook stays disarmed while the recorder is over
// half full, so one batch per tick capped delivery at 256 rows per 5 s.
func TestRequestTelemetryPublisherDrainsBacklogInOneTick(t *testing.T) {
	rec := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true}, nil)
	var shipped, batches int
	pub := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{Enabled: true}, rec, func(_ context.Context, rows []RequestTelemetryRow) error {
		batches++
		for _, r := range rows {
			shipped += r.Count
		}
		return nil
	}, nil)
	app, acct, dep := uuid.New(), uuid.New(), uuid.New()
	const queued = 3000
	for i := range queued {
		rec.RecordFromObserve(RequestTelemetryRow{AccountID: acct, AppID: app, DeploymentID: dep,
			Route: fmt.Sprintf("GET /r/%d", i), Method: "GET", Status: 200, LatencyMS: 5, ReceivedAt: time.Now(), Count: 1})
	}
	pub.tick(t.Context())
	if left := rec.PendingCount(); left >= pub.cfg.FlushBatchSize || shipped != queued-left {
		t.Fatalf("one tick shipped %d of %d rows in %d batches, %d left; want all but a partial batch", shipped, queued, batches, left)
	}
}
