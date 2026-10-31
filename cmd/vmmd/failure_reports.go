// adr: 471
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmd/failureoutbox"
	"github.com/onebox-faas/faas/pkg/wire"
)

func sendFailureReport(ctx context.Context, target string, tlsConfig *tls.Config, r failureoutbox.Report) error {
	conn, err := wire.DialContext(ctx, target, tlsConfig)
	if err != nil {
		return fmt.Errorf("vmmd: dial failure-report recipient: %w", err)
	}
	defer func() { _ = conn.Close() }()
	cli := scheddpb.NewScheddClient(conn)
	var ok bool
	switch r.Kind {
	case failureoutbox.Liveness:
		ack, callErr := cli.ReportLivenessFailed(ctx, &scheddpb.LivenessFailedReport{InstanceId: r.InstanceID, SourceNodeId: r.SourceNodeID, Reason: r.Reason})
		err, ok = callErr, ack.GetOk() && ack.GetApplied()
	case failureoutbox.WorkloadOOM:
		ack, callErr := cli.ReportWorkloadOOM(ctx, &scheddpb.ReportWorkloadOOMRequest{InstanceId: r.InstanceID, SourceNodeId: r.SourceNodeID, PeakMb: r.PeakMB, PlanMb: r.PlanMB})
		err, ok = callErr, ack.GetOk() && ack.GetApplied()
	default:
		return errors.New("vmmd: unknown failure-report kind")
	}
	if err != nil {
		return fmt.Errorf("vmmd: deliver failure report: %w", err)
	}
	if !ok {
		return errors.New("vmmd: failure report awaiting scheduler application")
	}
	return nil
}

// scheddClientTLS is read on every delivery attempt rather than captured
// here: run() wires the outbox before Wake RPCs are served (ADR-471), which
// is before the schedd client mTLS material is loaded. A captured nil made
// every report to a tcp:// schedd fail with "mTLS required" forever.
func wireFailureReports(sourceNodeID string, mgr *fcvm.Manager, cfg *Config, deps runDeps, scheddClientTLS func() *tls.Config, log *slog.Logger) (*failureoutbox.Outbox, error) {
	if deps.scheddTarget == "" {
		return nil, nil
	}
	if sourceNodeID == "" {
		return nil, errors.New("vmmd: durable failure reporting requires a resolved compute node ID")
	}
	outbox, err := failureoutbox.Open(cfg.FailureReportDir, ownedFailureReportSender(mgr, deps.scheddTarget, scheddClientTLS), log)
	if err != nil {
		return nil, fmt.Errorf("vmmd: open failure outbox: %w", err)
	}
	enqueue := func(r failureoutbox.Report) {
		r.SourceNodeID = sourceNodeID
		if err := outbox.Enqueue(r); err != nil {
			log.Error("vmmd: failure report persistence pending", "instance_id", r.InstanceID, "kind", r.Kind, "err", err)
		}
	}
	mgr.WithLivenessSink(func(_ context.Context, instanceID, reason string) {
		enqueue(failureoutbox.Report{InstanceID: instanceID, Kind: failureoutbox.Liveness, Reason: reason})
	})
	mgr.WithWorkloadOOMSink(func(_ context.Context, instanceID string, peakMB, planMB int) {
		enqueue(failureoutbox.Report{InstanceID: instanceID, Kind: failureoutbox.WorkloadOOM, PeakMB: uint32(peakMB), PlanMB: uint32(planMB)})
	})
	mgr.WithMemoryPressureSink(memoryPressureRelay(sourceNodeID, deps.scheddTarget, scheddClientTLS, log))
	log.Info("vmmd: durable failure reports wired", "target", deps.scheddTarget, "pending", outbox.Pending())
	return outbox, nil
}

// memoryPressureLimiter bounds the relays a guest's memory-pressure frames
// start. guest-init reports once per episode, but the guest writes the frame:
// without a bound, a tenant looping on it made vmmd open a goroutine and a
// TLS connection to schedd per frame.
type memoryPressureLimiter struct {
	mu       sync.Mutex
	last     map[string]time.Time
	interval time.Duration
	slots    chan struct{}
}

func newMemoryPressureLimiter(interval time.Duration, inFlight int) *memoryPressureLimiter {
	return &memoryPressureLimiter{last: map[string]time.Time{}, interval: interval, slots: make(chan struct{}, inFlight)}
}

// admit reserves a relay slot for instanceID's report at now, or reports
// false when the instance reported within the interval or every slot is
// busy. An admitted caller calls done when its relay finishes.
func (l *memoryPressureLimiter) admit(instanceID string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if prev, ok := l.last[instanceID]; ok && now.Sub(prev) < l.interval {
		return false
	}
	select {
	case l.slots <- struct{}{}:
	default:
		return false
	}
	l.last[instanceID] = now
	if len(l.last) > 4096 {
		for id, at := range l.last {
			if now.Sub(at) >= l.interval {
				delete(l.last, id)
			}
		}
	}
	return true
}

func (l *memoryPressureLimiter) done() { <-l.slots }

// memoryPressureRelay forwards guest memory-pressure reports to schedd
// (ADR-1155). They are advisory: a lost or rate-limited report only means no
// automatic heap capture, and nothing is spooled.
func memoryPressureRelay(sourceNodeID, target string, tlsConfig func() *tls.Config, log *slog.Logger) fcvm.MemoryPressureSink {
	limiter := newMemoryPressureLimiter(api.ProfileMemoryPressureRelayMinInterval, api.ProfileMemoryPressureRelayInFlight)
	return func(ctx context.Context, instanceID string, availableMB, totalMB, anonMB int) {
		if !limiter.admit(instanceID, time.Now()) {
			return
		}
		// The relay outlives the report's caller.
		ctx = context.WithoutCancel(ctx)
		go func() {
			defer limiter.done()
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			var cfg *tls.Config
			if tlsConfig != nil {
				cfg = tlsConfig()
			}
			conn, err := wire.DialContext(ctx, target, cfg)
			if err != nil {
				log.Warn("vmmd: memory pressure relay dial failed", "instance_id", instanceID, "err", err)
				return
			}
			defer func() { _ = conn.Close() }()
			ack, err := scheddpb.NewScheddClient(conn).ReportMemoryPressure(ctx, &scheddpb.ReportMemoryPressureRequest{
				InstanceId: instanceID, SourceNodeId: sourceNodeID,
				AvailableMb: clampMB(availableMB), TotalMb: clampMB(totalMB), AnonMb: clampMB(anonMB),
			})
			if err != nil {
				log.Warn("vmmd: memory pressure relay failed", "instance_id", instanceID, "err", err)
				return
			}
			log.Info("vmmd: memory pressure reported", "instance_id", instanceID, "available_mb", availableMB,
				"total_mb", totalMB, "capture_queued", ack.GetQueued(), "reason", ack.GetReason())
		}()
	}
}

func clampMB(v int) uint32 {
	if v < 0 {
		return 0
	}
	if v > 1<<20 {
		return 1 << 20
	}
	return uint32(v)
}

// Remember reconciliation for this Manager lifetime: after a confirmed destroy
// an acknowledgement can be lost, and the next retry must still reach schedd.
// A fresh Manager never inherits this permission from an old daemon's spool.
func ownedFailureReportSender(mgr *fcvm.Manager, target string, tlsConfig func() *tls.Config) failureoutbox.Sender {
	var reconciled sync.Map
	return func(ctx context.Context, r failureoutbox.Report) error {
		if r.Recovered {
			if _, ok := reconciled.Load(r.InstanceID); !ok {
				if !mgr.HasInstanceOwnership(r.InstanceID) {
					return errors.New("vmmd: recovered failure report requires guest ownership reconciliation")
				}
				reconciled.Store(r.InstanceID, struct{}{})
			}
		}
		var cfg *tls.Config
		if tlsConfig != nil {
			cfg = tlsConfig()
		}
		return sendFailureReport(ctx, target, cfg, r)
	}
}
