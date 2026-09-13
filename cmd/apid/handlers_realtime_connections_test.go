package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
)

type recordingRealtimeOwner struct {
	sent            []realtime.Message
	closed          []string
	subscriptions   []string
	unsubscriptions []string
	published       []string
	queued          int
	err             error
}

func (o *recordingRealtimeOwner) Send(_ context.Context, _, _ string, message realtime.Message) error {
	o.sent = append(o.sent, message)
	return o.err
}

func (o *recordingRealtimeOwner) CloseConnection(_ context.Context, _, connectionID, reason string) error {
	o.closed = append(o.closed, connectionID+":"+reason)
	return o.err
}

func (o *recordingRealtimeOwner) Subscribe(_ context.Context, _, connectionID, channel string) error {
	o.subscriptions = append(o.subscriptions, connectionID+":"+channel)
	return o.err
}

func (o *recordingRealtimeOwner) Unsubscribe(_ context.Context, _, connectionID, channel string) error {
	o.unsubscriptions = append(o.unsubscriptions, connectionID+":"+channel)
	return o.err
}

func (o *recordingRealtimeOwner) Publish(_ context.Context, _, channel string, _ realtime.Message) (int, error) {
	o.published = append(o.published, channel)
	return o.queued, o.err
}

func createRealtimeEndpointForTest(t *testing.T, e testEnv) string {
	t.Helper()
	teardown := withTestRecipient(t)
	t.Cleanup(teardown)
	mustSeedApp(t, e, "rt-actions")
	rec := e.do(t, http.MethodPost, "/v1/apps/rt-actions/realtime/endpoints", api.CreateManagedRealtimeEndpointRequest{
		CallbackURL: "https://example.com/callback", CallbackAuthToken: "callback-secret",
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create endpoint: %d %s", rec.Code, rec.Body)
	}
	var endpoint api.ManagedRealtimeEndpointResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &endpoint); err != nil {
		t.Fatal(err)
	}
	return endpoint.ID
}

func TestManagedRealtimeConnectionOperationsRouteToOwner(t *testing.T) {
	e := setup(t, api.PlanPro)
	endpointID := createRealtimeEndpointForTest(t, e)
	owner := &recordingRealtimeOwner{queued: 3}
	e.s.WithRealtimeOwner(owner)
	encoded := base64.StdEncoding.EncodeToString([]byte("hello"))

	if rec := e.do(t, http.MethodPost, "/v1/apps/rt-actions/realtime/endpoints/"+endpointID+"/connections/conn-1/send", api.ManagedRealtimeMessageRequest{DataBase64: encoded, Binary: true}, nil); rec.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	if len(owner.sent) != 1 || string(owner.sent[0].Data) != "hello" || !owner.sent[0].Binary {
		t.Fatalf("send owner call: %+v", owner.sent)
	}

	if rec := e.do(t, http.MethodPost, "/v1/apps/rt-actions/realtime/endpoints/"+endpointID+"/connections/conn-1/close", api.ManagedRealtimeCloseRequest{Reason: "done"}, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("close: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, http.MethodPut, "/v1/apps/rt-actions/realtime/endpoints/"+endpointID+"/connections/conn-1/subscriptions/updates", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("subscribe: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, http.MethodDelete, "/v1/apps/rt-actions/realtime/endpoints/"+endpointID+"/connections/conn-1/subscriptions/updates", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, http.MethodPost, "/v1/apps/rt-actions/realtime/endpoints/"+endpointID+"/channels/updates/publish", api.ManagedRealtimeMessageRequest{DataBase64: encoded}, nil); rec.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body)
	}
	if len(owner.closed) != 1 || len(owner.subscriptions) != 1 || len(owner.unsubscriptions) != 1 || len(owner.published) != 1 || owner.published[0] != "updates" {
		t.Fatalf("owner calls: %+v", owner)
	}
}

func TestManagedRealtimeConnectionOperationsValidateAndFailClosed(t *testing.T) {
	e := setup(t, api.PlanPro)
	endpointID := createRealtimeEndpointForTest(t, e)
	owner := &recordingRealtimeOwner{}
	e.s.WithRealtimeOwner(owner)

	if rec := e.do(t, http.MethodPost, "/v1/apps/rt-actions/realtime/endpoints/"+endpointID+"/connections/conn-1/send", api.ManagedRealtimeMessageRequest{DataBase64: "%%%"}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad base64: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, http.MethodPut, "/v1/apps/rt-actions/realtime/endpoints/"+endpointID+"/connections/conn-1/subscriptions/bad%2Fchannel", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad channel: %d %s", rec.Code, rec.Body)
	}
	if len(owner.sent) != 0 || len(owner.subscriptions) != 0 {
		t.Fatal("invalid requests reached owner")
	}

	e.s.WithRealtimeOwner(nil)
	if rec := e.do(t, http.MethodPost, "/v1/apps/rt-actions/realtime/endpoints/"+endpointID+"/channels/updates/publish", api.ManagedRealtimeMessageRequest{DataBase64: "aGVsbG8="}, nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing owner: %d %s", rec.Code, rec.Body)
	}
}

func TestManagedRealtimeConnectionOperationsMapOwnerErrors(t *testing.T) {
	e := setup(t, api.PlanPro)
	endpointID := createRealtimeEndpointForTest(t, e)
	e.s.WithRealtimeOwner(&recordingRealtimeOwner{err: errors.New("owner offline")})
	rec := e.do(t, http.MethodPost, "/v1/apps/rt-actions/realtime/endpoints/"+endpointID+"/connections/conn-1/send", api.ManagedRealtimeMessageRequest{DataBase64: "aGVsbG8="}, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("owner error: %d %s", rec.Code, rec.Body)
	}
}
