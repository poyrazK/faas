// adr: 090
package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestManualCronFireWaitsForOwningScheduler(t *testing.T) {
	for _, command := range []bool{false, true} {
		name := "http"
		if command {
			name = "command"
		}
		t.Run(name, func(t *testing.T) {
			h := newFireNowHarness(t)
			ctx := context.Background()
			acct, err := h.store.CreateAccount(ctx, "cron-owner@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, cron := newAppAndCron(t, h.store, acct.ID, true)
			if command {
				var deployment state.Deployment
				acct, app, deployment = seedApp(t, h.store, api.PlanPro, 256, 1)
				if err := h.store.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/cron-owner", "apps/cron-owner.ext4", 4096); err != nil {
					t.Fatal(err)
				}
				cron, err = h.store.CreateCronWithOptions(ctx, app.ID, "0 0 * * *", "", true, state.CronOptions{Command: []string{"/bin/true"}})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := h.store.SetAppNodeID(ctx, app.ID, "peer-node"); err != nil {
				t.Fatal(err)
			}
			id, err := h.store.InsertFireNowRequest(ctx, cron.ID, acct.ID)
			if err != nil {
				t.Fatal(err)
			}
			h.loop.drainPendingFireNowRequests(ctx)
			req, err := h.store.GetFireNowRequest(ctx, id)
			if err != nil || req.Status != state.FireNowStatusPending || h.synth.calls.Load() != 0 {
				t.Fatalf("non-owner consumed request: %+v, %v; calls=%d", req, err, h.synth.calls.Load())
			}
			h.loop.engine.WithOwnerNodeID("peer-node")
			h.loop.drainPendingFireNowRequests(ctx)
			req, err = h.store.GetFireNowRequest(ctx, id)
			if err != nil || req.Status != state.FireNowStatusSucceeded {
				t.Fatalf("owner did not complete request: %+v, %v", req, err)
			}
			wantCalls := int64(1)
			if command {
				wantCalls = 0 // Command crons enqueue durable app tasks.
			}
			if h.synth.calls.Load() != wantCalls {
				t.Fatalf("invoke count = %d, want %d", h.synth.calls.Load(), wantCalls)
			}
		})
	}
}

func TestManualCronFireRequeuesOwnershipChangeAfterClaim(t *testing.T) {
	h := newFireNowHarness(t)
	ctx := context.Background()
	acct, err := h.store.CreateAccount(ctx, "cron-handoff@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, cron := newAppAndCron(t, h.store, acct.ID, true)
	id, err := h.store.InsertFireNowRequest(ctx, cron.ID, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	req, err := h.store.ClaimPendingFireNowRequestForNode(ctx, h.loop.engine.nodeForRoute(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetAppNodeID(ctx, app.ID, "new-owner"); err != nil {
		t.Fatal(err)
	}
	h.loop.processFireNowRequest(ctx, req)
	pending, err := h.store.GetFireNowRequest(ctx, id)
	if err != nil || pending.Status != state.FireNowStatusPending || pending.FinishedAt != nil || h.synth.calls.Load() != 0 {
		t.Fatalf("handoff lost request or dispatched on old owner: %+v, %v", pending, err)
	}
	h.loop.engine.WithOwnerNodeID("new-owner")
	h.loop.drainPendingFireNowRequests(ctx)
	final, err := h.store.GetFireNowRequest(ctx, id)
	if err != nil || final.Status != state.FireNowStatusSucceeded || h.synth.calls.Load() != 1 {
		t.Fatalf("handoff did not complete once: %+v, %v", final, err)
	}
}
