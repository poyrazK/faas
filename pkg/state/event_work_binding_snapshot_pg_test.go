package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestPgEventWorkBindingSnapshotSurvivesConfigurationChange(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "event-work-snapshot-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "event-work-snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	policy := workpolicy.Policy{Name: "index-document", MaxRunningPerKey: 1, MaxRunningPerFairnessKey: 2}
	if _, err := store.UpsertAppWorkPolicy(ctx, account.ID, app.ID, policy); err != nil {
		t.Fatal(err)
	}
	subscription, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "documents", "edited", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetEventWorkBinding(ctx, app.ID, subscription.ID, policy.Name, "data.document_id",
		state.EventWorkBindingOptions{FairnessSelector: "data.tenant_id"}); err != nil {
		t.Fatal(err)
	}
	envelope := events.Envelope{SpecVersion: events.CloudEventsSpecVersion, ID: uuid.NewString(),
		Source: "documents", Type: "edited", Time: time.Now().UTC(),
		DataContentType: events.JSONDataContentType,
		Data:            json.RawMessage(`{"document_id":"doc-1","tenant_id":"tenant-1"}`), AccountID: account.ID}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
		t.Fatal(err)
	}
	var before []byte
	if err := pool.QueryRow(ctx, `select recipient_snapshot from event_fanout_outbox
		where account_id=$1 and source=$2 and event_id=$3`, account.ID, envelope.Source, envelope.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var recipients []state.PublishedEventRecipient
	if err := json.Unmarshal(before, &recipients); err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 1 || !recipients[0].WorkSnapshotCaptured || recipients[0].Work == nil ||
		recipients[0].Work.KeySelector != "data.document_id" ||
		recipients[0].Work.FairnessSelector != "data.tenant_id" {
		t.Fatalf("captured event work binding = %s", before)
	}
	if _, err := store.SetEventWorkBinding(ctx, app.ID, subscription.ID, policy.Name, "data.changed"); err != nil {
		t.Fatal(err)
	}
	var after []byte
	if err := pool.QueryRow(ctx, `select recipient_snapshot from event_fanout_outbox
		where account_id=$1 and source=$2 and event_id=$3`, account.ID, envelope.Source, envelope.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("recipient snapshot changed after binding update: before=%s after=%s", before, after)
	}
}
