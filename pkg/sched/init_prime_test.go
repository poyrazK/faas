package sched_test

// adr: 20261114153000 — a deployment's first init snapshot is primed with a
// short GET burst on its HTTP readiness path; priming never fails a deploy.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// primingVMM adds the priming capability to the lifecycle fake and records
// whether the burst came before the capture.
type primingVMM struct {
	*sched.FakeVMM
	mu               sync.Mutex
	calls            int
	path             string
	requests         int
	budget           time.Duration
	snapshotsAtPrime int
	rep              sched.PrimeReport
	err              error
}

func (p *primingVMM) PrimeInstance(_ context.Context, _, _, path string, requests int, budget time.Duration) (sched.PrimeReport, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.path, p.requests, p.budget = path, requests, budget
	_, p.snapshotsAtPrime, _ = p.XBootCounts()
	return p.rep, p.err
}

func TestInitSnapshotPrime_OutcomesNeverFailTheCapture(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rep     sched.PrimeReport
		err     error
		outcome string
	}{
		{name: "primed", rep: sched.PrimeReport{Sent: 20, OK: 20}, outcome: wire.InitSnapshotPrimePrimed},
		{name: "partial burst still primes", rep: sched.PrimeReport{Sent: 3, OK: 1}, outcome: wire.InitSnapshotPrimePrimed},
		{name: "no 2xx", rep: sched.PrimeReport{Sent: 20}, outcome: wire.InitSnapshotPrimeNoSuccess},
		{name: "older vmmd", err: api.NewProblem(501, api.CodeNotImplemented, "Prime unavailable", "old"), outcome: wire.InitSnapshotPrimeUnsupported},
		{name: "rpc failure", err: errors.New("instance is not live"), outcome: wire.InitSnapshotPrimeFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 3)
			vmm := &primingVMM{FakeVMM: &sched.FakeVMM{}, rep: tc.rep, err: tc.err}
			ops := wire.NewOpsMetrics("schedd")
			e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(ops)

			if err := e.Prime(ctx, app.ID, dep.ID); err != nil {
				t.Fatalf("Prime: %v", err)
			}
			if vmm.calls != 1 {
				t.Fatalf("prime calls = %d, want 1", vmm.calls)
			}
			if vmm.path != "/" || vmm.requests != sched.InitPrimeRequests || vmm.budget != sched.InitPrimeBudget {
				t.Fatalf("prime request = %q/%d/%s, want ///%d/%s", vmm.path, vmm.requests, vmm.budget, sched.InitPrimeRequests, sched.InitPrimeBudget)
			}
			if vmm.snapshotsAtPrime != 0 {
				t.Fatalf("snapshots before prime = %d, want the burst before the capture", vmm.snapshotsAtPrime)
			}
			if _, snapshots, _ := vmm.XBootCounts(); snapshots != 1 {
				t.Fatalf("snapshots = %d, want the capture to run whatever priming did", snapshots)
			}
			body := renderPrimeMetrics(t, ops)
			if want := `schedd_init_snapshot_prime_total{outcome="` + tc.outcome + `"} 1`; !strings.Contains(body, want) {
				t.Fatalf("metrics missing %s", want)
			}
		})
	}
}

// A later park reuses or recaptures the snapshot after real traffic; only the
// deployment's prime capture is primed.
func TestInitSnapshotPrime_OnlyAtThePrimeCapture(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanPro, 256, 3)
	vmm := &primingVMM{FakeVMM: &sched.FakeVMM{}, rep: sched.PrimeReport{Sent: 20, OK: 20}}
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	wake, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Park(ctx, wake.InstanceID); err != nil {
		t.Fatal(err)
	}
	if vmm.calls != 0 {
		t.Fatalf("prime calls on park = %d, want 0", vmm.calls)
	}
}

// An engine whose VMM lacks the capability captures unprimed.
func TestInitSnapshotPrime_UnsupportedVMM(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 3)
	vmm := &sched.FakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	if err := e.Prime(ctx, app.ID, dep.ID); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if want := `schedd_init_snapshot_prime_total{outcome="unsupported"} 1`; !strings.Contains(renderPrimeMetrics(t, ops), want) {
		t.Fatalf("metrics missing %s", want)
	}
}

func TestInitPrimePath(t *testing.T) {
	for _, tc := range []struct {
		name string
		dep  state.Deployment
		app  state.App
		want string
	}{
		// prod 2026-11-19: a function's handler ran 20 times per deploy.
		{name: "function is not primed", dep: state.Deployment{Handler: "handler.handler"}, want: ""},
		{name: "config-in-code function is not primed", app: state.App{Type: state.AppTypeFunction}, want: ""},
		{name: "function with warmup is primed", dep: state.Deployment{Handler: "handler.handler"}, app: state.App{Manifest: state.AppManifest{Warmup: &api.WarmupHook{Path: "/warm"}}}, want: "/warm"},
		{name: "TCP readiness primes /", want: "/"},
		{name: "image health path", app: state.App{Manifest: state.AppManifest{Healthz: "/status"}}, want: "/status"},
		{name: "http2 app with TCP readiness is not primed", app: state.App{AppProtocol: api.AppProtocolHTTP2}, want: ""},
		{name: "declared warmup wins", app: state.App{Manifest: state.AppManifest{Warmup: &api.WarmupHook{Path: "/work"}}}, want: "/work"},
		{name: "empty warmup falls back", app: state.App{Manifest: state.AppManifest{Warmup: &api.WarmupHook{}}}, want: "/"},
		{name: "grpc app is not primed", app: state.App{AppProtocol: api.AppProtocolGRPC}, want: ""},
		{name: "grpc app with warmup is primed", app: state.App{AppProtocol: api.AppProtocolGRPC, Manifest: state.AppManifest{Warmup: &api.WarmupHook{Path: "/warm"}}}, want: "/warm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sched.XInitPrimePath(tc.dep, tc.app); got != tc.want {
				t.Fatalf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

func renderPrimeMetrics(t *testing.T, metrics *wire.OpsMetrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", rec.Code)
	}
	return rec.Body.String()
}
