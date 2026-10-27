package realtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeUsageReporter struct {
	mu      sync.Mutex
	fail    error
	reports []state.RealtimeUsageReport
}

func (f *fakeUsageReporter) ReportRealtimeUsage(_ context.Context, r state.RealtimeUsageReport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.reports = append(f.reports, r)
	return nil
}

func (f *fakeUsageReporter) take() []state.RealtimeUsageReport {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.reports
	f.reports = nil
	return out
}

// usageFixture is one endpoint with live v1 sockets on a manager whose usage
// recorder is driven by hand.
type usageFixture struct {
	m        *Manager
	hooks    *testHooks
	endpoint string
	url      string
}

func newUsageFixture(t *testing.T, cfg Config) *usageFixture {
	t.Helper()
	hooks := &testHooks{accept: true}
	m := NewManager(cfg, hooks)
	endpoint := uuid.NewString()
	if err := m.RegisterEndpoint(Endpoint{Preview: true, ID: endpoint, AppID: "app-1", AccountID: "acct-1",
		Authorize: func(context.Context, *http.Request) (string, error) { return "", nil }}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { _ = m.Close(); server.Close() })
	return &usageFixture{m: m, hooks: hooks, endpoint: endpoint, url: "ws" + server.URL[len("http"):] + ManagedPathPrefix + endpoint}
}

func (f *usageFixture) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	client, response, err := websocket.DefaultDialer.Dial(f.url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestUsageRecorderCountsAndReportsHours(t *testing.T) {
	f := newUsageFixture(t, Config{})
	a, b := f.dial(t), f.dial(t)
	waitFor(t, "two connections", func() bool { return len(f.m.Snapshot()) == 2 })
	for _, c := range f.m.Snapshot() {
		if err := f.m.Subscribe(c.ID, "alerts"); err != nil {
			t.Fatal(err)
		}
	}
	if status, err := f.m.PublishWithStatus(t.Context(), f.endpoint, "alerts", Message{Data: []byte("hi")}); err != nil || status.Queued != 2 {
		t.Fatalf("publish = %+v, %v", status, err)
	}
	for _, c := range []*websocket.Conn{a, b} {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, data, err := c.ReadMessage(); err != nil || string(data) != "hi" {
			t.Fatalf("read = %q, %v", data, err)
		}
	}
	if err := a.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "message callback", func() bool {
		f.hooks.mu.Lock()
		defer f.hooks.mu.Unlock()
		return len(f.hooks.messages) == 1
	})

	reporter := &fakeUsageReporter{}
	start := time.Date(2026, 10, 14, 10, 0, 30, 0, time.UTC)
	clock := start
	r := &usageRecorder{m: f.m, reporter: reporter, incarnation: uuid.NewString(), now: func() time.Time { return clock },
		hours: make(map[time.Time]*usageHour), done: make(chan struct{}), lastSample: start}

	clock = start.Add(10 * time.Second) // 10:00:40, minute 0
	r.sample(clock)
	_ = b.Close()
	waitFor(t, "one connection", func() bool { return len(f.m.Snapshot()) == 1 })
	clock = start.Add(70 * time.Second) // 10:01:40, minute 1
	r.sample(clock)
	r.report(t.Context())
	reports := reporter.take()
	if len(reports) != 1 || len(reports[0].Endpoints) != 1 || !reports[0].Hour.Equal(start.Truncate(time.Hour)) {
		t.Fatalf("reports = %+v", reports)
	}
	got := reports[0].Endpoints[0]
	if got.EndpointID != f.endpoint || got.MinutePeakConnections[0] != 2 || got.MinutePeakConnections[1] != 2 || got.MinutePeakConnections[2] != 0 {
		t.Fatalf("peaks = %v, want 2 in minutes 0 and 1", got.MinutePeakConnections[:3])
	}
	// 2 connections for 10 s, then 1 for 60 s.
	if got.ConnectionSeconds != 80 || got.DeliveredMessages != 2 || got.DeliveredBytes != 4 || got.ReceivedMessages != 1 || got.ReceivedBytes != 5 {
		t.Fatalf("usage = %+v", got)
	}

	// The next hour starts a new row; a failed report keeps both hours.
	clock = time.Date(2026, 10, 14, 11, 0, 10, 0, time.UTC)
	r.sample(clock)
	reporter.fail = errors.New("apid unavailable")
	r.report(t.Context())
	if f.m.usageReportFailures.Load() != 1 || len(r.hours) != 2 {
		t.Fatalf("after failure: failures=%d held=%d", f.m.usageReportFailures.Load(), len(r.hours))
	}
	reporter.fail = nil
	r.report(t.Context())
	reports = reporter.take()
	if len(reports) != 2 || !reports[0].Hour.Before(reports[1].Hour) {
		t.Fatalf("retry reports = %+v, want both hours oldest first", reports)
	}
	// Time since the previous sample counts toward the sample's hour; samples
	// are a second apart in production, so at most a second crosses over.
	if c, n := reports[0].Endpoints[0].ConnectionSeconds, reports[1].Endpoints[0].ConnectionSeconds; c != 80 || n != 3510 {
		t.Fatalf("connection seconds = %d then %d, want 80 then 3510", c, n)
	}
	if len(r.hours) != 1 || f.m.usageHeldHours.Load() != 1 {
		t.Fatalf("held = %d, want only the current hour", len(r.hours))
	}

	// A completed hour that cannot be delivered is dropped after the age limit.
	reporter.fail = errors.New("apid unavailable")
	clock = clock.Add(26 * time.Hour)
	r.report(t.Context())
	if f.m.usageDroppedHours.Load() != 1 || len(r.hours) != 0 {
		t.Fatalf("dropped=%d held=%d, want the stale hour dropped", f.m.usageDroppedHours.Load(), len(r.hours))
	}
}

func TestUsageRecorderReportsPeriodicallyAndOnClose(t *testing.T) {
	reporter := &fakeUsageReporter{}
	f := newUsageFixture(t, Config{UsageReporter: reporter, UsageSampleInterval: 5 * time.Millisecond, UsageReportInterval: 20 * time.Millisecond})
	f.dial(t)
	waitFor(t, "a report with the connection", func() bool {
		for _, r := range reporter.take() {
			for _, e := range r.Endpoints {
				if e.EndpointID == f.endpoint && e.MinutePeakConnections[time.Now().UTC().Minute()] >= 1 {
					return true
				}
			}
		}
		return false
	})
	if err := f.m.Close(); err != nil {
		t.Fatal(err)
	}
	if final := reporter.take(); len(final) == 0 {
		t.Fatal("Close did not send a final report")
	}
	if stats := f.m.Stats(); stats.UsageReports == 0 || stats.UsageReportFailures != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestUsageRecorderIgnoresControlFrames(t *testing.T) {
	if usagePayloadFrames["subscribed"] || usagePayloadFrames["presence"] || usagePayloadFrames["error"] || !usagePayloadFrames["message"] {
		t.Fatal("payload frame set changed: only customer payload frames may count as delivered messages")
	}
}
