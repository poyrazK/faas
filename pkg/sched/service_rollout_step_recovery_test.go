// adr: 208
package sched_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceRolloutStepRestartWaitsForRoutesAndSelectedReplicaDrain(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			f := newRolloutRecoveryFixture(t, s, 2, false)
			ctx := t.Context()
			limit := 2
			if _, err := s.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{MaxConcurrency: &limit}); err != nil {
				t.Fatal(err)
			}
			old := []state.Instance{f.running(t, f.old), f.running(t, f.old)}
			f.running(t, f.next)
			steps := s.(state.ServiceRolloutStepStore)
			if _, err := steps.BeginServiceRolloutStep(ctx, f.next.ID, 50); err != nil {
				t.Fatal(err)
			}
			// Reproduce a crash immediately after publishing a smaller old weight,
			// before the explicit withdrawal has been recorded.
			role, url := "compute-only", "tcp://step-gateway.internal:8080"
			node, err := s.UpsertComputeNode(ctx, state.ComputeNode{Name: "step-gateway", Role: &role, GatewayTargetURL: &url, TargetURL: "unix:///tmp/step-vmmd.sock", VPCPUs: 4, VCPUBudget: 16, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4096, Active: true, Lifecycle: state.NodeLifecycleActive})
			if err != nil {
				t.Fatal(err)
			}
			engine := func(notifier sched.Notifier) *sched.Engine {
				t.Helper()
				e, err := sched.NewEngine(ctx, s, sched.NewNodeLedger(), f.vm, notifier, "1.10.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
				if err != nil {
					t.Fatal(err)
				}
				*e.ServiceReconcileSubmit() = func(context.Context, string) {}
				if err := e.SeedLedger(ctx); err != nil {
					t.Fatal(err)
				}
				return e
			}
			blocked := engine(rolloutRecoveryNotifier{})
			blocked.ReconcileServiceDeployment(ctx, f.next.ID)
			pending, err := s.DeploymentByID(ctx, f.next.ID)
			if err != nil || len(pending.ServiceRolloutHandoff.RetiringInstanceIDs) != 1 {
				t.Fatalf("restart bypassed durable withdrawal: %+v %v", pending, err)
			}
			retiring := pending.ServiceRolloutHandoff.RetiringInstanceIDs[0]
			assertResident := func() {
				t.Helper()
				ins, err := s.InstanceByID(ctx, retiring)
				if err != nil || ins.State != "draining" || !state.State(ins.State).CountsForRAM() {
					t.Fatalf("replica retired before barriers: %+v %v", ins, err)
				}
			}
			assertResident()
			if _, parks := f.vm.counts(); parks != 0 {
				t.Fatal("smaller weight triggered an ordinary surplus park")
			}
			n := &rolloutRouteNotifier{events: make(chan db.Notification, 16)}
			routes := make(chan db.DeploymentRouteChangedPayload, 1)
			var acknowledge atomic.Bool
			n.route = func(_ context.Context, p db.DeploymentRouteChangedPayload) {
				select {
				case routes <- p:
				default:
				}
				if acknowledge.Load() {
					body, _ := json.Marshal(db.DeploymentRouteAckPayload{Node: node.Name, Generation: p.Generation})
					select {
					case n.events <- db.Notification{Channel: db.NotifyDeploymentRouteAck, Payload: string(body)}:
					default:
					}
				}
			}
			restarted := engine(n)
			// A replacement owner retains the durable recovery cooldown.
			*restarted.Now() = func() time.Time {
				return time.Now().Add(time.Duration(api.ServiceRecoveryRetryBaseSeconds+1) * time.Second)
			}
			stale := time.Now().Add(-time.Second)
			restarted.NodeTelemetryCache().Replace(f.node.ID, stale, stale, []sched.NodeTelemetry{{InstanceID: retiring}})
			done := make(chan struct{})
			go func() { restarted.ReconcileServiceDeployment(ctx, f.next.ID); close(done) }()
			var p db.DeploymentRouteChangedPayload
			select {
			case p = <-routes:
			case <-time.After(5 * time.Second):
				t.Fatal("no withdrawal route generation")
			}
			assertResident()
			acknowledge.Store(true)
			body, _ := json.Marshal(db.DeploymentRouteAckPayload{Node: node.Name, Generation: p.Generation})
			n.events <- db.Notification{Channel: db.NotifyDeploymentRouteAck, Payload: string(body)}
			// An ACK with only pre-ACK zero telemetry cannot release the VM.
			select {
			case <-done:
				t.Fatal("stale telemetry completed withdrawal")
			case <-time.After(150 * time.Millisecond):
			}
			assertResident()
			sample := func(busy bool) {
				now := time.Now()
				var rows []sched.NodeTelemetry
				for _, ins := range old {
					inflight := int64(1) // Retained old replicas keep serving requests.
					if ins.ID == retiring && !busy {
						inflight = 0
					}
					rows = append(rows, sched.NodeTelemetry{InstanceID: ins.ID, InflightRequests: inflight})
				}
				restarted.NodeTelemetryCache().Replace(f.node.ID, now, now, rows)
			}
			sample(true)
			select {
			case <-done:
				t.Fatal("in-flight request completed withdrawal")
			case <-time.After(150 * time.Millisecond):
			}
			assertResident()
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			deadline := time.NewTimer(6 * time.Second)
			defer deadline.Stop()
			for {
				sample(false)
				select {
				case <-done:
					ins, err := s.InstanceByID(ctx, retiring)
					if err != nil || ins.State != "stopped" {
						t.Fatalf("withdrawal did not finish: %+v %v", ins, err)
					}
					return
				case <-deadline.C:
					t.Fatal("quiet selected replica did not drain while retained replica served requests")
				case <-ticker.C:
				}
			}
		})
	}
}
