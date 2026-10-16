package scenarioconsumer_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/scenarioconsumer"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestScenarioConsumerLostResponse(t *testing.T) {
	// adr: 984 — a response lost after commit can be retried without a second identity.
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "consumer-http@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "intent-http", RAMMB: 128, Status: state.AppActive, PreviewOfSlug: "intent-http", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry})
	if err != nil {
		t.Fatal(err)
	}
	run := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, run, []state.ScenarioTestMember{{Workload: "api", AppID: app.ID}}); err != nil {
		t.Fatal(err)
	}
	req := api.ScenarioTestConsumerRequest{AppID: app.ID, ExpiresAt: expiry}
	intent, _ := api.ScenarioTestConsumerIntent(run, "customer", req)
	var mu sync.Mutex
	lose := true
	puts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/dev/test-runs/{run_id}/consumers/{name}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing authorization")
		}
		if r.Method == "PUT" {
			puts++
			if puts <= 3 && r.Header.Get("Idempotency-Key") != intent {
				t.Error("unstable SDK intent key")
			}
			if lose {
				lose = false
				rec := httptest.NewRecorder()
				scenarioconsumer.Serve(rec, r, account, store)
				if rec.Code != 200 {
					t.Errorf("creation: %d %s", rec.Code, rec.Body.String())
				}
				w.WriteHeader(503)
				return
			}
		}
		scenarioconsumer.Serve(w, r, account, store)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := api.NewClient(server.URL, "test-token")
	if _, err := client.EnsureScenarioTestConsumer(ctx, run, "customer", req); err == nil {
		t.Fatal("lost response was accepted")
	}
	receipt, err := client.LookupScenarioTestConsumer(ctx, run, "customer", req)
	if err != nil || receipt.SchemaVersion != 1 || receipt.IntentSHA256 != intent || receipt.Consumer == nil {
		t.Fatal(receipt, err)
	}
	id := receipt.Consumer.ID
	for i := 0; i < 2; i++ {
		again, err := client.EnsureScenarioTestConsumer(ctx, run, "customer", req)
		if err != nil || again.Consumer == nil || again.Consumer.ID != id {
			t.Fatal(again, err)
		}
	}
	rows, err := store.ListAPIConsumersForApp(ctx, account.ID, app.ID)
	if err != nil || len(rows) != 1 || puts != 3 {
		t.Fatal("duplicate creation", len(rows), puts, err)
	}
	absent, err := client.LookupScenarioTestConsumer(ctx, run, "missing", req)
	if err != nil || absent.SchemaVersion != 1 || absent.Consumer != nil {
		t.Fatal("versioned lookup absence", absent, err)
	}
	changed := req
	changed.ExpiresAt = changed.ExpiresAt.Add(time.Hour)
	if _, err := client.EnsureScenarioTestConsumer(ctx, run, "customer", changed); err == nil {
		t.Fatal("changed input accepted")
	}
	if _, err := store.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RevokeScenarioTestConsumer(ctx, run, "customer", req, app.ID); err == nil {
		t.Fatal("replacement ID revoked")
	}
	revoked, err := client.RevokeScenarioTestConsumer(ctx, run, "customer", req, id)
	if err != nil || revoked.Consumer == nil || revoked.Consumer.ID != id || revoked.Consumer.Status != "revoked" {
		t.Fatal("SDK revoke after soft deletion", revoked, err)
	}
}
