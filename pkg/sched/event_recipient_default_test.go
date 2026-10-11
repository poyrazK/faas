// adr: 647
package sched

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEventRecipientRoutingDefaultOwnership(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store recipientRoutingTestStore = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			for _, tc := range []struct {
				name         string
				subscribed   bool
				filter       json.RawMessage
				disabled     bool
				wantEnqueued int
				wantFiltered int
			}{
				{name: "empty"},
				{name: "application", subscribed: true, wantEnqueued: 1},
				{name: "filtered", subscribed: true, filter: json.RawMessage(`{"data":{"amount":{"$gt":100}}}`), wantFiltered: 1},
				{name: "disabled", subscribed: true, disabled: true, wantEnqueued: 1},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx := t.Context()
					account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
					if err != nil {
						t.Fatal(err)
					}
					accountID := mustCanonicalEventAccountID(t, account.ID)
					app, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: tc.name, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
					if err != nil {
						t.Fatal(err)
					}
					if tc.subscribed {
						if _, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.created", tc.filter); err != nil {
							t.Fatal(err)
						}
					}
					envelope, err := (events.Envelope{ID: uuid.NewString(), Source: "orders", Type: "order.created", Data: json.RawMessage(`{"amount":1}`)}).Normalize(accountID, time.Now().UTC())
					if err != nil {
						t.Fatal(err)
					}
					payload, err := json.Marshal(envelope)
					if err != nil {
						t.Fatal(err)
					}
					if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
						t.Fatal(err)
					}
					loop := NewLoop(nil, &Engine{store: store}, nil)
					if tc.disabled {
						loop.WithEventRecipientClaims(false)
					}
					loop.runEventFanoutSweep(ctx)
					receipt, err := store.EventReceipt(ctx, accountID, "orders", envelope.ID, state.EventReceiptCursor{}, 100)
					if err != nil || receipt.RecipientClaims == tc.disabled || receipt.RoutingSettledAt == nil || receipt.RoutingSummary["enqueued"] != tc.wantEnqueued || receipt.RoutingSummary["filtered"] != tc.wantFiltered {
						t.Fatalf("default ownership = %+v, %v", receipt, err)
					}
					invocations, err := store.ListInvocationsForApp(ctx, app.ID)
					if err != nil || len(invocations) != tc.wantEnqueued {
						t.Fatalf("invocations = %d, %v", len(invocations), err)
					}
				})
			}
		})
	}
}
