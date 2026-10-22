package apidserver_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// Change hooks (ADR-20261022121642): a Data API runtime publishes recorded
// row changes as producer-key events under its own app's source.
func TestDataAPIChangeEventsPublishUnderTheAppSource(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "shop-data", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:data-api", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 256, "default-local", "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "events-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if *(*e.S()).WorkloadVerifier(), err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer); err != nil {
		t.Fatal(err)
	}
	bearer := func(audience string) map[string]string {
		token, err := signer.Mint(time.Now(), (*e.Acct()).ID, app.ID, instance.ID, audience)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + token.AccessToken}
	}
	change := func(id, status string) api.DataAPIChangeEvent {
		return api.DataAPIChangeEvent{ID: id, Type: "api.orders.updated", Data: json.RawMessage(`{"schema":"api","table":"orders","op":"update","key":{"id":7},"new":{"id":7,"status":"` + status + `"}}`)}
	}
	first, second := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	publish := func(events ...api.DataAPIChangeEvent) api.DataAPIChangeEventBatchResponse {
		t.Helper()
		rec := e.Do(t, http.MethodPost, "/v1/runtime/data-api/events", api.DataAPIChangeEventBatch{Version: 1, Events: events}, bearer(workloadidentity.DataAPIEventsAudience))
		var out api.DataAPIChangeEventBatchResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.Results) != len(events) {
			t.Fatalf("publish = %d: %s", rec.Code, rec.Body.String())
		}
		return out
	}

	out := publish(change(first, "paid"), change(second, "shipped"))
	if out.Source != "app."+app.ID || out.Results[0].Status != "accepted" || out.Results[1].Status != "accepted" || out.Results[0].Receipt == nil {
		t.Fatalf("first publication %+v", out)
	}
	// A relay that lost its settlement republishes the same changes.
	if out := publish(change(first, "paid")); out.Results[0].Status != "duplicate" || out.Results[0].Receipt.ID != publish(change(first, "paid")).Results[0].Receipt.ID {
		t.Fatalf("republication %+v", out)
	}
	// A change's identity cannot be reused for different content.
	if out := publish(change(first, "refunded")); out.Results[0].Status != "rejected" || out.Results[0].Retryable {
		t.Fatalf("changed content %+v", out)
	}
	// Only a change hooks workload token is accepted.
	rec := e.Do(t, http.MethodPost, "/v1/runtime/data-api/events", api.DataAPIChangeEventBatch{Version: 1, Events: []api.DataAPIChangeEvent{change(first, "paid")}}, bearer(workloadidentity.DataAPIUsageAudience))
	apidserver.AssertProblem(t, rec, http.StatusUnauthorized, api.CodeUnauthorized)
}
