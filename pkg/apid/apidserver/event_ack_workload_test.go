package apidserver_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// A deferred-ack worker settles its delivery with the instance's own
// gregale:events workload token instead of a deploy:write API key
// (ADR-20261110200000). The token must name this app and a live instance;
// other audiences and neighbours' tokens are refused.
func TestEventAcknowledgmentAcceptsWorkloadToken(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	acct := *e.Acct()
	ctx := t.Context()
	for _, slug := range []string{"ack-token-worker", "ack-neighbour"} {
		if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: slug, RAMMB: 256}, nil); r.Code != http.StatusCreated {
			t.Fatalf("create app %s: %d %s", slug, r.Code, r.Body)
		}
	}
	app, err := store.AppBySlug(ctx, "ack-token-worker")
	if err != nil {
		t.Fatal(err)
	}
	neighbour, err := store.AppBySlug(ctx, "ack-neighbour")
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := store.UpsertEventSubscription(ctx, acct.ID, app.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	lease := 60
	if r := e.Do(t, http.MethodPut, "/v1/apps/ack-token-worker/event-subscriptions/"+sub.ID+"/acknowledgment-policy",
		api.EventAcknowledgmentPolicyRequest{LeaseSeconds: &lease}, nil); r.Code != http.StatusOK {
		t.Fatalf("set policy: %d %s", r.Code, r.Body)
	}
	headers, _ := json.Marshal(map[string]string{
		"x-gregale-event-acknowledgment-lease-seconds": "60",
		"x-gregale-event-acceptance-id":                "1",
		"x-gregale-event-subscription-id":              sub.ID,
	})
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{ID: uuid.NewString(), AppID: app.ID, AccountID: acct.ID,
		Source: state.InvocationAsyncInvoke, State: state.InvocationPending, Method: http.MethodPost, Path: "/",
		Payload: json.RawMessage(`{"id":"evt-1"}`), Headers: headers, CreatedAt: time.Now(), DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimInvocation(ctx, inv.ID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	var acks state.EventAcknowledgmentStore = store
	deliveryToken, err := acks.OfferEventAcknowledgment(ctx, claimed, 3, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acks.AcceptEventAcknowledgment(ctx, claimed, deliveryToken); err != nil {
		t.Fatal(err)
	}

	// Deliveries reach stage deployments too, so a staging instance settles
	// its own; a parked instance and a neighbour's instance cannot.
	instance := func(appID, scope, st string) state.Instance {
		t.Helper()
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Scope: scope, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		in, err := store.CreateInstanceWithMode(ctx, appID, dep.ID, st, 256, "default-local", "", string(state.InstanceModeWorker))
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	live := instance(app.ID, "staging", string(state.StateRunning))
	parked := instance(app.ID, "", string(state.StateParked))
	other := instance(neighbour.ID, "", string(state.StateRunning))

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
	bearer := func(appID string, in state.Instance, environment, audience string) map[string]string {
		t.Helper()
		token, err := signer.MintEnvironment(time.Now(), acct.ID, appID, in.ID, environment, audience)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + token.AccessToken}
	}

	ackPath := "/v1/apps/ack-token-worker/event-deliveries/" + inv.ID + "/acknowledgment"
	renew := api.EventAcknowledgmentRequest{Token: deliveryToken}
	for name, auth := range map[string]map[string]string{
		"queues audience":      bearer(app.ID, live, "staging", workloadidentity.QueuesAudience),
		"parked instance":      bearer(app.ID, parked, "", workloadidentity.EventsAudience),
		"neighbour's token":    bearer(neighbour.ID, other, "", workloadidentity.EventsAudience),
		"neighbour's instance": bearer(app.ID, other, "", workloadidentity.EventsAudience),
	} {
		if r := e.Do(t, http.MethodPost, ackPath+"/renew", renew, auth); r.Code != http.StatusUnauthorized {
			t.Fatalf("%s renewed the delivery: %d %s", name, r.Code, r.Body)
		}
	}

	auth := bearer(app.ID, live, "staging", workloadidentity.EventsAudience)
	if r := e.Do(t, http.MethodGet, ackPath, nil, auth); r.Code != http.StatusOK {
		t.Fatalf("workload read = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPost, ackPath+"/renew", api.EventAcknowledgmentRequest{Token: "not-the-token"}, auth); r.Code == http.StatusOK {
		t.Fatalf("workload identity bypassed the delivery-token fence: %s", r.Body)
	}
	if r := e.Do(t, http.MethodPost, ackPath+"/renew", renew, auth); r.Code != http.StatusOK {
		t.Fatalf("workload renew = %d %s", r.Code, r.Body)
	}
	complete := api.EventAcknowledgmentRequest{Token: deliveryToken, Result: json.RawMessage(`{"ok":true}`)}
	if r := e.Do(t, http.MethodPost, ackPath+"/complete", complete, auth); r.Code != http.StatusOK {
		t.Fatalf("workload complete = %d %s", r.Code, r.Body)
	}
	if got, err := store.InvocationByID(ctx, inv.ID); err != nil || got.State != state.InvocationCompleted {
		t.Fatalf("invocation after workload completion = %+v %v", got, err)
	}
}
