package sched

// Init snapshot priming (ADR-20261114153000). A deployment's first init
// snapshot is captured right after readiness, before the app has served a
// request, so every wake of it runs each code path for the first time again
// (on the JVM that cost 0.5-0.8 s per wake in the 2026-11-09 benchmark,
// docs/ops/evidence/20261109-warm-vs-init-snapshot). Before that prime capture
// schedd asks vmmd to GET the app's readiness path a few times. Priming never
// fails a deploy: an older vmmd, an app without an HTTP readiness path, or a
// burst without one 2xx response captures the snapshot unprimed and warns.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Init snapshot priming burst: the GETs schedd asks for and the wall-clock
// budget vmmd enforces (ADR-20261114153000).
const (
	InitPrimeRequests = 20
	InitPrimeBudget   = 5 * time.Second
)

// PrimeReport is one priming burst as vmmd reports it.
type PrimeReport struct {
	Sent     int
	OK       int
	Duration time.Duration
}

// InitPrimeVMM is the additive priming capability. Separate from RoutedVMM so
// older clients and test doubles keep serving the lifecycle surface; an
// engine whose VMM lacks it captures unprimed.
type InitPrimeVMM interface {
	PrimeInstance(ctx context.Context, nodeID, instance, path string, requests int, budget time.Duration) (PrimeReport, error)
}

// primeInitSnapshot runs the priming burst before a prime capture and returns
// the outcome it recorded. It never returns an error: every failure leaves
// the guest as readiness left it, and the capture goes ahead.
func (e *Engine) primeInitSnapshot(ctx context.Context, ins state.Instance, dep state.Deployment, app state.App) string {
	outcome, rep, err := e.runInitPrime(ctx, ins, dep, app)
	e.ops.RecordInitSnapshotPrime(outcome)
	switch outcome {
	case wire.InitSnapshotPrimePrimed:
		e.log.Info("sched: primed init snapshot",
			"instance", ins.ID, "deployment", ins.DeploymentID,
			"sent", rep.Sent, "ok", rep.OK, "duration_ms", rep.Duration.Milliseconds())
	case wire.InitSnapshotPrimeNoHTTPPath:
	default:
		e.log.Warn("sched: init snapshot captured unprimed",
			"instance", ins.ID, "deployment", ins.DeploymentID, "outcome", outcome,
			"sent", rep.Sent, "ok", rep.OK, "err", err)
	}
	return outcome
}

func (e *Engine) runInitPrime(ctx context.Context, ins state.Instance, dep state.Deployment, app state.App) (string, PrimeReport, error) {
	path := initPrimePath(dep, app)
	if path == "" {
		return wire.InitSnapshotPrimeNoHTTPPath, PrimeReport{}, nil
	}
	p, ok := e.vmm.(InitPrimeVMM)
	if !ok {
		return wire.InitSnapshotPrimeUnsupported, PrimeReport{}, nil
	}
	// The vmmd budget bounds the burst; the extra second covers the RPC.
	primeCtx, cancel := context.WithTimeout(ctx, InitPrimeBudget+time.Second)
	defer cancel()
	rep, err := p.PrimeInstance(primeCtx, e.nodeForRoute(ins.NodeID), ins.ID, path, InitPrimeRequests, InitPrimeBudget)
	switch {
	case isNotImplemented(err):
		return wire.InitSnapshotPrimeUnsupported, rep, err
	case err != nil:
		return wire.InitSnapshotPrimeFailed, rep, err
	case rep.OK == 0:
		return wire.InitSnapshotPrimeNoSuccess, rep, nil
	}
	return wire.InitSnapshotPrimePrimed, rep, nil
}

// initPrimePath picks the path the burst GETs, in order: the app's declared
// lifecycle.warmup path; its HTTP readiness path; the image's declared health
// path; "/" for an HTTP/1 app whose readiness is only a TCP connect (most
// apps declare no health path). gRPC readiness, an HTTP/2 or gRPC app, and a
// port routed as a raw-TCP listener get "" and are not primed.
func initPrimePath(dep state.Deployment, app state.App) string {
	if app.Manifest.Warmup != nil && app.Manifest.Warmup.Path != "" {
		return app.Manifest.Warmup.Path
	}
	// Every path of a function is its handler, so an undeclared burst would
	// run customer code 20 times per deploy with a synthetic event and no
	// invocation ID, and capture the state it left (prod 2026-11-19). A
	// function primes only the path it declares in lifecycle.warmup. A
	// function discovered from config in code deploys with no Handler, so the
	// app type decides too (prod 2026-11-20: a queue function ran up to ~150
	// synthetic GET / events per new instance).
	if dep.Handler != "" || app.Type == state.AppTypeFunction {
		return ""
	}
	path, grpc, _ := startupProbeForApp(dep, app)
	switch {
	case path != "":
		return path
	case grpc:
		return ""
	case app.AppProtocol != "" && app.AppProtocol != api.AppProtocolHTTP1:
		return ""
	case routesRawTCPListener(dep, app):
		return ""
	case strings.HasPrefix(app.Manifest.Healthz, "/"):
		return app.Manifest.Healthz
	}
	return "/"
}

func isNotImplemented(err error) bool {
	var problem *api.Problem
	return errors.As(err, &problem) && problem.Code == api.CodeNotImplemented
}
