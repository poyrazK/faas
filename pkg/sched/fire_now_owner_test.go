// adr: 090
package sched_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestManualCronFireWaitsForOwningScheduler(t *testing.T) {
	for _, command := range []bool{false, true} {
		name := "http"
		if command {
			name = "command"
		}
		t.Run(name, func(t *testing.T) {
			h := sched.NewFireNowHarness(t)
			ctx := context.Background()
			acct, err := (*h.Store()).CreateAccount(ctx, "cron-owner@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, cron := sched.NewAppAndCron(t, (*h.Store()), acct.ID, true)
			if command {
				var deployment state.Deployment
				acct, app, deployment = sched.SeedApp(t, (*h.Store()), api.PlanPro, 256, 1)
				if err := (*h.Store()).SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/cron-owner", "apps/cron-owner.ext4", 4096); err != nil {
					t.Fatal(err)
				}
				cron, err = (*h.Store()).CreateCronWithOptions(ctx, app.ID, "0 0 * * *", "", true, state.CronOptions{Command: []string{"/bin/true"}})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := (*h.Store()).SetAppNodeID(ctx, app.ID, "peer-node"); err != nil {
				t.Fatal(err)
			}
			id, err := (*h.Store()).InsertFireNowRequest(ctx, cron.ID, acct.ID)
			if err != nil {
				t.Fatal(err)
			}
			(*h.Loop()).DrainPendingFireNowRequests(ctx)
			req, err := (*h.Store()).GetFireNowRequest(ctx, id)
			if err != nil || req.Status != state.FireNowStatusPending || (*(*h.Synth()).Calls()).Load() != 0 {
				t.Fatalf("non-owner consumed request: %+v, %v; calls=%d", req, err, (*(*h.Synth()).Calls()).Load())
			}
			(*(*h.Loop()).Engine()).WithOwnerNodeID("peer-node")
			(*h.Loop()).DrainPendingFireNowRequests(ctx)
			req, err = (*h.Store()).GetFireNowRequest(ctx, id)
			if err != nil || req.Status != state.FireNowStatusSucceeded {
				t.Fatalf("owner did not complete request: %+v, %v", req, err)
			}
			wantCalls := int64(1)
			if command {
				wantCalls = 0 // Command crons enqueue durable app tasks.
			}
			if (*(*h.Synth()).Calls()).Load() != wantCalls {
				t.Fatalf("invoke count = %d, want %d", (*(*h.Synth()).Calls()).Load(), wantCalls)
			}
		})
	}
}

func TestManualCronFireRequeuesOwnershipChangeAfterClaim(t *testing.T) {
	h := sched.NewFireNowHarness(t)
	ctx := context.Background()
	acct, err := (*h.Store()).CreateAccount(ctx, "cron-handoff@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, cron := sched.NewAppAndCron(t, (*h.Store()), acct.ID, true)
	id, err := (*h.Store()).InsertFireNowRequest(ctx, cron.ID, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	req, err := (*h.Store()).ClaimPendingFireNowRequestForNode(ctx, (*(*h.Loop()).Engine()).NodeForRoute(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := (*h.Store()).SetAppNodeID(ctx, app.ID, "new-owner"); err != nil {
		t.Fatal(err)
	}
	(*h.Loop()).ProcessFireNowRequest(ctx, req)
	pending, err := (*h.Store()).GetFireNowRequest(ctx, id)
	if err != nil || pending.Status != state.FireNowStatusPending || pending.FinishedAt != nil || (*(*h.Synth()).Calls()).Load() != 0 {
		t.Fatalf("handoff lost request or dispatched on old owner: %+v, %v", pending, err)
	}
	(*(*h.Loop()).Engine()).WithOwnerNodeID("new-owner")
	(*h.Loop()).DrainPendingFireNowRequests(ctx)
	final, err := (*h.Store()).GetFireNowRequest(ctx, id)
	if err != nil || final.Status != state.FireNowStatusSucceeded || (*(*h.Synth()).Calls()).Load() != 1 {
		t.Fatalf("handoff did not complete once: %+v, %v", final, err)
	}
}
