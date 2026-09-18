// events_fanout_e2e_test.go — Workstream B acceptance coverage for the
// publish → schedd matcher → async invocation path (EPIC #1278).
//
// The test uses real PostgreSQL, apid, and schedd processes. It does not need
// KVM: the assertion stops at the durable invocation ledger, before a guest
// wake is required.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestE2E_EventFanout_MatchesFiltersAndIsolatesAccounts(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Skip("pgtest.Open returned nil")
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("dbMigrateUp: %v", err)
	}

	ctx := context.Background()
	h := e2etest.Start(t, pool, e2etest.APID|e2etest.Schedd|e2etest.GatewaySynthStub)
	store := state.NewPgStore(h.Pool)

	keyA := h.SeedAccount(ctx, api.PlanPro, "event-fanout-a")
	keyB := h.SeedAccount(ctx, api.PlanPro, "event-fanout-b")
	appA := createEventFanoutApp(t, h, keyA, "event-fanout-match")
	filteredApp := createEventFanoutApp(t, h, keyA, "event-fanout-filtered")
	otherAccountApp := createEventFanoutApp(t, h, keyB, "event-fanout-other")

	accountA, err := store.AccountByEmail(ctx, "e2e+pro+event-fanout-a@test.example")
	if err != nil {
		t.Fatalf("AccountByEmail A: %v", err)
	}
	accountB, err := store.AccountByEmail(ctx, "e2e+pro+event-fanout-b@test.example")
	if err != nil {
		t.Fatalf("AccountByEmail B: %v", err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, accountA.ID, appA.ID,
		"billing.*", "invoice.paid", json.RawMessage(`{"data":{"amount":{"$gt":100}}}`)); err != nil {
		t.Fatalf("UpsertEventSubscription matching: %v", err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, accountA.ID, filteredApp.ID,
		"billing.*", "invoice.paid", json.RawMessage(`{"data":{"amount":{"$gt":200}}}`)); err != nil {
		t.Fatalf("UpsertEventSubscription filtered: %v", err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, accountB.ID, otherAccountApp.ID,
		"billing.*", "invoice.paid", json.RawMessage(`{"data":{"amount":{"$gt":100}}}`)); err != nil {
		t.Fatalf("UpsertEventSubscription other account: %v", err)
	}

	eventID := "evt-event-fanout-e2e"
	publish := api.PublishEventRequest{
		ID:     eventID,
		Source: "billing.stripe",
		Type:   "invoice.paid",
		Data:   json.RawMessage(`{"amount":150,"invoice_id":"inv-1"}`),
	}
	body, status := doReq(t, h, keyA, http.MethodPost, "/v1/events:publish", publish)
	if status != http.StatusAccepted {
		t.Fatalf("publish status=%d want 202: %s", status, body)
	}

	matched := waitForEventFanoutInvocations(t, store, appA.ID, 1, 10*time.Second)
	if len(matched) != 1 {
		t.Fatalf("matching invocations=%d want 1", len(matched))
	}
	if matched[0].Source != state.InvocationAsyncInvoke || matched[0].Method != "POST" || matched[0].Path != "/" {
		t.Fatalf("invocation route=%q %q %q, want async POST /", matched[0].Source, matched[0].Method, matched[0].Path)
	}
	if string(matched[0].Payload) == "" {
		t.Fatal("matched invocation payload is empty")
	}
	var headers map[string]string
	if err := json.Unmarshal(matched[0].Headers, &headers); err != nil {
		t.Fatalf("decode invocation headers: %v", err)
	}
	if headers["x-gregale-event-id"] != eventID ||
		headers["x-gregale-event-source"] != publish.Source ||
		headers["x-gregale-event-type"] != publish.Type {
		t.Fatalf("event headers=%+v, want id/source/type", headers)
	}

	// The same event ID can be observed more than once (for example after a
	// LISTEN reconnect). Deterministic invocation IDs must keep it single-shot
	// in PostgreSQL as well as MemStore.
	body, status = doReq(t, h, keyA, http.MethodPost, "/v1/events:publish", publish)
	if status != http.StatusAccepted {
		t.Fatalf("duplicate publish status=%d want 202: %s", status, body)
	}
	time.Sleep(500 * time.Millisecond)
	matched = waitForEventFanoutInvocations(t, store, appA.ID, 1, 2*time.Second)
	if len(matched) != 1 {
		t.Fatalf("duplicate publish created %d invocations, want 1", len(matched))
	}

	filtered, err := store.ListInvocationsForApp(ctx, filteredApp.ID)
	if err != nil {
		t.Fatalf("ListInvocationsForApp filtered: %v", err)
	}
	if len(filtered) != 0 {
		t.Fatalf("filtered app received %d invocations, want 0", len(filtered))
	}
	otherAccount, err := store.ListInvocationsForApp(ctx, otherAccountApp.ID)
	if err != nil {
		t.Fatalf("ListInvocationsForApp other account: %v", err)
	}
	if len(otherAccount) != 0 {
		t.Fatalf("cross-account app received %d invocations, want 0", len(otherAccount))
	}
}

func createEventFanoutApp(t *testing.T, h *e2etest.Harness, key, slug string) state.App {
	t.Helper()
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: slug,
		Type: string(state.AppTypeApp),
	})
	if status != http.StatusCreated {
		t.Fatalf("create app %q status=%d: %s", slug, status, body)
	}
	var response api.AppResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode app %q: %v; body=%s", slug, err, body)
	}
	return state.App{ID: response.ID, Slug: response.Slug}
}

func waitForEventFanoutInvocations(t *testing.T, store *state.PgStore, appID string, want int, timeout time.Duration) []state.Invocation {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last []state.Invocation
	for time.Now().Before(deadline) {
		rows, err := store.ListInvocationsForApp(context.Background(), appID)
		if err != nil {
			t.Fatalf("ListInvocationsForApp: %v", err)
		}
		last = rows
		if len(rows) >= want {
			return rows
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("app %s has %d invocations, want at least %d within %s", appID, len(last), want, timeout)
	return last
}
