package sched_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowEventCapabilityHoldsAndResumes(t *testing.T) {
	for _, independent := range []bool{false, true} {
		for _, global := range []bool{false, true} {
			t.Run(fmt.Sprintf("independent=%v/global=%v", independent, global), func(t *testing.T) {
				ctx := t.Context()
				store := state.NewMemStore()
				account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
				if err != nil {
					t.Fatal(err)
				}
				app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "event-capability", Type: state.AppTypeApp, RAMMB: 256})
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:abc", Workflows: json.RawMessage(`[{"name":"paid","trigger":{"type":"event","source":"orders","event_type":"order.created"},"steps":[{"name":"main","path":"/paid"}]}]`)})
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err = store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "order.created", nil); err != nil {
					t.Fatal(err)
				}
				envelope, err := (events.Envelope{ID: uuid.NewString(), Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}).Normalize(account.ID, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				payload, err := json.Marshal(envelope)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
					t.Fatal(err)
				}
				workflowKey, _ := platformflags.CapabilityFlag("workflows")
				eventsKey, _ := platformflags.CapabilityFlag("events")
				deny := false
				workflowFlag := platformflags.Definition{Enabled: true, Default: true, Rules: []platformflags.Rule{{ID: "deny", Accounts: []string{account.ID}, Value: &deny}}}
				if global {
					workflowFlag = platformflags.Definition{Enabled: false}
				}
				flags := platformflags.NewSet(map[string]platformflags.Definition{workflowKey: workflowFlag, eventsKey: {Enabled: true, Default: true}})
				decider, err := capflags.Load(flags.Snapshot)
				if err != nil {
					t.Fatal(err)
				}
				loop := eventsSwitchLoop(t, store, decider, independent).WithWorkflowsDispatched(true)
				loop.XRunEventFanoutSweep(ctx)
				_, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10})
				if err != nil || total != 0 {
					t.Fatalf("disabled workflow admitted %d runs: %v", total, err)
				}
				invocations, err := store.ListInvocationsForApp(ctx, app.ID)
				if err != nil || len(invocations) != 1 {
					t.Fatalf("ordinary subscription invocations=%d: %v", len(invocations), err)
				}
				loop = eventsSwitchLoop(t, store, nil, independent).WithWorkflowsDispatched(true)
				later := time.Now().UTC().Add(2 * time.Minute)
				for i := range 3 {
					loop.XRunEventFanoutSweepAt(ctx, later.Add(time.Duration(i)*time.Minute))
				}
				_, total, err = store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10})
				if err != nil || total != 1 {
					t.Fatalf("resumed workflow admitted %d runs: %v", total, err)
				}
			})
		}
	}
}

func TestWorkflowEventPlanHoldDoesNotSpendRetryBudget(t *testing.T) {
	for _, independent := range []bool{false, true} {
		t.Run(fmt.Sprintf("independent=%v", independent), func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "plan-hold", Type: state.AppTypeApp, RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:abc", Workflows: json.RawMessage(`[{"name":"paid","trigger":{"type":"event","source":"orders","event_type":"order.created"},"steps":[{"name":"main","path":"/paid","timeout":"20m"}]}]`)})
			if err != nil {
				t.Fatal(err)
			}
			envelope, err := (events.Envelope{ID: uuid.NewString(), Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}).Normalize(account.ID, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
				t.Fatal(err)
			}
			if err = store.UpdateAccountPlan(ctx, account.ID, api.PlanHobby); err != nil {
				t.Fatal(err)
			}
			loop := eventsSwitchLoop(t, store, nil, independent).WithWorkflowsDispatched(true)
			now := time.Now().UTC()
			for i := range 8 {
				loop.XRunEventFanoutSweepAt(ctx, now.Add(time.Duration(i)*2*time.Minute))
			}
			_, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10})
			if err != nil || total != 0 {
				t.Fatalf("ineligible plan admitted %d runs: %v", total, err)
			}
			if err = store.UpdateAccountPlan(ctx, account.ID, api.PlanPro); err != nil {
				t.Fatal(err)
			}
			for i := range 3 {
				loop.XRunEventFanoutSweepAt(ctx, now.Add(time.Duration(20+i*2)*time.Minute))
			}
			_, total, err = store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10})
			if err != nil || total != 1 {
				t.Fatalf("plan restored: runs=%d err=%v", total, err)
			}
		})
	}
}
