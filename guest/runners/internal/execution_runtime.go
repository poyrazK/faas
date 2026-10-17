package internal

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"

	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
)

// ExecutionRuntime is trusted runner wiring. Its plan comes from the bound
// host lease, not application headers, manifests or customer environment.
type ExecutionRuntime struct {
	client     *functionlifetime.Client
	mu         sync.Mutex
	background *BackgroundRuntime
	plan       api.Plan
}

func NewExecutionRuntime(dial functionlifetime.Dial) *ExecutionRuntime {
	client := functionlifetime.NewClient(dial)
	client.ObserveError = func(error) { slog.Error("function execution completion acknowledgement unavailable") }
	return &ExecutionRuntime{client: client}
}

// QualifiedExecutionRuntime is dormant in ordinary builds. The explicit
// metal,function_background_native build is for acceptance only (ADR-1542).
func QualifiedExecutionRuntime() *ExecutionRuntime {
	if !functionlifetime.NativeQualificationBuild {
		return nil
	}
	return NewExecutionRuntime(func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", functionlifetime.SocketPath)
	})
}

// WorkerSpec keeps prewarm and request dispatch in the same qualified pool.
// A nil runtime leaves ordinary production runner wiring unchanged.
func (e *ExecutionRuntime) WorkerSpec(spec workerpool.Spec) workerpool.Spec {
	if e == nil {
		return spec
	}
	if !slices.Contains(spec.Args, functionlifetime.NodeResourceArgument) {
		spec.Args = append(slices.Clone(spec.Args), functionlifetime.NodeResourceArgument)
	}
	spec.RequireResourceLifecycle = true
	return spec
}

func (e *ExecutionRuntime) serve(w http.ResponseWriter, r *http.Request, cfg ServeConfig) {
	cfg.Execution = nil
	cfg.Spec = e.WorkerSpec(cfg.Spec)
	hook := lifecycleHook(r)
	if hook == "after_restore" {
		// Restore hooks run within vmmd's boot/resume flight, before public
		// admission. The live incarnation may not have been published yet.
		serveFunctionLifecycle(w, r, cfg)
		return
	}
	lease, plan, err := e.client.Acquire(r.Context(), invocationTimeout(r.Header), hook)
	if err != nil {
		http.Error(w, "function execution ownership unavailable", http.StatusServiceUnavailable)
		return
	}
	e.mu.Lock()
	if e.background == nil || e.plan != plan {
		e.background, err = NewBackgroundRuntime(plan)
		e.plan = plan
	}
	cfg.Background = e.background
	e.mu.Unlock()
	if err != nil {
		lease.ReleaseAfter(r.Context(), nil)
		http.Error(w, "function execution policy unavailable", http.StatusServiceUnavailable)
		return
	}
	cfg.executionLease = lease
	if hook == "before_checkpoint" {
		serveFunctionLifecycle(w, r, cfg)
		return
	}
	Serve(w, r, cfg)
}

func invokeWithLease(ctx context.Context, cfg ServeConfig, env Envelope) (*invocation, error) {
	inv, err := invoke(ctx, cfg.Spec, env, cfg.Background)
	if cfg.executionLease == nil {
		return inv, err
	}
	if err != nil {
		cfg.executionLease.ReleaseAfter(ctx, inv.released)
		return inv, err
	}
	closeInvocation := inv.close
	inv.close = func(healthy bool) {
		closeInvocation(healthy)
		cfg.executionLease.ReleaseAfter(ctx, inv.released)
	}
	return inv, nil
}
