package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// SlowRequestReport is a proxied request still in flight after
// api.ProfileCaptureSlowRequestAfter (ADR-1155). Route is a label (route
// template or method), never the raw path.
type SlowRequestReport struct {
	AppID, InstanceID, RequestID, TraceID, Route string
	Inflight                                     time.Duration
}

// SlowRequestWatcher tracks proxied requests and reports ones in flight past
// a threshold so the platform can snapshot the serving instance's stacks
// while the request is stuck. Reports are best-effort and at most one per app
// per interval; schedd enforces its own per-app minimum on top.
type SlowRequestWatcher struct {
	after, every time.Duration
	report       func(context.Context, SlowRequestReport) error
	log          *slog.Logger
	now          func() time.Time

	mu        sync.Mutex
	next      uint64
	inflight  map[uint64]*trackedRequest
	lastByApp map[string]time.Time
}

type trackedRequest struct {
	SlowRequestReport
	started  time.Time
	reported bool
}

// NewSlowRequestWatcher reports through report, which should route to the
// schedd that owns the instance. A nil logger discards report failures.
func NewSlowRequestWatcher(report func(context.Context, SlowRequestReport) error, log *slog.Logger) *SlowRequestWatcher {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &SlowRequestWatcher{after: api.ProfileCaptureSlowRequestAfter, every: api.ProfileCaptureSlowRequestReportEvery,
		report: report, log: log, now: time.Now, inflight: map[uint64]*trackedRequest{}, lastByApp: map[string]time.Time{}}
}

// begin tracks one request; the returned func ends tracking.
func (w *SlowRequestWatcher) begin(r SlowRequestReport) func() {
	w.mu.Lock()
	id := w.next
	w.next++
	w.inflight[id] = &trackedRequest{SlowRequestReport: r, started: w.now()}
	w.mu.Unlock()
	return func() {
		w.mu.Lock()
		delete(w.inflight, id)
		w.mu.Unlock()
	}
}

// due marks and returns requests in flight past the threshold, one per app
// per reporting interval. Every request is considered once.
func (w *SlowRequestWatcher) due() []SlowRequestReport {
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []SlowRequestReport
	for _, t := range w.inflight {
		age := now.Sub(t.started)
		if t.reported || age < w.after {
			continue
		}
		t.reported = true
		if last, ok := w.lastByApp[t.AppID]; ok && now.Sub(last) < w.every {
			continue
		}
		w.lastByApp[t.AppID] = now
		report := t.SlowRequestReport
		report.Inflight = age
		out = append(out, report)
	}
	for app, last := range w.lastByApp {
		if now.Sub(last) >= w.every {
			delete(w.lastByApp, app)
		}
	}
	return out
}

// Run sweeps once a second until ctx ends.
func (w *SlowRequestWatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, report := range w.due() {
			go func() {
				rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				if err := w.report(rctx, report); err != nil {
					w.log.Debug("gateway: slow request report failed", "app_id", report.AppID, "instance_id", report.InstanceID, "err", err)
				}
			}()
		}
	}
}

// WithSlowRequestWatcher installs the watcher; nil disables tracking.
func (h *Handler) WithSlowRequestWatcher(w *SlowRequestWatcher) {
	h.slowRequests = w
}

// trackSlowRequest registers a proxied request with the slow-request watcher
// and returns the func that ends tracking. Streaming responses are long-lived
// by design, and plans without profiling cannot be captured, so neither is
// tracked.
func (h *Handler) trackSlowRequest(r *http.Request, app App, target Target, streaming bool) func() {
	if h.slowRequests == nil || streaming || target.InstanceID == "" || !api.MustLimitsFor(app.Plan).Profiling.Enabled {
		return func() {}
	}
	requestID := requestIDFrom(r)
	traceID := traceIDForTelemetry(r.Context())
	if traceID == "" {
		traceID = telemetryTraceID(requestID)
	}
	route := RouteLabelFrom(r)
	if route == "" {
		route = r.Method + " request"
	}
	return h.slowRequests.begin(SlowRequestReport{AppID: app.ID, InstanceID: target.InstanceID, RequestID: requestID, TraceID: traceID, Route: route})
}
