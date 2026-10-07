//go:build linux && metal

// adr: 470 — scheduler capacity survives an unconfirmed stop of a real guest.
package fcvm_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Model a failed scheduler→vmmd destroy RPC without stopping the guest. The
// successful retry uses the real Manager/JailerVMM teardown. Other routed
// operations are outside this test; their embedding fails if called unexpectedly.
type schedulerTeardownRoute struct {
	sched.RoutedVMM
	manager *fcvm.Manager
	fail    atomic.Bool
}

var errSchedulerMetalStop = errors.New("injected unconfirmed scheduler stop")

func (r *schedulerTeardownRoute) Destroy(ctx context.Context, _, instance string) error {
	if r.fail.Load() {
		return errSchedulerMetalStop
	}
	return r.manager.Destroy(ctx, instance)
}

func TestMetalSchedulerRetainsAccountingUntilConfirmedTeardown(t *testing.T) {
	for _, name := range []string{"FAAS_TEST_KERNEL", "FAAS_TEST_BASE_ROOTFS", "FAAS_TEST_LAYER_ROOTFS"} {
		if os.Getenv(name) == "" {
			t.Skipf("required %s is unset", name)
		}
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root and KVM on the dedicated Linux acceptance host")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("KVM unavailable: %v", err)
	}
	for _, cause := range []string{"liveness", "workload_oom", "force_restart", "cold_boot_timeout"} {
		t.Run(cause, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			s := state.NewMemStore()
			acct, err := s.CreateAccount(ctx, "scheduler-metal@example.com", api.PlanPro)
			mustSchedulerTeardown(t, err)
			app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "scheduler-metal", RAMMB: 512, MaxConcurrency: 1})
			mustSchedulerTeardown(t, err)
			dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:metal", Status: state.DeployLive})
			mustSchedulerTeardown(t, err)
			initial := state.StateRunning
			if cause == "cold_boot_timeout" {
				initial = state.StateColdBooting
			}
			i, err := s.CreateInstance(ctx, app.ID, dep.ID, string(initial), app.RAMMB, state.DefaultLocalNodeName, "")
			mustSchedulerTeardown(t, err)
			m := fcvm.NewAcceptanceManager(t)
			t.Cleanup(func() {
				cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
				defer done()
				if err := m.Destroy(cleanupCtx, i.ID); err != nil {
					t.Errorf("cleanup guest: %v", err)
				}
				leakcheck.AssertZero(t)
			})
			limits, _ := api.LimitsFor(api.PlanPro)
			guest, err := m.Wake(ctx, fcvm.WakeRequest{
				Instance: i.ID, AppID: app.ID, DeploymentID: dep.ID,
				BaseKey: os.Getenv("FAAS_TEST_BASE_ROOTFS"), LayerKey: os.Getenv("FAAS_TEST_LAYER_ROOTFS"),
				VcpuCount: limits.VCPU, MemSizeMiB: app.RAMMB, Plan: api.PlanPro,
			})
			mustSchedulerTeardown(t, err)
			route := &schedulerTeardownRoute{manager: m}
			route.fail.Store(true)
			newEngine := func() *sched.Engine {
				e, err := sched.NewEngine(ctx, s, sched.NewNodeLedger(), route, nil, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default())
				mustSchedulerTeardown(t, err)
				mustSchedulerTeardown(t, e.SeedLedger(ctx))
				return e
			}
			stop := func(e *sched.Engine) error {
				switch cause {
				case "liveness":
					return e.DestroyForLivenessFailure(ctx, i.ID, "liveness_timeout")
				case "workload_oom":
					return e.DestroyForWorkloadOOMFailure(ctx, i.ID, 384, 256)
				case "force_restart":
					_, err := e.ForceRestart(ctx, i.ID, "metal_accounting")
					return err
				default:
					return e.KillStuck(ctx, i.ID, app.ID, sched.StuckColdBootTimeout)
				}
			}
			e := newEngine()
			if err := stop(e); !errors.Is(err, errSchedulerMetalStop) {
				t.Fatalf("unconfirmed stop acknowledged: %v", err)
			}
			pid, ok := m.InstancePID(i.ID)
			if !ok || syscall.Kill(pid, 0) != nil {
				t.Fatal("fixture did not retain a real running Firecracker guest")
			}
			if _, err := os.Lstat(filepath.Join("/run/netns", guest.Net.Netns)); err != nil {
				t.Fatalf("unconfirmed stop lost network identity: %v", err)
			}
			assertHeld := func(e *sched.Engine) {
				fresh, err := s.InstanceByID(ctx, i.ID)
				mustSchedulerTeardown(t, err)
				if fresh.State != string(initial) || !e.Ledger().ResidentFor(i.ID) ||
					e.Ledger().ResidentRAMForNode(i.NodeID) != app.RAMMB+api.PerVMOverheadMB ||
					e.Ledger().Concurrency(app.ID) != 1 || e.Ledger().ConcurrencyForDeployment(app.ID, dep.ID) != 1 ||
					e.Ledger().UsedVCPUForNode(i.NodeID) != limits.VCPU ||
					e.Ledger().UsedCPUMillicoresForNode(i.NodeID) != api.DefaultAppCPUMillicores {
					t.Fatal("unconfirmed stop or scheduler restart lost resident accounting")
				}
				if err := e.Ledger().Admit(sched.Request{Instance: "replacement", AppID: app.ID, Plan: api.PlanPro,
					RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency, NodeID: i.NodeID}); err == nil {
					t.Fatal("admitted a replacement while the source guest remained alive")
				}
			}
			assertHeld(e)
			e = newEngine()
			assertHeld(e)
			route.fail.Store(false)
			mustSchedulerTeardown(t, stop(e))
			if _, alive := m.InstancePID(i.ID); alive || m.LiveCount() != 0 || m.LeasedCount() != 0 {
				t.Fatal("confirmed teardown retained a guest or manager lease")
			}
			if e.Ledger().ResidentRAM() != 0 || e.Ledger().UsedVCPU() != 0 || e.Ledger().Concurrency(app.ID) != 0 {
				t.Fatal("confirmed teardown retained scheduler capacity")
			}
			leakcheck.AssertZero(t)
		})
	}
}

func mustSchedulerTeardown(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
