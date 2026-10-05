package conformance

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testQueueReplayReceipt(t *testing.T, fx *Fixture) {
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, Slug: "replay-" + uuid.NewString()[:8], Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := fx.Store.(state.QueueBindingConsumerStore).CreateQueueBindingWithConsumer(fx.Ctx, state.QueueBinding{AccountID: fx.Account.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	triggerID := binding.Changes[0].TriggerID
	finisher := fx.Store.(state.TriggerClaimFinisher)
	for _, surface := range []string{"queue", "invocation_event", "receipt", "receipt_event"} {
		t.Run(surface, func(t *testing.T) {
			inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AppID: app.ID, AccountID: fx.Account.ID,
				Source: state.InvocationQueue, QueueName: "jobs", DeploymentScope: "staging", ReplayGeneration: 99,
				Payload: []byte(`{"job":"retained"}`), DueAt: time.Now().Add(-time.Second)})
			if err != nil || inv.ReplayGeneration != 0 {
				t.Fatalf("admission generation=%d %v", inv.ReplayGeneration, err)
			}
			receiptID, err := fx.Store.InsertTriggerRecord(fx.Ctx, triggerID, inv.ID, inv.Payload, []byte(`{}`), []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			oldRecords, err := fx.Store.(state.TriggerBatchClaimer).ClaimTriggerRecordsByItems(fx.Ctx, triggerID, []string{inv.ID})
			if err != nil || len(oldRecords) != 1 {
				t.Fatalf("claim receipt=%+v %v", oldRecords, err)
			}
			old := oldRecords[0]
			if err := finisher.RouteClaimedTriggerDeadLetter(fx.Ctx, receiptID, old.ClaimGeneration, triggerID, "poison_record", []byte(`{"cause":"original"}`)); err != nil {
				t.Fatal(err)
			}
			if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, inv.ID, "", 60, 10); err != nil {
				t.Fatal(err)
			}
			if err := fx.Store.FailInvocation(fx.Ctx, inv.ID, "exhausted", time.Nanosecond, 1); err != nil {
				t.Fatal(err)
			}
			switch surface {
			case "queue":
				_, err = fx.Store.RetryQueueDeadLetter(fx.Ctx, fx.Account.ID, inv.ID)
			case "receipt":
				err = fx.Store.RetryTriggerRecordByOperator(fx.Ctx, receiptID)
			default:
				var events []state.DeadLetterEvent
				events, err = fx.Store.ListDeadLetterEvents(fx.Ctx, app.ID, 100, "")
				if err != nil {
					t.Fatal(err)
				}
				wantedSource, wantedID := "invocation", inv.ID
				if surface == "receipt_event" {
					wantedSource, wantedID = "trigger_record", receiptID
				}
				found := false
				for _, event := range events {
					if event.Source == wantedSource && event.SourceID == wantedID {
						_, err = fx.Store.ReplayDeadLetterEvent(fx.Ctx, fx.Account.ID, app.ID, event.ID)
						found = true
						break
					}
				}
				if !found {
					t.Fatal("missing original failure event")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			row, err := fx.Store.InvocationByID(fx.Ctx, inv.ID)
			if err != nil || row.State != state.InvocationPending || row.Attempts != 0 || row.ReplayGeneration != 1 ||
				row.QueueBindingID != binding.Binding.ID || row.DeploymentScope != "staging" || string(row.Payload) != string(inv.Payload) {
				t.Fatalf("replay changed admission=%+v %v", row, err)
			}
			fresh, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, inv.ID, "", 60, 10)
			if err != nil || fresh.Attempts != 1 {
				t.Fatalf("claim replay=%+v %v", fresh, err)
			}
			carrier := state.Invocation{ID: inv.ID, AppID: app.ID, Source: "esm", Attempts: 1}
			if _, err := state.AdmitPlatformTenantInvocation(fx.Ctx, fx.Store, app.ID, carrier); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("old generation admitted=%v", err)
			}
			carrier.ReplayGeneration = 1
			if _, err := state.AdmitPlatformTenantInvocation(fx.Ctx, fx.Store, app.ID, carrier); err != nil {
				t.Fatal(err)
			}
			claims, err := fx.Store.(state.TriggerBatchClaimer).ClaimTriggerRecordsByItems(fx.Ctx, triggerID, []string{inv.ID})
			if err != nil || len(claims) != 1 || claims[0].ID.String() != receiptID || claims[0].ClaimGeneration <= old.ClaimGeneration || claims[0].Attempts != 0 {
				t.Fatalf("receipt was not rearmed=%+v %v", claims, err)
			}
			if err := finisher.CompleteClaimedTriggerRecord(fx.Ctx, receiptID, old.ClaimGeneration); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("stale receipt completion=%v", err)
			}
			if err := finisher.RouteClaimedTriggerDeadLetter(fx.Ctx, receiptID, claims[0].ClaimGeneration, triggerID, "max_attempts", []byte(`{"cause":"second"}`)); err != nil {
				t.Fatal(err)
			}
			if err := fx.Store.FailInvocation(fx.Ctx, inv.ID, "again", time.Nanosecond, 1); err != nil {
				t.Fatal(err)
			}
			audit, err := fx.Store.ListTriggerDeadLetter(fx.Ctx, triggerID, 100)
			if err != nil {
				t.Fatal(err)
			}
			foundHistory := false
			for _, failure := range audit {
				if failure.RecordID.String() == receiptID {
					foundHistory = failure.Reason == "max_attempts" && json.Valid(failure.FailureHistory) && bytes.Contains(failure.FailureHistory, []byte("original"))
				}
			}
			if !foundHistory {
				t.Fatal("repeat failure erased original audit")
			}
			if replayed, err := fx.Store.RetryQueueDeadLetter(fx.Ctx, fx.Account.ID, inv.ID); err != nil || replayed.ReplayGeneration != 2 {
				t.Fatalf("second replay=%+v %v", replayed, err)
			}
			if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, inv.ID, "", 60, 10); err != nil {
				t.Fatal(err)
			}
			claims, err = fx.Store.(state.TriggerBatchClaimer).ClaimTriggerRecordsByItems(fx.Ctx, triggerID, []string{inv.ID})
			if err != nil || len(claims) != 1 {
				t.Fatalf("second receipt replay=%+v %v", claims, err)
			}
			if err := finisher.CompleteClaimedTriggerRecord(fx.Ctx, receiptID, claims[0].ClaimGeneration); err != nil {
				t.Fatal(err)
			}
			if audit, err := fx.Store.ListTriggerDeadLetter(fx.Ctx, triggerID, 100); err != nil || len(audit) == 0 {
				t.Fatalf("replay lost failure audit=%+v %v", audit, err)
			}
			if err := fx.Store.RetryTriggerRecordByOperator(fx.Ctx, receiptID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("receipt revived a live invocation=%v", err)
			}
			if err := fx.Store.CompleteInvocation(fx.Ctx, inv.ID, []byte(`{}`)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
