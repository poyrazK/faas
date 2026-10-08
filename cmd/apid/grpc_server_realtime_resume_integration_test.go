package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

type realtimeResumeIntegrationJWTAuthorizer struct{}

func (realtimeResumeIntegrationJWTAuthorizer) Authorize(context.Context, string, realtime.AuthPolicy) (string, error) {
	return "integration-user", nil
}

type realtimeResumeIntegrationHooks struct{ realtime.NopHooks }

func (realtimeResumeIntegrationHooks) AuthorizeChannel(context.Context, realtime.Event) (bool, error) {
	return true, nil
}

type realtimeResumeIntegrationClientFrame struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
	After   int64  `json:"after,omitempty"`
}

type realtimeResumeIntegrationServerFrame struct {
	Type           string `json:"type"`
	Channel        string `json:"channel"`
	Code           string `json:"code"`
	Sequence       int64  `json:"sequence"`
	LatestSequence int64  `json:"latest_sequence"`
	DataBase64     string `json:"data_base64"`
}

func TestRealtimeHistoryRPCDrivesResumeWebSocket(t *testing.T) {
	ctx := context.Background()
	store, endpoint, _ := newRealtimeRouteReportFixture(t)
	for _, data := range []string{"one", "two"} {
		if _, err := store.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte(data), false, ""); err != nil {
			t.Fatalf("append retained message %q: %v", data, err)
		}
	}

	// Use the same registered private gRPC service and generated client used by
	// realtimed, then attach that client to an actual resumable WebSocket owner.
	historyClient := newRealtimeHistoryTestClient(t, store, "test-node")
	manager := realtime.NewManager(realtime.Config{
		JWTAuthorizer:      realtimeResumeIntegrationJWTAuthorizer{},
		ResumePreview:      true,
		HistoryReader:      historyClient,
		ResumePollInterval: 15 * time.Millisecond,
	}, realtimeResumeIntegrationHooks{})
	if err := manager.RegisterEndpoint(realtime.Endpoint{
		ID: endpoint.ID, AppID: endpoint.AppID, AccountID: endpoint.AccountID,
		CallbackURL: endpoint.CallbackURL,
		ClientAuth: realtime.AuthPolicy{
			Mode: realtime.AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"},
		},
	}); err != nil {
		_ = manager.Close()
		t.Fatalf("register realtime endpoint: %v", err)
	}
	server := httptest.NewServer(manager.Handler())
	t.Cleanup(func() {
		_ = manager.Close()
		server.Close()
	})

	client := dialRealtimeResumeIntegrationWebSocket(t, server, endpoint.ID)
	writeRealtimeResumeIntegrationFrame(t, client, realtimeResumeIntegrationClientFrame{
		Type: "subscribe", Channel: "updates", After: 0,
	})
	if frame := readRealtimeResumeIntegrationFrame(t, client); frame.Type != "subscribed" || frame.LatestSequence != 2 {
		t.Fatalf("subscription frame = %+v, want subscribed through sequence 2", frame)
	}
	for sequence, data := range []string{"b25l", "dHdv"} {
		frame := readRealtimeResumeIntegrationFrame(t, client)
		if frame.Type != "message" || frame.Sequence != int64(sequence+1) || frame.DataBase64 != data {
			t.Fatalf("replay frame %d = %+v", sequence+1, frame)
		}
	}

	if _, err := store.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("three"), false, ""); err != nil {
		t.Fatalf("append live retained message: %v", err)
	}
	if frame := readRealtimeResumeIntegrationFrame(t, client); frame.Type != "message" || frame.Sequence != 3 || frame.DataBase64 != "dGhyZWU=" {
		t.Fatalf("polled message frame = %+v", frame)
	}

	// apid revokes history reads as soon as an endpoint is disabled. The owner
	// must surface that RPC failure rather than silently accepting a cursor.
	disabled := false
	if _, err := store.UpdateManagedRealtimeEndpoint(ctx, endpoint.ID, state.UpdateManagedRealtimeEndpointParams{Enabled: &disabled}); err != nil {
		t.Fatalf("disable realtime endpoint: %v", err)
	}
	newClient := dialRealtimeResumeIntegrationWebSocket(t, server, endpoint.ID)
	writeRealtimeResumeIntegrationFrame(t, newClient, realtimeResumeIntegrationClientFrame{
		Type: "subscribe", Channel: "updates", After: 0,
	})
	if frame := readRealtimeResumeIntegrationFrame(t, newClient); frame.Type != "error" || frame.Code != "history_read_failed" {
		t.Fatalf("revoked history read frame = %+v, want history_read_failed", frame)
	}
}

func dialRealtimeResumeIntegrationWebSocket(t *testing.T, server *httptest.Server, endpointID string) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{realtime.ResumeSubprotocol}}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + realtime.ManagedPathPrefix + endpointID
	client, response, err := dialer.Dial(url, http.Header{"Authorization": {"Bearer integration-token"}})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial resumable websocket: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func writeRealtimeResumeIntegrationFrame(t *testing.T, client *websocket.Conn, frame realtimeResumeIntegrationClientFrame) {
	t.Helper()
	if err := client.WriteJSON(frame); err != nil {
		t.Fatalf("write resume frame: %v", err)
	}
}

func readRealtimeResumeIntegrationFrame(t *testing.T, client *websocket.Conn) realtimeResumeIntegrationServerFrame {
	t.Helper()
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set resume read deadline: %v", err)
	}
	_, data, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("read resume frame: %v", err)
	}
	var frame realtimeResumeIntegrationServerFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatalf("decode resume frame %q: %v", data, err)
	}
	return frame
}
