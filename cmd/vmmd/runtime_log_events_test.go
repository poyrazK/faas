package main

// adr: 20261018083536 — vmmd projects warn/error lines within a per-app
// minute budget, summarizes the overflow, and pauses when apid refuses.

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
)

type fakeRuntimeLogClient struct {
	batches [][]*apidpb.RuntimeLogEvent
	err     error
}

func (f *fakeRuntimeLogClient) RecordRuntimeLogEvents(_ context.Context, req *apidpb.RecordRuntimeLogEventsRequest) (*apidpb.RecordRuntimeLogEventsResponse, error) {
	f.batches = append(f.batches, req.GetEvents())
	if f.err != nil {
		return nil, f.err
	}
	return &apidpb.RecordRuntimeLogEventsResponse{Inserted: int32(len(req.GetEvents()))}, nil
}

func (f *fakeRuntimeLogClient) Close() error { return nil }

func drainRuntimeLogQueue(p *runtimeLogProjector) []*apidpb.RuntimeLogEvent {
	var events []*apidpb.RuntimeLogEvent
	for {
		select {
		case event := <-p.queue:
			events = append(events, event)
		default:
			return events
		}
	}
}

func TestRuntimeLogProjectorForwardsWarnAndErrorLines(t *testing.T) {
	at := time.Date(2026, 10, 21, 12, 0, 30, 0, time.UTC)
	p := newRuntimeLogProjector(prometheus.NewRegistry(), func() time.Time { return at })
	observe, stop := p.Start("app-1", "dep-1", "vm-1")
	defer stop()
	for _, line := range []logbuf.Line{
		{Seq: 1, Stream: "stderr", Line: "[ERROR] payment declined", WrittenAt: at},
		{Seq: 2, Stream: "stdout", Line: `{"level":"warn"}`, Level: "warn", WrittenAt: at},
		{Seq: 3, Stream: "stdout", Line: "[info] listening", WrittenAt: at},
		{Seq: 4, Stream: "stdout", Line: "an error in prose is not an error line", WrittenAt: at},
		{Seq: 5, Stream: "stderr", Line: "[error] " + strings.Repeat("é", api.RuntimeLogEventMessageMaxBytes), WrittenAt: at},
	} {
		observe(line)
	}
	events := drainRuntimeLogQueue(p)
	if len(events) != 3 {
		t.Fatalf("queued %d events, want the 3 warn/error lines", len(events))
	}
	first := events[0]
	if first.GetAppId() != "app-1" || first.GetDeploymentId() != "dep-1" || first.GetInstanceId() != "vm-1" ||
		first.GetSourceEventId() != "vm-1:1" || first.GetLevel() != "error" || first.GetStream() != "stderr" ||
		first.GetOccurredAtUnixMs() != at.UnixMilli() {
		t.Fatalf("first event = %+v", first)
	}
	if events[1].GetLevel() != "warn" || events[1].GetStream() != "stdout" {
		t.Fatalf("structured warn = %+v", events[1])
	}
	if long := events[2].GetMessage(); len(long) > api.RuntimeLogEventMessageMaxBytes || !strings.HasSuffix(long, "é") {
		t.Fatalf("long line = %d bytes, want a rune-safe cut at %d", len(long), api.RuntimeLogEventMessageMaxBytes)
	}
}

func TestRuntimeLogProjectorBudgetAndDropSummary(t *testing.T) {
	at := time.Date(2026, 10, 21, 12, 0, 30, 0, time.UTC)
	now := at
	p := newRuntimeLogProjector(prometheus.NewRegistry(), func() time.Time { return now })
	observe, _ := p.Start("app-1", "dep-1", "vm-1")
	for i := range api.RuntimeLogEventsPerAppMinute + 5 {
		observe(logbuf.Line{Seq: int64(i), Line: "[error] boom", WrittenAt: at})
	}
	if got := len(drainRuntimeLogQueue(p)); got != api.RuntimeLogEventsPerAppMinute {
		t.Fatalf("queued %d, want the %d-line budget", got, api.RuntimeLogEventsPerAppMinute)
	}
	if got := testutil.ToFloat64(p.outcomes.WithLabelValues("over_budget")); got != 5 {
		t.Fatalf("over_budget = %v, want 5", got)
	}
	p.closeMinutes(at) // the minute is still open: no summary yet
	if got := len(drainRuntimeLogQueue(p)); got != 0 {
		t.Fatalf("summary before the minute closed: %d events", got)
	}
	now = at.Add(time.Minute)
	p.closeMinutes(now)
	summary := drainRuntimeLogQueue(p)
	if len(summary) != 1 || summary[0].GetStream() != "system" || summary[0].GetLevel() != "warn" ||
		!strings.HasPrefix(summary[0].GetMessage(), "5 warn/error lines") ||
		summary[0].GetSourceEventId() != "budget:app-1:"+strconv.FormatInt(at.Unix()/60, 10) {
		t.Fatalf("drop summary = %+v", summary)
	}
	if len(p.budgets) != 0 {
		t.Fatalf("closed budgets kept: %v", p.budgets)
	}
	// A new minute starts a fresh budget.
	observe(logbuf.Line{Seq: 999, Line: "[error] boom", WrittenAt: now})
	if got := len(drainRuntimeLogQueue(p)); got != 1 {
		t.Fatalf("new minute queued %d, want 1", got)
	}
}

func TestRuntimeLogProjectorSendPausesWhenApidRefuses(t *testing.T) {
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	now := at
	p := newRuntimeLogProjector(prometheus.NewRegistry(), func() time.Time { return now })
	batch := make([]*apidpb.RuntimeLogEvent, api.RuntimeLogEventsBatchMax+1)
	for i := range batch {
		batch[i] = &apidpb.RuntimeLogEvent{AppId: "app-1"}
	}
	client := &fakeRuntimeLogClient{}
	if paused := p.send(context.Background(), client, batch, time.Time{}); !paused.IsZero() {
		t.Fatalf("paused after success until %v", paused)
	}
	if len(client.batches) != 2 || len(client.batches[0]) != api.RuntimeLogEventsBatchMax {
		t.Fatalf("batches = %d (first %d), want chunks of %d", len(client.batches), len(client.batches[0]), api.RuntimeLogEventsBatchMax)
	}
	for _, code := range []codes.Code{codes.Unavailable, codes.Unimplemented} {
		client := &fakeRuntimeLogClient{err: status.Error(code, "no")}
		paused := p.send(context.Background(), client, batch[:1], time.Time{})
		if !paused.Equal(at.Add(api.RuntimeLogEventsUnavailablePause)) {
			t.Fatalf("%v: paused until %v, want %v", code, paused, at.Add(api.RuntimeLogEventsUnavailablePause))
		}
		// While paused, batches are dropped without a call.
		now = at.Add(time.Second)
		p.send(context.Background(), client, batch[:1], paused)
		if len(client.batches) != 1 {
			t.Fatalf("%v: called apid %d times while paused", code, len(client.batches))
		}
		now = at
	}
	if got := testutil.ToFloat64(p.outcomes.WithLabelValues("paused")); got != 2 {
		t.Fatalf("paused = %v, want 2", got)
	}
	client = &fakeRuntimeLogClient{err: status.Error(codes.DeadlineExceeded, "slow")}
	if paused := p.send(context.Background(), client, batch[:1], time.Time{}); !paused.IsZero() {
		t.Fatalf("a timeout paused forwarding until %v", paused)
	}
}
