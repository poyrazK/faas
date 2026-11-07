// adr: 208
package sched_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceRolloutAbortResumesOutstandingReplicaWithdrawals(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			f := newRolloutRecoveryFixture(t, s, 2, false)
			ctx := t.Context()
			f.running(t, f.old)
			f.running(t, f.old)
			f.running(t, f.next)
			steps := s.(state.ServiceRolloutStepStore)
			if _, err := steps.BeginServiceRolloutStep(ctx, f.next.ID, 50); err != nil {
				t.Fatal(err)
			}
			claimed, err := steps.ClaimServiceRolloutReplicaDrains(ctx, f.next.ID, 50)
			if err != nil || len(claimed.ServiceRolloutHandoff.RetiringInstanceIDs) != 1 {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			h := claimed.ServiceRolloutHandoff
			h.Action, h.Phase = state.ServiceRolloutActionAbort, state.ServiceRolloutPhasePending
			if _, err := s.UpdateServiceRolloutHandoff(ctx, f.next.ID, h); err != nil {
				t.Fatal(err)
			}
			e, err := sched.NewEngine(ctx, s, sched.NewNodeLedger(), f.vm, rolloutRecoveryNotifier{}, "1.10.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			*e.ServiceReconcileSubmit() = func(context.Context, string) {}
			if err := e.SeedLedger(ctx); err != nil {
				t.Fatal(err)
			}
			e.ReconcileServiceDeployment(ctx, f.next.ID)
			aborted, err := s.DeploymentByID(ctx, f.next.ID)
			if err != nil || aborted.RolloutState != "aborted" || len(aborted.ServiceRolloutHandoff.RetiringInstanceIDs) != 0 {
				t.Fatalf("abort left owned withdrawals: %+v %v", aborted, err)
			}
			ins, err := s.InstanceByID(ctx, h.RetiringInstanceIDs[0])
			if err != nil || ins.State != "stopped" {
				t.Fatalf("abort stranded predecessor reservation: %+v %v", ins, err)
			}
			old, err := s.DeploymentByID(ctx, f.old.ID)
			if err != nil || old.Status != state.DeployLive || old.TrafficPercent != 100 {
				t.Fatalf("abort lost recipient: %+v %v", old, err)
			}
		})
	}
}
