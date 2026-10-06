// adr: 374
package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type workIdentityTestPoller struct {
	deliveries []SourceRecord
	acks       []string
	nacks      []string
}

func (p *workIdentityTestPoller) Kind() string { return "kafka" }
func (p *workIdentityTestPoller) Poll(context.Context, sqlc.Trigger) PollResult {
	if len(p.deliveries) == 0 {
		return PollResult{Records: []SourceRecord{}}
	}
	record := p.deliveries[0]
	p.deliveries = p.deliveries[1:]
	return PollResult{Records: []SourceRecord{record}}
}
func (p *workIdentityTestPoller) Ack(_ context.Context, _ sqlc.Trigger, ids []string) error {
	p.acks = append(p.acks, ids...)
	return nil
}
func (p *workIdentityTestPoller) Nack(_ context.Context, _ sqlc.Trigger, ids []string, _ string) error {
	p.nacks = append(p.nacks, ids...)
	return nil
}
func (p *workIdentityTestPoller) Close() error { return nil }

func TestBrokerWorkBindingDispatchAndRedelivery(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "broker-work@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "broker-work", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAppWorkPolicy(ctx, account.ID, app.ID,
		workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1}); err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "kafka", "orders", false,
		[]byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetTriggerWorkBinding(ctx, app.ID, trigger.ID.String(), "orders", "order_id", ""); err != nil {
		t.Fatal(err)
	}
	enabled := true
	trigger, err = store.UpdateTrigger(ctx, trigger.ID.String(), &enabled, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	poller := &workIdentityTestPoller{deliveries: []SourceRecord{
		{ItemIdentifier: "0-8-10", StableIdentifier: "orders/0/8", Payload: []byte(`{"order_id":"one"}`), ReceivedAt: time.Now()},
		{ItemIdentifier: "0-8-15", StableIdentifier: "orders/0/8", Payload: []byte(`{"order_id":"one"}`), ReceivedAt: time.Now()},
	}}
	installPollerFactory(t, "kafka", func(sqlc.Trigger) (triggerSource, error) { return poller, nil })
	posted := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posted++
		var request triggerDispatchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("gateway decode: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(request.Records) != 1 || request.Records[0].ItemIdentifier != "orders/0/8" {
			t.Errorf("gateway records = %+v", request.Records)
		}
		_, _ = fmt.Fprint(w, `{"results":[{"item_identifier":"orders/0/8","status":"succeeded"}]}`)
	}))
	defer gateway.Close()
	loop := makeLoopForDLQ()
	loop.gatewayHTTPClient = gateway.Client()
	loop.gatewayBaseURL = gateway.URL
	for i := 0; i < 2; i++ {
		if err := loop.dispatchOneTrigger(ctx, trigger, store, nil); err != nil {
			t.Fatal(err)
		}
	}
	if posted != 1 || len(poller.acks) != 2 || poller.acks[0] != "0-8-10" || poller.acks[1] != "0-8-15" || len(poller.nacks) != 0 {
		t.Fatalf("posted=%d acks=%v nacks=%v", posted, poller.acks, poller.nacks)
	}
	var count int
	var recordState string
	if err := pool.QueryRow(ctx, `select count(*), max(state) from trigger_records
		where trigger_id=$1 and item_identifier='orders/0/8'`, trigger.ID).Scan(&count, &recordState); err != nil {
		t.Fatal(err)
	}
	if count != 1 || recordState != "succeeded" {
		t.Fatalf("receipt count=%d state=%s", count, recordState)
	}
}

func TestExclusiveBrokerTriggerAdmissionAndReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "exclusive-broker@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "exclusive-broker", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	owners := state.ExclusiveWorkStore(store)
	if _, err := owners.UpsertExclusiveWorkPolicy(ctx, account.ID, exclusivework.Policy{
		Name: "crm-sync", Scope: "account", Contention: "queue", MemberAppIDs: []string{app.ID},
		LeaseSeconds: 5, MaxAttemptSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "kafka", "crm-sync", true,
		[]byte(`{"topic":"crm"}`), "", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	bindings := state.ExclusiveTriggerBindingStore(store)
	if _, err := bindings.UpsertExclusiveTriggerBinding(ctx, state.ExclusiveTriggerBinding{
		Source: "broker", TriggerID: trigger.ID.String(), AccountID: account.ID,
		PolicyName: "crm-sync", Key: json.RawMessage(`"customer:acme:crm-sync"`), EquivalenceKey: "scheduled-sync",
	}); err != nil {
		t.Fatal(err)
	}
	poller := &workIdentityTestPoller{deliveries: []SourceRecord{
		{ItemIdentifier: "orders/0/101", StableIdentifier: "orders/0/42", Payload: []byte(`{"customer":"acme"}`)},
		{ItemIdentifier: "orders/0/102", StableIdentifier: "orders/0/42", Payload: []byte(`{"customer":"acme"}`)},
	}}
	installPollerFactory(t, "kafka", func(sqlc.Trigger) (triggerSource, error) { return poller, nil })
	loop := makeLoopForDLQ()
	for range 2 {
		if err := loop.dispatchOneTrigger(ctx, trigger, store, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(poller.acks) != 2 || poller.acks[0] != "orders/0/101" || poller.acks[1] != "orders/0/102" || len(poller.nacks) != 0 {
		t.Fatalf("acks=%v nacks=%v", poller.acks, poller.nacks)
	}
	operations, err := owners.ListDueExclusiveOperations(ctx, 10)
	if err != nil || len(operations) != 1 {
		t.Fatalf("due operations=%+v err=%v, want one idempotent operation", operations, err)
	}
	var linked int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM trigger_records
		WHERE trigger_id=$1 AND state='succeeded'
		  AND metadata->'_gregale'->>'exclusive_operation_id'=$2`, trigger.ID, operations[0].ID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != 2 {
		t.Fatalf("linked trigger receipts=%d, want both deliveries linked to operation %s", linked, operations[0].ID)
	}
}
