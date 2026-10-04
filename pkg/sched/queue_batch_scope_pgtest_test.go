// adr: 532 — environment intent and runtime ownership contracts.
package sched

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Runs the real queue poller, trigger receipt admission/claim, HTTP batch
// transport and queue acknowledgement against PostgreSQL. Guest execution is
// represented by the handler; native runtime acceptance remains separate.
func TestQueueBatchDispatchPreservesCapturedScopes(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "batch-scopes@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "batch-scopes", Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassWorker, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID,
		Name: "jobs", QueueName: "jobs", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := store.TriggerByID(ctx, consumer.Changes[0].TriggerID)
	if err != nil {
		t.Fatal(err)
	}
	if !trigger.QueueBindingID.Valid {
		t.Fatal("queue consumer lacks binding identity")
	}
	rows := map[string]state.Invocation{}
	for _, scope := range []string{"default", "staging"} {
		row, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID,
			Source: state.InvocationQueue, QueueName: "jobs", DeploymentScope: scope,
			Payload: []byte(`{"job":"run"}`), DueAt: time.Now().Add(-time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		rows[row.ID] = row
	}
	seen := map[string]string{}
	var seenMu sync.Mutex
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope triggerDispatchRequest
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Errorf("batch decode: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		results := make([]triggerDispatchResult, 0, len(envelope.Records))
		for _, record := range envelope.Records {
			want, ok := rows[record.InvocationID]
			if !ok || record.InvocationID != record.ItemIdentifier || record.InvocationAttempt != 1 {
				t.Errorf("queue identity lost across receipt claim: id=%q attempt=%d", record.InvocationID, record.InvocationAttempt)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			payload, err := base64.StdEncoding.DecodeString(record.PayloadB64)
			if err != nil {
				t.Error(err)
				return
			}
			inv, err := state.AdmitPlatformTenantInvocation(r.Context(), store, envelope.AppID,
				state.Invocation{ID: record.InvocationID, AppID: envelope.AppID, Attempts: record.InvocationAttempt,
					Source: state.InvocationSource(envelope.Source), Headers: marshalJSON(record.Headers), Payload: payload})
			if err != nil {
				t.Errorf("durable batch admission: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, version, err := state.ResolveInvocationVersion(r.Context(), store, inv)
			if err != nil || version.Scope != want.DeploymentScope || string(inv.Payload) != string(want.Payload) {
				t.Errorf("batch routing: scope=%q want=%q err=%v", version.Scope, want.DeploymentScope, err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			seenMu.Lock()
			seen[inv.ID] = version.Scope
			seenMu.Unlock()
			results = append(results, triggerDispatchResult{ItemIdentifier: record.ItemIdentifier, Status: "succeeded"})
		}
		_ = json.NewEncoder(w).Encode(triggerDispatchResponse{Results: results})
	}))
	defer gateway.Close()
	loop := makeLoopForDLQ()
	loop.pool = pool
	t.Cleanup(func() {
		for _, poller := range loop.triggerPollers {
			_ = poller.Close()
		}
	})
	loop.gatewayHTTPClient, loop.gatewayBaseURL = gateway.Client(), gateway.URL
	if err := loop.dispatchOneTrigger(ctx, trigger, store, func(string) api.Plan { return api.PlanPro }); err != nil {
		t.Fatal(err)
	}
	seenMu.Lock()
	delivered := len(seen)
	seenMu.Unlock()
	if delivered != len(rows) {
		t.Fatalf("delivered %d of %d records", delivered, len(rows))
	}
	for id, want := range rows {
		got, err := store.InvocationByID(ctx, id)
		if err != nil || got.State != state.InvocationCompleted || got.DeploymentScope != want.DeploymentScope {
			t.Fatalf("queue acknowledgement: state=%q scope=%q err=%v", got.State, got.DeploymentScope, err)
		}
	}
}
