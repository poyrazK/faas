package e2e_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/realtime"
)

// adr: 20261019102657 — the realtime preview end to end: real apid, realtimed
// and both gateways. A backend issues a subscription ticket for an endpoint
// declared by slug, a client subscribes over the v2 protocol through the
// public edge, a retained publish reaches it, and occupancy counts it.
func TestE2E_RealtimePreviewTicketRetainedPublishAndOccupancy(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// The endpoint's callbacks (connect, message, disconnect) land here.
	callbacks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(callbacks.Close)
	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd|e2etest.GatewaydPublic|e2etest.Realtimed, []string{
		"FAAS_EGRESS_ALLOW_LOOPBACK=1",
		"FAAS_REALTIME_PREVIEW_ACCOUNTS=*",
		"FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1",
		"FAAS_REALTIME_RESUME_PREVIEW_ENABLED=1",
		"FAAS_REALTIME_TICKETS_PREVIEW_ENABLED=1",
		"FAAS_REALTIME_OCCUPANCY_PREVIEW_ENABLED=1",
	})
	key := h.SeedAccount(context.Background(), api.PlanPro, "realtime-preview")
	call := func(method, path string, body any, want int, out any, headers ...map[string]string) {
		t.Helper()
		raw, status := doReq(t, h, key, method, path, body, headers...)
		if status != want {
			t.Fatalf("%s %s = %d %s, want %d", method, path, status, raw, want)
		}
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
			}
		}
	}
	call(http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "rt-e2e"}, http.StatusCreated, nil)
	var endpoint api.ManagedRealtimeEndpointResponse
	call(http.MethodPost, "/v1/apps/rt-e2e/realtime/endpoints", api.CreateManagedRealtimeEndpointRequest{
		CallbackURL: callbacks.URL, CallbackAuthToken: "e2e-callback-token", AuthMode: api.RealtimeAuthModeNone, Slug: "chat",
	}, http.StatusCreated, &endpoint)
	if endpoint.Slug != "chat" {
		t.Fatalf("endpoint = %+v, want slug chat", endpoint)
	}
	base := "/v1/apps/rt-e2e/realtime/endpoints/" + endpoint.ID
	var ticket api.RealtimeTicketResponse
	call(http.MethodPost, base+"/tickets", api.RealtimeTicketRequest{Principal: "user-1", Channels: []string{"lobby"}, TTLSeconds: 60},
		http.StatusCreated, &ticket)

	// apid registers the endpoint with realtimed asynchronously; retry the
	// handshake until it is known.
	url := "ws" + strings.TrimPrefix(h.EdgeURL(), "http") + realtime.ManagedPathPrefix + endpoint.ID
	dialer := websocket.Dialer{Subprotocols: []string{realtime.ResumeSubprotocol}, HandshakeTimeout: 5 * time.Second}
	var client *websocket.Conn
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, resp, err := dialer.Dial(url, http.Header{"Authorization": {"Bearer " + ticket.Ticket}})
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			client = conn
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("v2 handshake through the edge: %v (status %v)", err, resp)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.WriteJSON(map[string]any{"type": "subscribe", "channel": "lobby", "after": 0, "occupancy": true}); err != nil {
		t.Fatal(err)
	}
	next := func(want string) map[string]any {
		t.Helper()
		_ = client.SetReadDeadline(time.Now().Add(10 * time.Second))
		for {
			var frame map[string]any
			if err := client.ReadJSON(&frame); err != nil {
				t.Fatalf("waiting for %s: %v", want, err)
			}
			if frame["type"] == want {
				return frame
			}
			if frame["type"] == "error" {
				t.Fatalf("waiting for %s: error frame %v", want, frame)
			}
		}
	}
	next("subscribed")

	payload := base64.StdEncoding.EncodeToString([]byte("hello from the backend"))
	call(http.MethodPost, base+"/channels/lobby/publish?delivery=retained", api.ManagedRealtimeMessageRequest{DataBase64: payload},
		http.StatusOK, nil, map[string]string{"Idempotency-Key": "e2e-publish-1"})
	if message := next("message"); message["data_base64"] != payload {
		t.Fatalf("message = %v, want the retained publish", message)
	}

	var occupancy api.ManagedRealtimeOccupancyResponse
	call(http.MethodGet, base+"/occupancy?channel=lobby", nil, http.StatusOK, &occupancy)
	if len(occupancy.Channels) != 1 || occupancy.Channels[0].Subscribers != 1 {
		t.Fatalf("occupancy = %+v, want the one ticket subscriber", occupancy)
	}
}
