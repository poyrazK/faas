package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDevBridgeSelectedWebhookReplayPreservesOriginal(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*(*e.S()).DevBridgeEnabled()) = true
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	project, err := (*e.Store()).CreateProject(ctx, state.Project{AccountID: (*e.Acct()).ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (*e.Store()).CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "development"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "payments", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	original, err := (*e.Store()).EnqueueInvocation(ctx, state.Invocation{AccountID: (*e.Acct()).ID, AppID: app.ID, Source: state.InvocationInboundWebhook, State: state.InvocationPending, Method: "POST", Path: "/webhooks/stripe", Payload: []byte(`{"id":"evt_selected","type":"payment_intent.succeeded"}`), Headers: []byte(`{"content-type":"application/json","x-gregale-webhook-event-id":"evt_selected","x-gregale-webhook-provider":"stripe","authorization":"Bearer source-secret","stripe-signature":"expired-signature"}`), DueAt: time.Now(), CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	relay := devbridge.NewRelay(4)
	relayServer := httptest.NewServer(devbridge.NewServer(relay, (*e.Store()).DevBridgeByID, nil))
	defer relayServer.Close()
	(*(*e.S()).DevBridgeURL()) = relayServer.URL
	apiServer := httptest.NewServer((*e.H()))
	defer apiServer.Close()
	client := api.NewClient(apiServer.URL, (*e.Key()))
	session, err := client.CreateDevBridge(ctx, api.CreateDevBridgeRequest{App: app.Slug, Environment: "development", DeveloperID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	var deliveries atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deliveries.Add(1)
		body, _ := io.ReadAll(r.Body)
		if string(body) != string(original.Payload) || r.URL.Path != original.Path {
			t.Error("verified delivery changed")
		}
		if r.Header.Get("Stripe-Signature") != "" || r.Header.Get("Authorization") != "" || r.Header.Get(devbridge.TokenHeader) != "" {
			t.Error("replay leaked a credential or expired signature")
		}
		if r.Header.Get("X-Faas-Invocation-Source") != "inbound_webhook" || r.Header.Get("X-Gregale-Webhook-Development-Replay") != "true" || r.Header.Get("X-Gregale-Webhook-Original-Receipt") != original.ID {
			t.Error("verified development copy identity missing")
		}
		w.WriteHeader(204)
	}))
	defer local.Close()
	target, _ := url.Parse(local.URL)
	socket, handshake, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(apiServer.URL, "http")+"/v1/dev/bridges/"+session.Session.ID+"/connect", http.Header{devbridge.AccountHeader: []string{(*e.Acct()).ID}, devbridge.TokenHeader: []string{session.Credentials.AttachmentToken}})
	if handshake != nil {
		_ = handshake.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	defer relay.CloseSession(session.Session.ID)
	go func() { _ = devbridge.ServeLocal(ctx, devbridge.NewWebSocketConn(socket), target, 4) }()
	deadline := time.Now().Add(3 * time.Second)
	for !relay.Connected(session.Session.ID) {
		if time.Now().After(deadline) {
			t.Fatal("laptop did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	input := api.ReplayDevBridgeWebhookRequest{InvocationID: original.ID, RequestToken: session.Credentials.RequestToken, IdempotencyKey: "selected-replay"}
	for n := 0; n < 2; n++ {
		out, err := client.ReplayDevBridgeWebhook(ctx, session.Session.ID, input)
		if err != nil || out.ID != devbridge.WebhookReplayID(session.Session.ID, input.IdempotencyKey) || out.State != "completed" || out.HTTPStatus != 204 {
			t.Fatalf("replay: %+v err=%v", out, err)
		}
	}
	if deliveries.Load() != 1 {
		t.Fatal("idempotent replay duplicated local delivery")
	}
	preserved, err := (*e.Store()).InvocationByID(ctx, original.ID)
	if err != nil || preserved.State != original.State || preserved.Attempts != original.Attempts || string(preserved.Payload) != string(original.Payload) {
		t.Fatal("original delivery mutated")
	}
	input.RequestToken = session.Credentials.AttachmentToken
	if _, err := client.ReplayDevBridgeWebhook(ctx, session.Session.ID, input); err == nil {
		t.Fatal("attachment credential authorized replay routing")
	}
	if err := client.RevokeDevBridge(ctx, session.Session.ID); err != nil {
		t.Fatal(err)
	}
	out, err := client.GetDevBridgeWebhookReplay(ctx, session.Session.ID, devbridge.WebhookReplayID(session.Session.ID, input.IdempotencyKey))
	if err != nil || out.HTTPStatus != 204 {
		t.Fatal("owned receipt lost after revocation")
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), session.Credentials.RequestToken) || strings.Contains(string(b), "source-secret") {
		t.Fatal("receipt exposed secrets")
	}
}
