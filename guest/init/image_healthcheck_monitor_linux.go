//go:build linux

// adr:644
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/healthcheckproto"
)

func handleImageHealthcheckMonitorConn(f *os.File, kind, size uint32, log *slog.Logger) {
	var req healthcheckproto.Request
	if err := healthcheckproto.ReadBody(f, size, &req); err != nil {
		return
	}
	runtime := mainImageReadiness.Load()
	if kind == healthcheckproto.ConfigProbe {
		config := imageHealthcheckConfig(req, runtime)
		_ = healthcheckproto.Write(f, healthcheckproto.ConfigAck, config)
		return
	}
	resp := healthcheckproto.Response{Nonce: req.Nonce}
	if err := req.Validate(healthcheckproto.MaxProbeBudgetMS); err != nil || req.RuntimeID == "" {
		resp.Error = "invalid_request"
	} else if runtime == nil {
		resp.Error = "runtime_unavailable"
	} else if req.RuntimeID != runtime.id {
		resp.Error = "runtime_changed"
	} else {
		resp.RuntimeID = runtime.id
		ctx, cancel := context.WithTimeout(runtime.ctx, time.Duration(req.BudgetMS)*time.Millisecond)
		defer cancelImageCheckOnDisconnect(ctx, cancel, f)()
		resp.Error = runtime.runCheck(ctx, log, true)
		interval, _, grace, _ := healthcheckDefaults(runtime.manifest.Healthcheck)
		resp.NextIntervalNS = int64(imageHealthcheckPollDelay(runtime.manifest.Healthcheck, time.Since(runtime.startedAt), interval, grace))
		if mainImageReadiness.Load() != runtime {
			resp.Error = "runtime_changed"
		}
		resp.Healthy = resp.Error == ""
	}
	_ = healthcheckproto.Write(f, healthcheckproto.CheckAck, resp)
}

func imageHealthcheckConfig(req healthcheckproto.Request, runtime *imageReadinessRuntime) healthcheckproto.Config {
	config := healthcheckproto.Config{Nonce: req.Nonce}
	if err := req.Validate(healthcheckproto.MaxProbeBudgetMS); err != nil {
		config.Error = "invalid_request"
	} else if runtime == nil {
		config.Error = "runtime_unavailable"
	} else if runtime.manifest.Healthcheck == nil {
		config.Error = "healthcheck_missing"
	} else if argv, _ := parseHealthcheckTest(runtime.manifest.Healthcheck.Test); len(argv) == 0 {
		config.Error = "healthcheck_missing"
	} else {
		interval, timeout, grace, retries := healthcheckDefaults(runtime.manifest.Healthcheck)
		interval = imageHealthcheckPollDelay(runtime.manifest.Healthcheck, time.Since(runtime.startedAt), interval, grace)
		config.RuntimeID, config.IntervalNS, config.TimeoutNS, config.Retries = runtime.id, int64(interval), int64(timeout), retries
		// Host polling replaces the unauthoritative DGRAM poll.
		runtime.monitoring.Store(true)
	}
	return config
}

// Serialize legacy telemetry with fresh checks during protocol negotiation.
// Once host monitoring owns the runtime, no legacy command can start.
func acquireLegacyImageHealthcheck(ctx context.Context) (context.Context, func(), bool) {
	runtime := mainImageReadiness.Load()
	if runtime == nil {
		return ctx, func() {}, true
	}
	probeCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(runtime.ctx, cancel) //nolint:contextcheck // Main-process retirement independently cancels legacy telemetry.
	select {
	case runtime.busy <- struct{}{}:
	case <-probeCtx.Done():
		cancel()
		stop()
		return nil, nil, false
	}
	release := func() { cancel(); stop(); <-runtime.busy }
	if runtime.monitoring.Load() || probeCtx.Err() != nil {
		release()
		return nil, nil, false
	}
	return probeCtx, release, true
}
