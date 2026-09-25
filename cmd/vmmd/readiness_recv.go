//go:build linux

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

type appReadinessProbeLoop struct {
	instance   string
	appID      string
	cfg        fcvm.ReadinessProbeConfig
	socketPath string
	events     *events.Platform
	log        *slog.Logger
	probeFn    func(context.Context, int) string
	failures   int
	ready      bool
}

func startAppReadinessProbeLoop(parent context.Context, log *slog.Logger, platform *events.Platform, instance, appID string, cfg fcvm.ReadinessProbeConfig, socketPath string) {
	loop := &appReadinessProbeLoop{
		instance:   instance,
		appID:      appID,
		cfg:        cfg,
		socketPath: socketPath,
		events:     platform,
		log:        log,
	}
	go loop.run(parent) //nolint:contextcheck // Manager supplies the daemon-owned lifecycle context.
}

func (l *appReadinessProbeLoop) run(ctx context.Context) {
	if l.cfg.PeriodSeconds <= 0 || l.cfg.FailureThreshold <= 0 {
		return
	}
	tick := time.NewTicker(time.Duration(l.cfg.PeriodSeconds) * time.Second)
	defer tick.Stop()
	// A configured instance is fail-closed until its first successful recurring
	// probe, even if the startup healthcheck admitted it moments ago.
	l.emit(ctx, "unready", "awaiting_initial_probe")
	l.runOne(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			l.runOne(ctx)
		}
	}
}

func (l *appReadinessProbeLoop) runOne(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	timeoutMs := l.cfg.TimeoutSeconds * 1000
	if timeoutMs <= 0 {
		timeoutMs = 2000
	}
	probe := &livenessProbeLoop{
		cfg: livenessProbeConfig{
			Path:        l.cfg.Path,
			GRPC:        l.cfg.GRPC,
			GRPCService: l.cfg.GRPCService,
			Port:        l.cfg.Port,
		},
		socketPath: l.socketPath,
	}
	var outcome string
	if l.probeFn != nil {
		outcome = l.probeFn(ctx, timeoutMs)
	} else {
		outcome = probe.dialAndProbe(ctx, timeoutMs)
	}
	if ctx.Err() != nil {
		return
	}
	if outcome == livenessOutcomeOK {
		l.failures = 0
		if !l.ready {
			l.emit(ctx, "ready", "probe_succeeded")
			l.ready = true
			l.log.Info("readiness: instance restored to routing", "instance", l.instance)
		}
		return
	}
	l.failures++
	if l.ready && l.failures >= l.cfg.FailureThreshold {
		l.emit(ctx, "unready", outcome)
		l.ready = false
		l.log.Warn("readiness: instance withdrawn from routing",
			"instance", l.instance, "failures", l.failures, "threshold", l.cfg.FailureThreshold, "reason", outcome)
	}
}

func (l *appReadinessProbeLoop) emit(ctx context.Context, status, reason string) {
	if l.events == nil {
		return
	}
	l.events.Emit(ctx, events.AppReadiness{
		EmitAt:     time.Now().UTC(),
		AppID:      l.appID,
		InstanceID: l.instance,
		Status:     status,
		Reason:     reason,
	})
}
