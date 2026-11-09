// adr: 20261103113952
package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/requestanalytics"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeRollupApid answers gateway rollup batches; err is returned for every
// non-empty batch until cleared.
type fakeRollupApid struct {
	probeErr error
	err      error
	sent     []uuid.UUID
	applied  map[uuid.UUID][]state.RequestAnalyticsRollup
}

func (a *fakeRollupApid) ship(_ context.Context, batchID uuid.UUID, rows []state.RequestAnalyticsRollup) error {
	if len(rows) == 0 {
		return a.probeErr
	}
	a.sent = append(a.sent, batchID)
	if a.err != nil {
		return a.err
	}
	if a.applied == nil {
		a.applied = map[uuid.UUID][]state.RequestAnalyticsRollup{}
	}
	a.applied[batchID] = rows
	return nil
}

func (a *fakeRollupApid) requests() int64 {
	var n int64
	for _, rows := range a.applied {
		n += rollupRequests(rows)
	}
	return n
}

type rollupClock struct{ now time.Time }

func (c *rollupClock) Now() time.Time { return c.now }

func newRollupRecorder(t *testing.T, apid *fakeRollupApid, clock *rollupClock) *requestTelemetryRecorder {
	t.Helper()
	recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 64}, nopLog())
	recorder.EnableAnalyticsRollups(RequestAnalyticsRollupsConfig{Ship: apid.ship, Now: clock.Now}, nopLog())
	return recorder
}

func rollupRow(appID uuid.UUID, at time.Time) RequestTelemetryRow {
	row := makeRow()
	row.AppID, row.ReceivedAt = appID, at
	return row
}

// Counting waits for apid to answer the empty probe batch: until then rows
// stay unflagged for apid to count.
func TestGatewayRollupsCountOnlyOnceApidTakesBatches(t *testing.T) {
	apid := &fakeRollupApid{probeErr: ErrRequestAnalyticsRollupsUnsupported}
	clock := &rollupClock{now: time.Date(2026, 11, 9, 12, 0, 10, 0, time.UTC)}
	recorder := newRollupRecorder(t, apid, clock)
	app := uuid.New()
	recorder.analytics.tick(t.Context(), false)
	recorder.RecordFromObserve(rollupRow(app, clock.now))
	if rows := recorder.DrainBatch(10); len(rows) != 1 || rows[0].AnalyticsCounted {
		t.Fatalf("rows before apid takes batches = %+v, want one unflagged", rows)
	}
	// A refusing apid is asked again only after the probe interval.
	apid.probeErr = nil
	clock.now = clock.now.Add(time.Minute)
	recorder.analytics.tick(t.Context(), false)
	if recorder.analytics.active.Load() {
		t.Fatal("asked again before the probe interval")
	}
	clock.now = clock.now.Add(api.RequestAnalyticsGatewayRollupProbeInterval)
	recorder.analytics.tick(t.Context(), false)
	recorder.RecordFromObserve(rollupRow(app, clock.now))
	if rows := recorder.DrainBatch(10); len(rows) != 1 || !rows[0].AnalyticsCounted {
		t.Fatalf("rows once apid takes batches = %+v, want one flagged", rows)
	}
}

// A minute ships once it closed the grace ago; a batch whose answer was lost
// is resent with the same batch_id.
func TestGatewayRollupsShipClosedMinutesAndResendTheSameBatch(t *testing.T) {
	apid := &fakeRollupApid{}
	clock := &rollupClock{now: time.Date(2026, 11, 9, 12, 0, 10, 0, time.UTC)}
	recorder := newRollupRecorder(t, apid, clock)
	g := recorder.analytics
	g.tick(t.Context(), false) // probe
	app := uuid.New()
	for range 5 {
		recorder.RecordFromObserve(rollupRow(app, clock.now))
	}
	clock.now = clock.now.Add(time.Minute)
	g.tick(t.Context(), false)
	if len(apid.sent) != 0 {
		t.Fatalf("shipped %d batches before the grace passed", len(apid.sent))
	}
	if got := g.undelivered()[app]; got != 0 {
		t.Fatalf("undelivered = %d inside the grace, want none late", got)
	}
	clock.now = clock.now.Add(api.RequestAnalyticsRollupMinuteGrace)
	apid.err = errors.New("apid unavailable")
	g.tick(t.Context(), false)
	if got := g.undelivered()[app]; got != 5 {
		t.Fatalf("undelivered = %d, want the unsent minute's 5 late", got)
	}
	apid.err = nil
	g.tick(t.Context(), false)
	if len(apid.sent) != 2 || apid.sent[0] != apid.sent[1] {
		t.Fatalf("sent = %v, want one batch sent twice under one batch_id", apid.sent)
	}
	if apid.requests() != 5 || len(g.batches) != 0 || len(g.undelivered()) != 0 {
		t.Fatalf("applied %d requests, %d batches waiting; want 5 and none", apid.requests(), len(g.batches))
	}
	for _, rows := range apid.applied {
		kinds := map[string]bool{}
		for _, r := range rows {
			kinds[r.DimensionKind] = true
		}
		if !kinds[requestanalytics.KindRoute] || len(kinds) != 1+len(requestanalytics.FoldedKinds) {
			t.Fatalf("kinds = %v, want the route row and every folded kind", kinds)
		}
	}
}

// A refused batch, or one past the bound, is lost and reported per app; an
// apid that stops taking batches is not a loss, since it counts the rows.
func TestGatewayRollupsAccountForGivenUpBatches(t *testing.T) {
	clock := &rollupClock{now: time.Date(2026, 11, 9, 12, 0, 10, 0, time.UTC)}
	closeMinute := func(recorder *requestTelemetryRecorder, app uuid.UUID, n int) {
		for range n {
			recorder.RecordFromObserve(rollupRow(app, clock.now))
		}
		clock.now = clock.now.Add(time.Minute + api.RequestAnalyticsRollupMinuteGrace)
	}
	t.Run("refused", func(t *testing.T) {
		apid := &fakeRollupApid{}
		recorder := newRollupRecorder(t, apid, clock)
		recorder.analytics.tick(t.Context(), false)
		app := uuid.New()
		closeMinute(recorder, app, 3)
		apid.err = ErrRequestAnalyticsRollupBatchRefused
		recorder.analytics.tick(t.Context(), false)
		if recorder.analyticsLosses[app] != 3 || len(recorder.appLosses) != 0 || len(recorder.analytics.batches) != 0 {
			t.Fatalf("analytics losses = %v, raw %v, batches %d; want 3 lost to analytics only", recorder.analyticsLosses, recorder.appLosses, len(recorder.analytics.batches))
		}
	})
	t.Run("past the bound", func(t *testing.T) {
		apid := &fakeRollupApid{err: errors.New("apid down")}
		recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 64}, nopLog())
		recorder.EnableAnalyticsRollups(RequestAnalyticsRollupsConfig{Ship: apid.ship, Now: clock.Now, MaxBatches: 1}, nopLog())
		recorder.analytics.active.Store(true)
		first, second := uuid.New(), uuid.New()
		closeMinute(recorder, first, 2)
		recorder.analytics.tick(t.Context(), false)
		closeMinute(recorder, second, 1)
		recorder.analytics.tick(t.Context(), false)
		if recorder.analyticsLosses[first] != 2 || recorder.analyticsLosses[second] != 0 || len(recorder.analytics.batches) != 1 {
			t.Fatalf("losses = %v, batches %d; want the oldest batch lost", recorder.analyticsLosses, len(recorder.analytics.batches))
		}
	})
	t.Run("apid stops taking batches", func(t *testing.T) {
		apid := &fakeRollupApid{}
		recorder := newRollupRecorder(t, apid, clock)
		recorder.analytics.tick(t.Context(), false)
		app := uuid.New()
		closeMinute(recorder, app, 2)
		apid.err = ErrRequestAnalyticsRollupsUnsupported
		recorder.analytics.tick(t.Context(), false)
		if len(recorder.analyticsLosses) != 0 || recorder.analytics.active.Load() || recorder.analytics.agg.Len() != 0 {
			t.Fatalf("losses = %v, active %v; want no loss and counting off", recorder.appLosses, recorder.analytics.active.Load())
		}
		recorder.DrainBatch(10)
		recorder.RecordFromObserve(rollupRow(app, clock.now))
		if rows := recorder.DrainBatch(10); len(rows) != 1 || rows[0].AnalyticsCounted {
			t.Fatalf("row after apid stopped = %+v, want unflagged", rows)
		}
	})
}

// The final flush ships the open minute and stops counting for good.
func TestGatewayRollupsFinalFlushShipsOpenMinutes(t *testing.T) {
	apid := &fakeRollupApid{}
	clock := &rollupClock{now: time.Date(2026, 11, 9, 12, 0, 10, 0, time.UTC)}
	recorder := newRollupRecorder(t, apid, clock)
	g := recorder.analytics
	g.tick(t.Context(), false)
	app := uuid.New()
	recorder.RecordFromObserve(rollupRow(app, clock.now))
	g.tick(t.Context(), true)
	if apid.requests() != 1 {
		t.Fatalf("final flush shipped %d requests, want the open minute's 1", apid.requests())
	}
	g.tick(t.Context(), false)
	recorder.DrainBatch(10)
	recorder.RecordFromObserve(rollupRow(app, clock.now))
	if rows := recorder.DrainBatch(10); len(rows) != 1 || rows[0].AnalyticsCounted || g.active.Load() {
		t.Fatalf("row after the final flush = %+v, want unflagged and counting off", rows)
	}
}

// A flagged row never folds with an unflagged one in the publisher's collapse.
func TestRequestTelemetryCollapseKeepsTheAnalyticsFlagApart(t *testing.T) {
	a := makeRow()
	b := a
	b.AnalyticsCounted = true
	if rows := collapseRequestTelemetry([]RequestTelemetryRow{a, b}); len(rows) != 2 {
		t.Fatalf("collapsed = %d rows, want flagged and unflagged kept apart", len(rows))
	}
}

// A raw row the gateway counted and then lost costs the debugger, not
// analytics; one it did not count costs both (amendment 6).
func TestGatewayRollupsRawLossCostsAnalyticsOnlyWhenUncounted(t *testing.T) {
	apid := &fakeRollupApid{}
	clock := &rollupClock{now: time.Date(2026, 11, 9, 12, 0, 10, 0, time.UTC)}
	recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 1}, nopLog())
	recorder.EnableAnalyticsRollups(RequestAnalyticsRollupsConfig{Ship: apid.ship, Now: clock.Now}, nopLog())
	counted, uncounted := uuid.New(), uuid.New()
	recorder.RecordFromObserve(rollupRow(uncounted, clock.now)) // before the probe: not counted
	recorder.analytics.tick(t.Context(), false)
	recorder.RecordFromObserve(rollupRow(counted, clock.now)) // overwrites the uncounted row
	recorder.RecordFromObserve(rollupRow(counted, clock.now)) // overwrites the counted row
	if recorder.appLosses[uncounted] != 1 || recorder.appLosses[counted] != 1 {
		t.Fatalf("raw losses = %v, want one each", recorder.appLosses)
	}
	if recorder.analyticsLosses[uncounted] != 1 || recorder.analyticsLosses[counted] != 0 {
		t.Fatalf("analytics losses = %v, want only the uncounted row", recorder.analyticsLosses)
	}
}

// The heartbeat carries the analytics part apart from raw delivery, and
// subtracts the analytics deltas a receipt acknowledges.
func TestGatewayRollupsCoverageReportsAnalyticsApart(t *testing.T) {
	apid := &fakeRollupApid{err: errors.New("apid down")}
	clock := &rollupClock{now: time.Date(2026, 11, 9, 12, 0, 10, 0, time.UTC)}
	recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 64}, nopLog())
	recorder.EnableAnalyticsRollups(RequestAnalyticsRollupsConfig{Ship: apid.ship, Now: clock.Now}, nopLog())
	recorder.analytics.active.Store(true)
	app := uuid.New()
	for range 4 {
		recorder.RecordFromObserve(rollupRow(app, clock.now))
	}
	recorder.ringMu.Lock()
	recorder.recordAnalyticsLossLocked(app, 2)
	recorder.ringMu.Unlock()
	var reports []RequestTelemetryCoverage
	publisher := NewRequestTelemetryPublisher(RequestTelemetryPublisherConfig{Enabled: true, Now: clock.Now,
		OnCoverage: func(_ context.Context, c RequestTelemetryCoverage) error { reports = append(reports, c); return nil },
	}, recorder, func(context.Context, []RequestTelemetryRow) error { return nil }, nopLog())
	clock.now = clock.now.Add(time.Minute + api.RequestAnalyticsRollupMinuteGrace)
	publisher.tick(t.Context()) // raw rows ship; the rollup batch does not
	if len(reports) != 1 || reports[0].Analytics == nil || !reports[0].Analytics.Counting {
		t.Fatalf("reports = %+v, want one with a counting analytics part", reports)
	}
	if len(reports[0].AppGaps) != 1 {
		t.Fatalf("gaps = %+v, want one app", reports[0].AppGaps)
	}
	gap := reports[0].AppGaps[0]
	if gap.PendingCount != 0 || gap.DroppedCount != 0 || gap.AnalyticsPendingCount != 4 || gap.AnalyticsDroppedCount != 2 {
		t.Fatalf("gap = %+v, want raw clean, 4 late and 2 lost for analytics", gap)
	}
	if len(recorder.analyticsLosses) != 0 {
		t.Fatalf("analytics losses after the receipt = %v, want the acknowledged delta gone", recorder.analyticsLosses)
	}
}
