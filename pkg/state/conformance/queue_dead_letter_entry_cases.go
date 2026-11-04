package conformance

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// queueDeadLetterMessage is one push-delivered queue message whose invocation
// and push receipt have both dead-lettered, as a handler failure leaves them.
type queueDeadLetterMessage struct {
	app       state.App
	triggerID string
	inv       state.Invocation
	receiptID string
}

func newQueueDeadLetterMessage(t *testing.T, fx *Fixture) queueDeadLetterMessage {
	t.Helper()
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, Slug: "dlq-" + uuid.NewString()[:8], Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := fx.Store.(state.QueueBindingConsumerStore).CreateQueueBindingWithConsumer(fx.Ctx, state.QueueBinding{AccountID: fx.Account.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	msg := queueDeadLetterMessage{app: app, triggerID: binding.Changes[0].TriggerID}
	msg.inv, err = fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AppID: app.ID, AccountID: fx.Account.ID,
		Source: state.InvocationQueue, QueueName: "jobs", Payload: []byte(`{"job":"poison"}`), DueAt: time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	msg.receiptID, err = fx.Store.InsertTriggerRecord(fx.Ctx, msg.triggerID, msg.inv.ID, msg.inv.Payload, []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	msg.fail(t, fx)
	return msg
}

// fail dead-letters the receipt and then its invocation.
func (msg queueDeadLetterMessage) fail(t *testing.T, fx *Fixture) {
	t.Helper()
	claims, err := fx.Store.(state.TriggerBatchClaimer).ClaimTriggerRecordsByItems(fx.Ctx, msg.triggerID, []string{msg.inv.ID})
	if err != nil || len(claims) != 1 || claims[0].ID.String() != msg.receiptID {
		t.Fatalf("claim receipt=%+v %v", claims, err)
	}
	if err := fx.Store.(state.TriggerClaimFinisher).RouteClaimedTriggerDeadLetter(fx.Ctx, msg.receiptID, claims[0].ClaimGeneration, msg.triggerID, "max_attempts", []byte(`{"cause":"handler"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, msg.inv.ID, "", 60, 10); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.FailInvocation(fx.Ctx, msg.inv.ID, "exhausted", time.Nanosecond, 1); err != nil {
		t.Fatal(err)
	}
}

func (msg queueDeadLetterMessage) owns(ev state.DeadLetterEvent) bool {
	return ev.Source == "invocation" && ev.SourceID == msg.inv.ID || ev.Source == "trigger_record" && ev.SourceID == msg.receiptID
}

// entries returns the message's unified entries; app and account lists agree.
func (msg queueDeadLetterMessage) entries(t *testing.T, fx *Fixture) []state.DeadLetterEvent {
	t.Helper()
	appEvents, err := fx.Store.ListDeadLetterEvents(fx.Ctx, msg.app.ID, 200, "")
	if err != nil {
		t.Fatal(err)
	}
	accountEvents, err := fx.Store.ListDeadLetterEventsForAccount(fx.Ctx, fx.Account.ID, 200, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, account []state.DeadLetterEvent
	for _, ev := range appEvents {
		if msg.owns(ev) {
			out = append(out, ev)
		}
	}
	for _, ev := range accountEvents {
		if msg.owns(ev) {
			account = append(account, ev)
		}
	}
	if len(account) != len(out) {
		t.Fatalf("account list has %d entries for the message, app list %d", len(account), len(out))
	}
	return out
}

// requireReceiptAudit asserts the receipt's failure stays in its trigger's DLQ.
func (msg queueDeadLetterMessage) requireReceiptAudit(t *testing.T, fx *Fixture) {
	t.Helper()
	audit, err := fx.Store.ListTriggerDeadLetter(fx.Ctx, msg.triggerID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range audit {
		if failure.RecordID.String() == msg.receiptID {
			return
		}
	}
	t.Fatalf("receipt %s left the trigger dead-letter audit: %+v", msg.receiptID, audit)
}

// A failed queue message is one unified dead-letter entry: its invocation.
// The push receipt that failed beside it stays in the trigger's audit and is
// recovered through the invocation (ADR-20261022203653).
func testQueueDeadLetterSingleEntry(t *testing.T, fx *Fixture) {
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	accountID := fx.Account.ID
	cases := []struct {
		name        string
		act         func(*testing.T, queueDeadLetterMessage, state.DeadLetterEvent)
		wantEntries int
		wantOpen    int
	}{
		{"listed_once", func(t *testing.T, msg queueDeadLetterMessage, entry state.DeadLetterEvent) {
			if got, err := fx.Store.DeadLetterEventByID(fx.Ctx, msg.app.ID, entry.ID); err != nil || got.SourceID != msg.inv.ID {
				t.Fatalf("inspect entry=%+v %v", got, err)
			}
		}, 1, 1},
		{"replay_one_rearms_receipt", func(t *testing.T, msg queueDeadLetterMessage, entry state.DeadLetterEvent) {
			if replayed, err := fx.Store.ReplayDeadLetterEvent(fx.Ctx, accountID, msg.app.ID, entry.ID); err != nil || replayed.ReplayedAt == nil {
				t.Fatalf("replay=%+v %v", replayed, err)
			}
			if row, err := fx.Store.InvocationByID(fx.Ctx, msg.inv.ID); err != nil || row.State != state.InvocationPending {
				t.Fatalf("replayed invocation=%+v %v", row, err)
			}
			claims, err := fx.Store.(state.TriggerBatchClaimer).ClaimTriggerRecordsByItems(fx.Ctx, msg.triggerID, []string{msg.inv.ID})
			if err != nil || len(claims) != 1 || claims[0].ID.String() != msg.receiptID || claims[0].Attempts != 0 {
				t.Fatalf("receipt was not rearmed=%+v %v", claims, err)
			}
		}, 1, 0},
		{"replay_all_counts_the_message_once", func(t *testing.T, msg queueDeadLetterMessage, _ state.DeadLetterEvent) {
			if n, err := fx.Store.ReplayDeadLetterEvents(fx.Ctx, accountID, msg.app.ID, 200); err != nil || n != 1 {
				t.Fatalf("replay all=%d %v, want 1", n, err)
			}
			if n, err := fx.Store.ReplayDeadLetterEvents(fx.Ctx, accountID, msg.app.ID, 200); err != nil || n != 0 {
				t.Fatalf("second replay all=%d %v, want 0", n, err)
			}
		}, 1, 0},
		{"refailure_stays_one_entry", func(t *testing.T, msg queueDeadLetterMessage, entry state.DeadLetterEvent) {
			if _, err := fx.Store.ReplayDeadLetterEvent(fx.Ctx, accountID, msg.app.ID, entry.ID); err != nil {
				t.Fatal(err)
			}
			msg.fail(t, fx)
		}, 1, 1},
		{"purge_one_hides_receipt", func(t *testing.T, msg queueDeadLetterMessage, entry state.DeadLetterEvent) {
			if err := fx.Store.DeleteDeadLetterEvent(fx.Ctx, accountID, msg.app.ID, entry.ID); err != nil {
				t.Fatal(err)
			}
		}, 0, 0},
		{"purge_all_counts_the_message_once", func(t *testing.T, msg queueDeadLetterMessage, _ state.DeadLetterEvent) {
			if n, err := fx.Store.DeleteDeadLetterEvents(fx.Ctx, accountID, msg.app.ID, 200); err != nil || n != 1 {
				t.Fatalf("purge all=%d %v, want 1", n, err)
			}
		}, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := newQueueDeadLetterMessage(t, fx)
			before := msg.entries(t, fx)
			if len(before) != 1 || before[0].Source != "invocation" || before[0].ReplayedAt != nil {
				t.Fatalf("failed message listed as %+v, want one open invocation entry", before)
			}
			tc.act(t, msg, before[0])
			after := msg.entries(t, fx)
			open := 0
			for _, ev := range after {
				if ev.Source != "invocation" {
					t.Fatalf("receipt listed as its own entry: %+v", ev)
				}
				if ev.ReplayedAt == nil {
					open++
				}
			}
			if len(after) != tc.wantEntries || open != tc.wantOpen {
				t.Fatalf("entries=%d open=%d, want %d/%d: %+v", len(after), open, tc.wantEntries, tc.wantOpen, after)
			}
			msg.requireReceiptAudit(t, fx)
		})
	}
}
