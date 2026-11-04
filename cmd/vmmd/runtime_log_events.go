package main

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/loglevel"
)

// runtimeLogProjector forwards guest warn and error lines to apid's log
// ledger (ADR-20261018083536 slice B). Its observer runs under the instance
// ring's mutex, so it only classifies, charges the app's minute budget and
// enqueues without blocking; one sender goroutine batches to apid. The
// projection is lossy by design: over budget, on a full queue or while apid
// is unavailable, lines are counted and dropped. The ring and the S3 archive
// keep every line.
type runtimeLogProjector struct {
	queue    chan *apidpb.RuntimeLogEvent
	now      func() time.Time
	outcomes *prometheus.CounterVec

	mu      sync.Mutex
	budgets map[string]*runtimeLogBudget // app id → its current minute
}

// runtimeLogBudget is one app's line budget for one UTC minute on this node.
type runtimeLogBudget struct {
	minute                   int64
	used, dropped            int
	deploymentID, instanceID string
}

func newRuntimeLogProjector(reg prometheus.Registerer, now func() time.Time) *runtimeLogProjector {
	p := &runtimeLogProjector{
		queue:   make(chan *apidpb.RuntimeLogEvent, api.RuntimeLogEventsQueueMax),
		now:     now,
		budgets: map[string]*runtimeLogBudget{},
		outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_runtime_log_events_total",
			Help: "Guest warn/error lines projected to the log_events ledger, by outcome: inserted, rejected and rate_limited by apid; over_budget, queue_full, paused and forward_failed dropped here (ADR-20261018083536).",
		}, []string{"outcome"}),
	}
	if reg != nil {
		reg.MustRegister(p.outcomes)
	}
	return p
}

// Start is the fcvm instance log observer: it binds the host-owned identity
// to each committed line of one instance.
func (p *runtimeLogProjector) Start(appID, deploymentID, instance string) (func(logbuf.Line), func()) {
	return func(line logbuf.Line) { p.observe(appID, deploymentID, instance, line) }, func() {}
}

func (p *runtimeLogProjector) observe(appID, deploymentID, instance string, line logbuf.Line) {
	level := guestLineLevel(line)
	if level != loglevel.Error && level != loglevel.Warn {
		return
	}
	at := line.WrittenAt
	if at.IsZero() {
		at = p.now()
	}
	if !p.charge(appID, deploymentID, instance, at) {
		p.outcomes.WithLabelValues("over_budget").Inc()
		return
	}
	stream := line.Stream
	if stream != "stderr" {
		stream = "stdout"
	}
	event := &apidpb.RuntimeLogEvent{
		AppId: appID, DeploymentId: deploymentID, InstanceId: instance,
		SourceEventId:    instance + ":" + strconv.FormatInt(line.Seq, 10),
		OccurredAtUnixMs: at.UnixMilli(), Level: level, Stream: stream,
		Message: truncateLogLine(line.Line, api.RuntimeLogEventMessageMaxBytes),
	}
	select {
	case p.queue <- event:
	default:
		p.outcomes.WithLabelValues("queue_full").Inc()
	}
}

// charge takes one line from the app's budget for the line's minute,
// recording the overflow for that minute's drop summary.
func (p *runtimeLogProjector) charge(appID, deploymentID, instance string, at time.Time) bool {
	minute := at.Unix() / 60
	p.mu.Lock()
	defer p.mu.Unlock()
	budget := p.budgets[appID]
	if budget == nil || minute > budget.minute {
		if budget != nil && budget.dropped > 0 {
			p.enqueueLocked(p.dropSummary(appID, budget))
		}
		budget = &runtimeLogBudget{minute: minute}
		p.budgets[appID] = budget
	}
	budget.deploymentID, budget.instanceID = deploymentID, instance
	if budget.used >= api.RuntimeLogEventsPerAppMinute {
		budget.dropped++
		return false
	}
	budget.used++
	return true
}

// closeMinutes reports and forgets every budget older than now's minute, so
// an app that stops logging still gets its drop summary and the map only
// holds apps that logged a warning or error this minute.
func (p *runtimeLogProjector) closeMinutes(now time.Time) {
	minute := now.Unix() / 60
	p.mu.Lock()
	defer p.mu.Unlock()
	for appID, budget := range p.budgets {
		if budget.minute >= minute {
			continue
		}
		if budget.dropped > 0 {
			p.enqueueLocked(p.dropSummary(appID, budget))
		}
		delete(p.budgets, appID)
	}
}

func (p *runtimeLogProjector) enqueueLocked(event *apidpb.RuntimeLogEvent) {
	select {
	case p.queue <- event:
	default:
		p.outcomes.WithLabelValues("queue_full").Inc()
	}
}

// dropSummary is the one system line per app and minute that tells a
// searcher the index is incomplete for that minute.
func (p *runtimeLogProjector) dropSummary(appID string, budget *runtimeLogBudget) *apidpb.RuntimeLogEvent {
	end := time.Unix((budget.minute+1)*60, 0).Add(-time.Millisecond)
	return &apidpb.RuntimeLogEvent{
		AppId: appID, DeploymentId: budget.deploymentID, InstanceId: budget.instanceID,
		SourceEventId:    "budget:" + appID + ":" + strconv.FormatInt(budget.minute, 10),
		OccurredAtUnixMs: end.UnixMilli(), Level: loglevel.Warn, Stream: "system",
		Message: fmt.Sprintf("%d warn/error lines this minute exceeded the %d-line search index budget on this node; gregale logs still has them",
			budget.dropped, api.RuntimeLogEventsPerAppMinute),
	}
}

// run batches queued lines to apid until ctx ends. A batch goes out when it
// is full or on the flush tick; Unavailable or Unimplemented (apid disabled,
// down or older than this vmmd) pauses forwarding instead of retrying.
func (p *runtimeLogProjector) run(ctx context.Context, client apidgrpc.RuntimeLogEventsClient) {
	ticker := time.NewTicker(api.RuntimeLogEventsFlushInterval)
	defer ticker.Stop()
	batch := make([]*apidpb.RuntimeLogEvent, 0, api.RuntimeLogEventsBatchMax)
	var pausedUntil time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-p.queue:
			if batch = append(batch, event); len(batch) < api.RuntimeLogEventsBatchMax {
				continue
			}
		case <-ticker.C:
			p.closeMinutes(p.now())
		}
		pausedUntil = p.send(ctx, client, batch, pausedUntil)
		batch = batch[:0]
	}
}

func (p *runtimeLogProjector) send(ctx context.Context, client apidgrpc.RuntimeLogEventsClient, batch []*apidpb.RuntimeLogEvent, pausedUntil time.Time) time.Time {
	if len(batch) == 0 {
		return pausedUntil
	}
	now := p.now()
	if now.Before(pausedUntil) {
		p.outcomes.WithLabelValues("paused").Add(float64(len(batch)))
		return pausedUntil
	}
	for chunk := range slices.Chunk(batch, api.RuntimeLogEventsBatchMax) {
		callCtx, cancel := context.WithTimeout(ctx, api.RuntimeLogEventsForwardTimeout)
		resp, err := client.RecordRuntimeLogEvents(callCtx, &apidpb.RecordRuntimeLogEventsRequest{Events: chunk})
		cancel()
		if err != nil {
			p.outcomes.WithLabelValues("forward_failed").Add(float64(len(chunk)))
			if code := status.Code(err); code == codes.Unavailable || code == codes.Unimplemented {
				return now.Add(api.RuntimeLogEventsUnavailablePause)
			}
			continue
		}
		p.outcomes.WithLabelValues("inserted").Add(float64(resp.GetInserted()))
		p.outcomes.WithLabelValues("rejected").Add(float64(resp.GetRejected()))
		p.outcomes.WithLabelValues("rate_limited").Add(float64(resp.GetRateLimited()))
	}
	return pausedUntil
}

// truncateLogLine cuts s to at most n bytes on a rune boundary.
func truncateLogLine(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// startRuntimeLogEvents installs the projector on the jailer when
// FAAS_RUNTIME_LOG_EVENTS_ENABLED=1. It must run before the manager registers
// any ring, so recovered instances are observed too. The sender dials the
// apid target guest traces use.
func startRuntimeLogEvents(ctx context.Context, log *slog.Logger, getenv func(string) string, reg prometheus.Registerer, jailer *fcvm.JailerVMM) (func(), error) {
	if getenv("FAAS_RUNTIME_LOG_EVENTS_ENABLED") != "1" {
		return func() {}, nil
	}
	target, tlsCfg, err := traceSpansWriterTarget(getenv)
	if err != nil {
		return nil, fmt.Errorf("runtime log events: %w", err)
	}
	client, err := apidgrpc.DialRuntimeLogEvents(ctx, target, tlsCfg)
	if err != nil {
		return nil, err
	}
	projector := newRuntimeLogProjector(reg, time.Now)
	jailer.WithInstanceLogObserver(projector.Start)
	go projector.run(ctx, client)
	log.Info("runtime log events forwarding to apid", "target", target, "mtls", tlsCfg != nil,
		"per_app_minute", api.RuntimeLogEventsPerAppMinute)
	return func() { _ = client.Close() }, nil
}
