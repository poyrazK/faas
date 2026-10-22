package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowRecoveryJobs(t *testing.T) {
	for _, modern := range []bool{false, true} {
		name := "legacy"
		if modern {
			name = "recipient"
		}
		t.Run(name, func(t *testing.T) {
			for _, scenario := range []string{"queue", "revision", "count", "generation", "cancel"} {
				t.Run(scenario, func(t *testing.T) {
					state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
						ctx := context.Background()
						app, _ := state.SeedEventWorkflow(t, store)
						author := store.(state.AutomationStore)
						draft := state.MutateForTest(t, author, app.ID, "paid", "save", 0, state.AutomationDraft("paid", "/paid", `{"type":"event","source":"billing.*","event_type":"invoice.paid"}`), false)
						published := state.MutateForTest(t, author, app.ID, "paid", "publish", draft.Version, nil, true)
						id := uuid.NewString()
						work := state.PublishWorkflowEvent(t, store, app, id)
						recipient := work.RecipientSnapshot[0]
						failure := state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: 1, UpdatedAt: time.Now().UTC()}
						target := api.EventWorkflowRecoveryTarget{EventID: id, SubscriptionID: recipient.ID}
						if modern {
							routing := store.(state.PublishedEventRecipientWorkStore)
							if err := routing.InitializePublishedEventRecipients(ctx, work, time.Now().UTC()); err != nil {
								t.Fatal(err)
							}
							claimed, err := routing.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
							if err != nil {
								t.Fatal(err)
							}
							generation := claimed.Generation
							target.Generation = &generation
							if err := routing.FinishPublishedEventRecipient(ctx, claimed, failure, time.Now().UTC()); err != nil {
								t.Fatal(err)
							}
						} else {
							if err := store.(state.PublishedEventRecipientProgressStore).RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, recipient.ID, failure); err != nil {
								t.Fatal(err)
							}
							if err := store.(state.PublishedEventWorkStore).FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
								t.Fatal(err)
							}
						}
						req := api.EventRecoveryRequest{Mode: "routing", RequestID: uuid.NewString(), RatePerSecond: 1, ProtectReceipts: true, WorkflowRouting: &api.EventWorkflowRecoveryPlan{Name: "paid", PublishedVersion: published.PublishedVersion, Source: "billing.stripe", Events: []api.EventWorkflowRecoveryTarget{target}}}
						if scenario == "count" {
							req.WorkflowRouting.Events[0].ReplayCount++
						}
						if scenario == "generation" {
							wrong := int64(999)
							req.WorkflowRouting.Events[0].Generation = &wrong
						}
						recovery := store.(state.EventRecoveryStore)
						preview, err := recovery.PreviewEventRecovery(ctx, app.AccountID, app.ID, req)
						if err != nil || preview.MatchedCount != 1 {
							t.Fatalf("preview=%+v err=%v", preview, err)
						}
						job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, req)
						if err != nil {
							t.Fatal(err)
						}
						retry, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, req)
						if err != nil || retry.ID != job.ID {
							t.Fatalf("retry=%+v err=%v", retry, err)
						}
						changed := req
						changed.WorkflowRouting = &api.EventWorkflowRecoveryPlan{Name: "paid", PublishedVersion: published.PublishedVersion + 1, Source: "billing.stripe", Events: []api.EventWorkflowRecoveryTarget{target}}
						if _, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, changed); !errors.Is(err, state.ErrEventRecoveryRequestConflict) {
							t.Fatalf("request conflict=%v", err)
						}
						if pg, ok := store.(*state.PgStore); ok {
							recovery = state.NewPgStore((*pg.Pool()).Pool)
						}
						if _, err := recovery.GetEventRecovery(ctx, uuid.NewString(), job.ID); !errors.Is(err, state.ErrNotFound) {
							t.Fatalf("tenant lookup=%v", err)
						}
						if _, err := recovery.PauseEventRecovery(ctx, app.AccountID, job.ID); err != nil {
							t.Fatal(err)
						}
						if did, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil || did {
							t.Fatalf("paused worker=%v %v", did, err)
						}
						if _, err := recovery.ResumeEventRecovery(ctx, app.AccountID, job.ID); err != nil {
							t.Fatal(err)
						}
						if scenario == "revision" {
							state.MutateForTest(t, author, app.ID, "paid", "publish", published.Version, nil, false)
						}
						if scenario == "cancel" {
							if _, err := recovery.CancelEventRecovery(ctx, app.AccountID, job.ID); err != nil {
								t.Fatal(err)
							}
						} else if did, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil || !did {
							t.Fatalf("worker=%v %v", did, err)
						}
						saved, err := recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
						if err != nil {
							t.Fatal(err)
						}
						items, err := recovery.ListEventRecoveryItems(ctx, app.AccountID, job.ID, 0, 10)
						if err != nil || len(items.Items) != 1 {
							t.Fatalf("items=%+v err=%v", items, err)
						}
						expected := "skipped"
						if scenario == "queue" {
							expected = "queued"
						}
						if scenario == "cancel" {
							expected = "cancelled"
						}
						if items.Items[0].State != expected {
							t.Fatalf("outcome=%+v saved=%+v", items.Items[0], saved)
						}
						if expected == "skipped" && items.Items[0].Reason != "changed" {
							t.Fatalf("reason=%+v", items.Items[0])
						}
						retry, err = recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, req)
						if err != nil || retry.ID != job.ID {
							t.Fatalf("terminal retry=%+v err=%v", retry, err)
						}
					})
				})
			}
		})
	}
}

func TestWorkflowRecoveryJobRateAndPartialProgress(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		app, _ := state.SeedEventWorkflow(t, store)
		author := store.(state.AutomationStore)
		draft := state.MutateForTest(t, author, app.ID, "paid", "save", 0, state.AutomationDraft("paid", "/paid", `{"type":"event","source":"billing.*","event_type":"invoice.paid"}`), false)
		published := state.MutateForTest(t, author, app.ID, "paid", "publish", draft.Version, nil, true)
		targets := []api.EventWorkflowRecoveryTarget{}
		for range 2 {
			id := uuid.NewString()
			work := state.PublishWorkflowEvent(t, store, app, id)
			recipient := work.RecipientSnapshot[0]
			failure := state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: 1, UpdatedAt: time.Now().UTC()}
			if err := store.(state.PublishedEventRecipientProgressStore).RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, recipient.ID, failure); err != nil {
				t.Fatal(err)
			}
			if err := store.(state.PublishedEventWorkStore).FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
				t.Fatal(err)
			}
			targets = append(targets, api.EventWorkflowRecoveryTarget{EventID: id, SubscriptionID: recipient.ID})
		}
		recovery := store.(state.EventRecoveryStore)
		ordinary, err := recovery.PreviewEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{IncludeNonRetryable: true})
		if err != nil || ordinary.MatchedCount != 0 {
			t.Fatalf("ordinary selection=%+v err=%v", ordinary, err)
		}
		req := api.EventRecoveryRequest{RequestID: uuid.NewString(), RatePerSecond: 1, ProtectReceipts: true, WorkflowRouting: &api.EventWorkflowRecoveryPlan{Name: "paid", PublishedVersion: published.PublishedVersion, Source: "billing.stripe", Events: targets}}
		job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		job.Selection.WorkflowRouting.PublishedVersion++
		saved, err := recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
		if err != nil || saved.Selection.WorkflowRouting.PublishedVersion != published.PublishedVersion {
			t.Fatalf("returned selection mutated persisted plan: %+v %v", saved, err)
		}
		now := time.Now().UTC()
		if did, err := recovery.ProcessNextEventRecovery(ctx, now); err != nil || !did {
			t.Fatalf("first=%v %v", did, err)
		}
		if did, err := recovery.ProcessNextEventRecovery(ctx, now); err != nil || did {
			t.Fatalf("spent rate permit=%v %v", did, err)
		}
		saved, err = recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
		if err != nil || saved.PendingCount != 1 || saved.QueuedCount != 1 {
			t.Fatalf("partial=%+v %v", saved, err)
		}
		preflight, err := store.(state.EventRecoveryPreflightStore).GetEventRecoveryPreflight(ctx, app.AccountID, job.ID, now)
		if err != nil || preflight.UnknownCount != 1 || preflight.ReceiptProtectionUntil == nil {
			t.Fatalf("preflight=%+v %v", preflight, err)
		}
		if _, err := recovery.SetEventRecoveryRate(ctx, app.AccountID, job.ID, api.EventRecoveryRateRequest{RatePerSecond: 2}); err != nil {
			t.Fatal(err)
		}
		if _, err := recovery.CancelEventRecovery(ctx, app.AccountID, job.ID); err != nil {
			t.Fatal(err)
		}
		saved, err = recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
		if err != nil || saved.CancelledCount != 1 || saved.QueuedCount != 1 {
			t.Fatalf("cancel=%+v %v", saved, err)
		}
	})
}
