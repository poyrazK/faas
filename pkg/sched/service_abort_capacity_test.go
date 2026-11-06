package sched_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceAbortPreservesLastServingReplica(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			for _, oldAvailable := range []bool{true, false} {
				t.Run(fmt.Sprint(oldAvailable), func(t *testing.T) {
					f := newRolloutRecoveryFixture(t, s, 2, false)
					ctx := t.Context()
					o1, o2, n1, survivor := f.running(t, f.old), f.running(t, f.old), f.running(t, f.next), f.running(t, f.next)
					if _, err := s.BeginServiceRolloutCutover(ctx, f.next.ID); err != nil {
						t.Fatal(err)
					}
					for _, i := range []state.Instance{n1, o1, o2} {
						if i.DeploymentID == f.old.ID && oldAvailable {
							continue
						}
						if err := s.UpdateInstanceState(ctx, i.ID, "stopped"); err != nil {
							t.Fatal(err)
						}
					}
					rolloutRecoveryReconcile(t, f.engine, f.next.ID)
					d, err := s.DeploymentByID(ctx, f.next.ID)
					if err != nil {
						t.Fatal(err)
					}
					old, err := s.DeploymentByID(ctx, f.old.ID)
					if err != nil {
						t.Fatal(err)
					}
					instance, err := s.InstanceByID(ctx, survivor.ID)
					if err != nil {
						t.Fatal(err)
					}
					boots, parks := f.vm.counts()
					if oldAvailable {
						if d.RolloutState != "aborted" || old.TrafficPercent != 100 {
							t.Fatalf("healthy reverse did not finish: candidate=%+v old=%+v", d, old)
						}
					} else {
						if d.TrafficPercent != 100 || old.TrafficPercent != 0 || instance.State != "running" || !d.ServiceRolloutHandoff.ActiveAbort() || boots == 0 || parks != 0 {
							t.Fatalf("last serving replica lost before recipient recovery: candidate=%+v old=%+v survivor=%+v boots=%d parks=%d", d, old, instance, boots, parks)
						}
						// The durable abort remains recoverable when capacity comes back.
						e := sched.XNewEngine(t, s, &sched.FakeVMM{}, rolloutRecoveryNotifier{}, "1.10.0")
						*e.ServiceReconcileSubmit() = func(context.Context, string) {}
						rolloutRecoveryReconcile(t, e, f.next.ID)
						completed, err := s.DeploymentByID(ctx, f.next.ID)
						if err != nil || completed.RolloutState != "aborted" {
							t.Fatalf("reverse did not resume after capacity returned: %+v %v", completed, err)
						}
						previous, err := s.DeploymentByID(ctx, f.old.ID)
						if err != nil || previous.TrafficPercent != 100 {
							t.Fatalf("repaired recipient lacks traffic: %+v %v", previous, err)
						}
						rows, err := s.ListInstancesForApp(ctx, f.app.ID)
						if err != nil {
							t.Fatal(err)
						}
						ready := 0
						for _, i := range rows {
							if i.DeploymentID == f.old.ID && i.State == "running" {
								ready++
							}
						}
						if ready == 0 {
							t.Fatal("reverse completed without usable recipient capacity")
						}
					}
				})
			}
		})
	}
}
