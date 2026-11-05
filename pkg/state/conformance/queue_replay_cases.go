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
	// A queue receipt has no unified dead-letter entry of its own
	// (ADR-20261022203653); testQueueDeadLetterSingleEntry covers its absence.
	for _, surface := range []string{"queue", "invocation_event", "receipt"} {
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
				err = fx.Store.RetryTriggerRecordByOperator(fx.Ctx, app.ID, triggerID, receiptID)
			default:
				var events []state.DeadLetterEvent
				events, err = fx.Store.ListDeadLetterEvents(fx.Ctx, app.ID, 100, "")
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, event := range events {
					if event.Source == "invocation" && event.SourceID == inv.ID {
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
			if err := fx.Store.RetryTriggerRecordByOperator(fx.Ctx, app.ID, triggerID, receiptID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("receipt revived a live invocation=%v", err)
			}
			if err := fx.Store.CompleteInvocation(fx.Ctx, inv.ID, []byte(`{}`)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The operator record verbs act only on a record of the addressed trigger
// on the addressed app; a record of another trigger, or a trigger of another
// app, is not found and stays as it was.
func testTriggerRecordOperatorScope(t *testing.T, fx *Fixture) {
	limits := api.MustLimitsFor(api.PlanPro)
	newTrigger := func(appID string) string {
		row, err := fx.Store.CreateTriggerIfUnderQuota(fx.Ctx, appID, "nats", uuid.NewString(), false, []byte(`{}`), "", 1, 1000, 3, 1024, "commit", limits)
		if err != nil {
			t.Fatal(err)
		}
		return row.ID.String()
	}
	other, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, Slug: "scope-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	own, sibling, foreign := newTrigger(fx.App.ID), newTrigger(fx.App.ID), newTrigger(other.ID)
	stateOf := func(triggerID, recordID string) string {
		rows, err := fx.Store.ListTriggerRecordsForTrigger(fx.Ctx, triggerID, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID.String() == recordID {
				return row.State
			}
		}
		return "missing"
	}
	verbs := map[string]func(appID, triggerID, recordID string) error{
		"retry": func(a, tr, r string) error { return fx.Store.RetryTriggerRecordByOperator(fx.Ctx, a, tr, r) },
		"drop":  func(a, tr, r string) error { return fx.Store.DropTriggerRecordByOperator(fx.Ctx, a, tr, r) },
	}
	type scope struct{ appID, triggerID string }
	for name, verb := range verbs {
		for _, target := range []scope{{fx.App.ID, sibling}, {other.ID, foreign}} {
			recordID, err := fx.Store.InsertTriggerRecord(fx.Ctx, target.triggerID, name+"-"+uuid.NewString(), []byte(`{}`), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := fx.Store.MarkTriggerRecordDeadLetter(fx.Ctx, recordID, "max_attempts"); err != nil {
				t.Fatal(err)
			}
			for _, wrong := range []scope{{fx.App.ID, own}, {other.ID, own}, {other.ID, sibling}, {fx.App.ID, foreign}} {
				if wrong == target {
					continue
				}
				if err := verb(wrong.appID, wrong.triggerID, recordID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("%s through app %s trigger %s = %v, want ErrNotFound", name, wrong.appID, wrong.triggerID, err)
				}
			}
			if err := verb(target.appID, target.triggerID, "not-a-uuid"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("%s malformed record = %v, want ErrNotFound", name, err)
			}
			if got := stateOf(target.triggerID, recordID); got != "dead_letter" {
				t.Fatalf("%s out of scope changed the record to %q", name, got)
			}
			if err := verb(target.appID, target.triggerID, recordID); err != nil {
				t.Fatalf("%s in scope: %v", name, err)
			}
			want := map[string]string{"retry": "pending", "drop": "missing"}[name]
			if got := stateOf(target.triggerID, recordID); got != want {
				t.Fatalf("%s in scope left %q, want %q", name, got, want)
			}
		}
	}
}
