//go:build linux && metal

// adr: 397 — durable report redelivery retries failed real-guest cleanup.
package fcvm_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmd/failureoutbox"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

func TestMetalFailureOutboxRetriesConfirmedTeardown(t *testing.T) {
	for _, name := range []string{"FAAS_TEST_KERNEL", "FAAS_TEST_BASE_ROOTFS", "FAAS_TEST_LAYER_ROOTFS"} {
		if os.Getenv(name) == "" {
			t.Skipf("required %s is unset", name)
		}
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root and dedicated Linux KVM")
	}
	for _, kind := range []string{failureoutbox.Liveness, failureoutbox.WorkloadOOM} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			s := state.NewMemStore()
			acct, err := s.CreateAccount(ctx, "failure-outbox@example.com", api.PlanPro)
			mustSchedulerTeardown(t, err)
			app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "failure-outbox", RAMMB: 512, MaxConcurrency: 1})
			mustSchedulerTeardown(t, err)
			dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:metal", Status: state.DeployLive})
			mustSchedulerTeardown(t, err)
			i, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), app.RAMMB, state.DefaultLocalNodeName, "")
			mustSchedulerTeardown(t, err)
			m := fcvm.NewAcceptanceManager(t)
			t.Cleanup(func() {
				cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
				defer done()
				if err := m.Destroy(cleanupCtx, i.ID); err != nil {
					t.Errorf("guest cleanup: %v", err)
				}
				leakcheck.AssertZero(t)
			})
			limits, _ := api.LimitsFor(api.PlanPro)
			_, err = m.Wake(ctx, fcvm.WakeRequest{Instance: i.ID, AppID: app.ID, DeploymentID: dep.ID,
				BaseKey: os.Getenv("FAAS_TEST_BASE_ROOTFS"), LayerKey: os.Getenv("FAAS_TEST_LAYER_ROOTFS"),
				VcpuCount: limits.VCPU, MemSizeMiB: app.RAMMB, Plan: api.PlanPro})
			mustSchedulerTeardown(t, err)
			route := &schedulerTeardownRoute{manager: m}
			route.fail.Store(true)
			e, err := sched.NewEngine(ctx, s, sched.NewNodeLedger(), route, nil, "1.7.0", slog.Default())
			mustSchedulerTeardown(t, err)
			mustSchedulerTeardown(t, e.SeedLedger(ctx))
			// Exercise the actual gRPC handler and its persisted outcome check.
			dir, err := os.MkdirTemp("", "fo-")
			mustSchedulerTeardown(t, err)
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			path := filepath.Join(dir, "s.sock")
			lis, err := net.Listen("unix", path)
			mustSchedulerTeardown(t, err)
			server := grpc.NewServer()
			scheddgrpc.New(e, nil, slog.Default()).WithOwner("", s).Register(server)
			go func() { _ = server.Serve(lis) }()
			t.Cleanup(func() { server.Stop(); _ = lis.Close() })
			conn, err := wire.DialContext(ctx, "unix://"+path, nil)
			mustSchedulerTeardown(t, err)
			defer func() { _ = conn.Close() }()
			cli := scheddpb.NewScheddClient(conn)
			var attempts atomic.Int32
			send := func(sendCtx context.Context, r failureoutbox.Report) error {
				var ok bool
				var err error
				if r.Kind == failureoutbox.Liveness {
					ack, callErr := cli.ReportLivenessFailed(sendCtx, &scheddpb.LivenessFailedReport{InstanceId: r.InstanceID, SourceNodeId: r.SourceNodeID, Reason: r.Reason})
					err, ok = callErr, ack.GetOk() && ack.GetApplied()
				} else {
					ack, callErr := cli.ReportWorkloadOOM(sendCtx, &scheddpb.ReportWorkloadOOMRequest{InstanceId: r.InstanceID, SourceNodeId: r.SourceNodeID, PeakMb: r.PeakMB, PlanMb: r.PlanMB})
					err, ok = callErr, ack.GetOk() && ack.GetApplied()
				}
				attempts.Add(1)
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("negative application acknowledgement")
				}
				return nil
			}
			spool := filepath.Join(dir, "spool")
			o, err := failureoutbox.Open(spool, send, slog.Default())
			mustSchedulerTeardown(t, err)
			t.Cleanup(func() { _ = o.Close() })
			mustSchedulerTeardown(t, o.Enqueue(failureoutbox.Report{InstanceID: i.ID, SourceNodeID: i.NodeID, Kind: kind, Reason: "timeout", PeakMB: 384, PlanMB: 256}))
			o.Start(ctx)
			waitFailureOutbox(t, func() bool { return attempts.Load() >= 2 })
			mustSchedulerTeardown(t, o.Close())
			pid, ok := m.InstancePID(i.ID)
			if !ok || syscall.Kill(pid, 0) != nil || !m.HasInstanceOwnership(i.ID) {
				t.Fatal("failed stop lost real guest ownership")
			}
			fresh, err := s.InstanceByID(ctx, i.ID)
			mustSchedulerTeardown(t, err)
			if fresh.State != string(state.StateRunning) || !e.Ledger().ResidentFor(i.ID) || e.Ledger().Concurrency(app.ID) != 1 || o.Pending() != 1 {
				t.Fatal("failed delivery lost report/state/admission")
			}
			// Reopen the spool while the same vmmd still owns the guest. This is
			// outbox recovery, not guest-inventory recovery after a vmmd crash.
			reopened, err := failureoutbox.Open(spool, send, slog.Default())
			mustSchedulerTeardown(t, err)
			defer func() { _ = reopened.Close() }()
			route.fail.Store(false)
			reopened.Start(ctx)
			waitFailureOutbox(t, func() bool { return reopened.Pending() == 0 })
			fresh, err = s.InstanceByID(ctx, i.ID)
			mustSchedulerTeardown(t, err)
			if fresh.State != string(state.StateStopped) || e.Ledger().ResidentRAM() != 0 || e.Ledger().Concurrency(app.ID) != 0 || m.HasInstanceOwnership(i.ID) {
				t.Fatal("confirmed retry did not complete cleanup")
			}
			leakcheck.AssertZero(t)
		})
	}
}

func waitFailureOutbox(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !check() {
		select {
		case <-deadline:
			t.Fatal("failure outbox did not progress")
		case <-tick.C:
		}
	}
}
