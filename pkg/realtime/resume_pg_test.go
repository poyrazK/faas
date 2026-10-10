package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type pgResumeHistoryReader struct{ store *state.PgStore }

func (r pgResumeHistoryReader) ReadChannelHistory(ctx context.Context, endpointID, channel string, after int64, limit int) (state.ManagedRealtimeChannelHistory, error) {
	return r.store.ReadManagedRealtimeChannelHistory(ctx, endpointID, channel, after, limit)
}

func startPGResumeNode(t *testing.T, endpointID, appID, accountID string, reader pgResumeHistoryReader, hooks *testChannelHooks) (*Manager, *httptest.Server) {
	t.Helper()
	m := NewManager(Config{
		JWTAuthorizer: &recordingJWTAuthorizer{}, ResumePreview: true,
		HistoryReader: reader, ResumePollInterval: 15 * time.Millisecond,
	}, hooks)
	if err := m.RegisterEndpoint(Endpoint{
		ID: endpointID, AppID: appID, AccountID: accountID, CallbackURL: "https://app.example",
		ClientAuth: AuthPolicy{
			Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"},
		},
	}); err != nil {
		_ = m.Close()
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { _ = m.Close(); server.Close() })
	return m, server
}

func dialPGResumeNode(t *testing.T, server *httptest.Server, endpointID string) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{ResumeSubprotocol}}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + ManagedPathPrefix + endpointID
	client, response, err := dialer.Dial(url, http.Header{"Authorization": {"Bearer test-jwt"}})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestResumePostgresContinuityAcrossOwnersAndRestart(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "realtime-fleet@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "realtime-fleet", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		AccountID: account.ID, AppID: app.ID, Enabled: true,
		CallbackURL: "https://app.example", ConnectPath: "/realtime/connect",
		MessagePath: "/realtime/message", DisconnectPath: "/realtime/disconnect",
		CallbackAuthTokenSealed: []byte("sealed-callback-token"),
		AuthTokenSealed:         []byte("sealed-client-token"),
		AuthMode:                api.RealtimeAuthModeOIDCJWT, AuthIssuer: "https://issuer.example",
		AuthJWKSURL:    "https://issuer.example/.well-known/jwks.json",
		AuthAlgorithms: []string{"RS256"},
	}, 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	appendMessage := func(data string) int64 {
		t.Helper()
		message, err := store.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte(data), false, "")
		if err != nil {
			t.Fatal(err)
		}
		return message.Sequence
	}
	for _, data := range []string{"one", "two"} {
		appendMessage(data)
	}
	hooks := &testChannelHooks{allowed: true}
	reader := pgResumeHistoryReader{store: store}
	firstOwner, firstServer := startPGResumeNode(t, endpoint.ID, app.ID, account.ID, reader, hooks)
	firstClient := dialPGResumeNode(t, firstServer, endpoint.ID)
	sendResumeTestFrame(t, firstClient, resumeClientFrame{Type: "subscribe", Channel: "updates", After: 0})
	if frame := readResumeTestFrame(t, firstClient); frame.Type != "subscribed" || frame.LatestSequence != 2 {
		t.Fatalf("first subscription = %+v", frame)
	}
	if frame := readResumeTestFrame(t, firstClient); frame.Type != "presence" || frame.Event != "snapshot" || frame.Channel != "updates" || !frame.Complete || len(frame.Members) != 0 {
		t.Fatalf("first presence snapshot = %+v", frame)
	}
	for sequence, data := range []string{"b25l", "dHdv"} {
		frame := readResumeTestFrame(t, firstClient)
		if frame.Type != "message" || frame.Sequence != int64(sequence+1) || frame.DataBase64 != data {
			t.Fatalf("initial replay %d = %+v", sequence+1, frame)
		}
	}
	sendResumeTestFrame(t, firstClient, resumeClientFrame{Type: "ack", Channel: "updates", Sequence: 2})
	if frame := readResumeTestFrame(t, firstClient); frame.Type != "acknowledged" || frame.Sequence != 2 {
		t.Fatalf("first acknowledgement = %+v", frame)
	}
	_ = firstOwner.Close()
	_ = firstClient.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := firstClient.ReadMessage(); err == nil {
		t.Fatal("first owner shutdown left the client connected")
	}
	_ = firstClient.Close()
	if sequence := appendMessage("three"); sequence != 3 {
		t.Fatalf("offline sequence = %d", sequence)
	}
	if sequence := appendMessage("four"); sequence != 4 {
		t.Fatalf("offline sequence = %d", sequence)
	}
	secondOwner, secondServer := startPGResumeNode(t, endpoint.ID, app.ID, account.ID, reader, hooks)
	secondClient := dialPGResumeNode(t, secondServer, endpoint.ID)
	sendResumeTestFrame(t, secondClient, resumeClientFrame{Type: "subscribe", Channel: "updates", After: 2})
	if frame := readResumeTestFrame(t, secondClient); frame.Type != "subscribed" || frame.LatestSequence != 4 {
		t.Fatalf("second subscription = %+v", frame)
	}
	if frame := readResumeTestFrame(t, secondClient); frame.Type != "presence" || frame.Event != "snapshot" || frame.Channel != "updates" || !frame.Complete || len(frame.Members) != 0 {
		t.Fatalf("second presence snapshot = %+v", frame)
	}
	for sequence, data := range []string{"dGhyZWU=", "Zm91cg=="} {
		frame := readResumeTestFrame(t, secondClient)
		if frame.Type != "message" || frame.Sequence != int64(sequence+3) || frame.DataBase64 != data {
			t.Fatalf("cross-owner replay %d = %+v", sequence+3, frame)
		}
	}
	if sequence := appendMessage("five"); sequence != 5 {
		t.Fatalf("live sequence = %d", sequence)
	}
	if frame := readResumeTestFrame(t, secondClient); frame.Type != "message" || frame.Sequence != 5 || frame.DataBase64 != "Zml2ZQ==" {
		t.Fatalf("live delivery = %+v", frame)
	}
	sendResumeTestFrame(t, secondClient, resumeClientFrame{Type: "ack", Channel: "updates", Sequence: 5})
	if frame := readResumeTestFrame(t, secondClient); frame.Type != "acknowledged" || frame.Sequence != 5 {
		t.Fatalf("second acknowledgement = %+v", frame)
	}
	_ = secondOwner.Close()
	_ = secondClient.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := secondClient.ReadMessage(); err == nil {
		t.Fatal("second owner shutdown left the client connected")
	}
	_ = secondClient.Close()
	if sequence := appendMessage("six"); sequence != 6 {
		t.Fatalf("restart-gap sequence = %d", sequence)
	}
	_, restartedServer := startPGResumeNode(t, endpoint.ID, app.ID, account.ID, reader, hooks)
	restartedClient := dialPGResumeNode(t, restartedServer, endpoint.ID)
	sendResumeTestFrame(t, restartedClient, resumeClientFrame{Type: "subscribe", Channel: "updates", After: 5})
	if frame := readResumeTestFrame(t, restartedClient); frame.Type != "subscribed" || frame.LatestSequence != 6 {
		t.Fatalf("restarted subscription = %+v", frame)
	}
	if frame := readResumeTestFrame(t, restartedClient); frame.Type != "presence" || frame.Event != "snapshot" || frame.Channel != "updates" || !frame.Complete || len(frame.Members) != 0 {
		t.Fatalf("restarted presence snapshot = %+v", frame)
	}
	if frame := readResumeTestFrame(t, restartedClient); frame.Type != "message" || frame.Sequence != 6 || frame.DataBase64 != "c2l4" {
		t.Fatalf("restarted replay = %+v", frame)
	}
	_ = restartedClient.Close()
	if _, err := pool.Exec(ctx, `update managed_realtime_channel_messages set created_at = $4 where endpoint_id = $1 and channel = $2 and sequence = $3`,
		endpoint.ID, "updates", 6, time.Now().Add(-state.ManagedRealtimeHistoryRetention-time.Minute)); err != nil {
		t.Fatal(err)
	}
	staleClient := dialPGResumeNode(t, restartedServer, endpoint.ID)
	sendResumeTestFrame(t, staleClient, resumeClientFrame{Type: "subscribe", Channel: "updates", After: 5})
	if frame := readResumeTestFrame(t, staleClient); frame.Type != "resync_required" || frame.OldestSequence != 7 || frame.LatestSequence != 6 {
		t.Fatalf("expired cursor = %+v", frame)
	}
	hooks.mu.Lock()
	events := append([]Event(nil), hooks.events...)
	hooks.mu.Unlock()
	if len(events) != 4 {
		t.Fatalf("channel authorization decisions = %d, want one per subscription", len(events))
	}
	for _, event := range events {
		if event.EndpointID != endpoint.ID || event.AppID != app.ID || event.AccountID != account.ID ||
			event.Channel != "updates" || event.Permission != "read" || event.Principal != "user-123" {
			t.Fatalf("channel decision = %+v", event)
		}
	}
}
