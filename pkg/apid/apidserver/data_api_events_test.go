package apidserver_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// changeHooksRuntime is a running Data API instance of a new app on plan:
// the app, and a bearer for audience from its workload identity.
func changeHooksRuntime(t *testing.T, plan api.Plan) (apidserver.TestEnv, state.App, func(audience string) map[string]string) {
	t.Helper()
	ctx := context.Background()
	e := apidserver.Setup(t, plan)
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
	return e, app, bearer
}

// Change hooks (ADR-20261022121642): a Data API runtime publishes recorded
// row changes as producer-key events under its own app's source.
func TestDataAPIChangeEventsPublishUnderTheAppSource(t *testing.T) {
	e, app, bearer := changeHooksRuntime(t, api.PlanPro)
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
	if out.Source != "app."+uuid.MustParse(app.ID).String() || out.Results[0].Status != "accepted" || out.Results[1].Status != "accepted" || out.Results[0].Receipt == nil {
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

// changeEvents is n distinct inserts, numbered from first.
func changeEvents(first, n int) api.DataAPIChangeEventBatch {
	batch := api.DataAPIChangeEventBatch{Version: 1}
	for i := first; i < first+n; i++ {
		batch.Events = append(batch.Events, api.DataAPIChangeEvent{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i),
			Type: "api.orders.inserted", Data: json.RawMessage(fmt.Sprintf(`{"key":{"id":%d}}`, i))})
	}
	return batch
}

// Any app can mint a change hooks workload token, and the route belongs to
// the data-api capability, so it applies the events pause itself, as the
// platform-tenant route does (bug hunt 36).
func TestDataAPIChangeEventsFollowTheEventsFlag(t *testing.T) {
	e, _, bearer := changeHooksRuntime(t, api.PlanPro)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	publish := func(first int) *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPost, "/v1/runtime/data-api/events", changeEvents(first, 1), bearer(workloadidentity.DataAPIEventsAudience))
	}
	// events is preview: off for an account without an allow rule.
	eventsPaused(t, publish(1), "for this account")

	key := capabilityFlag(t, "events")
	allow := true
	srv.SeedPlatformFlag(key, platformflags.Definition{Enabled: true,
		Rules: []platformflags.Rule{{ID: "partner", Accounts: []string{(*e.Acct()).ID}, Value: &allow}}})
	if r := publish(2); r.Code != http.StatusOK {
		t.Fatalf("publish with events released = %d %s", r.Code, r.Body)
	}

	srv.SeedPlatformFlag(key, platformflags.Definition{Enabled: false})
	eventsPaused(t, publish(3), "platform-wide")
}

// Each change event counts toward the account's publish rate, as every other
// publication's does (ADR-20261026140000, bug hunt 36): a Free account's
// burst admits one full publication, and the next waits for the bucket.
func TestDataAPIChangeEventsCountTowardThePublishRate(t *testing.T) {
	e, _, bearer := changeHooksRuntime(t, api.PlanFree)
	limits, _ := api.LimitsFor(api.PlanFree)
	burst := limits.EventPublish.Burst
	if burst <= 0 || burst > api.DataAPIChangeEventsBatchMaxEvents {
		t.Fatalf("Free publish burst %d does not fit one publication of at most %d", burst, api.DataAPIChangeEventsBatchMaxEvents)
	}
	publish := func(first int) *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPost, "/v1/runtime/data-api/events", changeEvents(first, burst), bearer(workloadidentity.DataAPIEventsAudience))
	}
	if r := publish(0); r.Code != http.StatusOK {
		t.Fatalf("first publication = %d %s", r.Code, r.Body)
	}
	r := publish(burst)
	apidserver.AssertProblem(t, r, http.StatusTooManyRequests, api.CodeEventPublishRateLimited)
	if r.Header().Get("Retry-After") == "" {
		t.Fatal("a rate-limited publication carries no Retry-After")
	}
}
