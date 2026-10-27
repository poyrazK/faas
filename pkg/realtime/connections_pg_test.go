package realtime

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgTicketReader is the apid ticket read path, without the gRPC hop.
type pgTicketReader struct{ store *state.PgStore }

func (r pgTicketReader) ReadSubscriptionTicket(ctx context.Context, ep, token string) (state.RealtimeSubscriptionTicket, error) {
	return r.store.ReadRealtimeSubscriptionTicket(ctx, ep, token)
}

// pgConnectionsNode is one realtime node serving a Postgres-backed endpoint
// over a real HTTP listener: history, tickets and access all come from the
// store the way apid serves them in production.
type pgConnectionsNode struct {
	store    *state.PgStore
	endpoint string
	server   *httptest.Server
	hooks    *testChannelHooks
	manager  *Manager
}

// delivered is the endpoint's usage count of payload deliveries not yet
// drained by a usage recorder (none runs in these tests).
func (n *pgConnectionsNode) delivered(t *testing.T) int64 {
	t.Helper()
	value, ok := n.manager.endpoints.Load(n.endpoint)
	if !ok {
		t.Fatal("endpoint not registered")
	}
	return value.(*endpointState).usage.deliveredMessages.Load()
}

// waitDelivered waits for the endpoint's delivered-message usage to reach
// want. SSE counts an event after flushing it, so the client can read the
// event before the count moves.
func (n *pgConnectionsNode) waitDelivered(t *testing.T, want int64, why string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for got := n.delivered(t); got != want; got = n.delivered(t) {
		if got > want || time.Now().After(deadline) {
			t.Fatalf("delivered usage = %d, want %d %s", got, want, why)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func startPGConnectionsNode(t *testing.T) *pgConnectionsNode {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "realtime-connections@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "realtime-connections", Type: state.AppTypeApp,
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
	hooks := &testChannelHooks{allowed: true}
	m := NewManager(Config{
		JWTAuthorizer: &recordingJWTAuthorizer{}, ResumePreview: true, ResumePollInterval: 15 * time.Millisecond,
		HistoryReader: pgResumeHistoryReader{store: store},
		TicketPreview: true, TicketReader: pgTicketReader{store: store},
		OccupancyPreview: true,
	}, hooks)
	if err := m.RegisterEndpoint(Endpoint{Preview: true,
		ID: endpoint.ID, AppID: app.ID, AccountID: account.ID, CallbackURL: "https://app.example",
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
	return &pgConnectionsNode{store: store, endpoint: endpoint.ID, server: server, hooks: hooks, manager: m}
}

func (n *pgConnectionsNode) publish(t *testing.T, channel, data string) {
	t.Helper()
	if _, err := n.store.AppendManagedRealtimeChannelMessage(t.Context(), n.endpoint, channel, []byte(data), false, ""); err != nil {
		t.Fatal(err)
	}
}

func (n *pgConnectionsNode) ticket(t *testing.T, principal string, ttl int, channels ...string) string {
	t.Helper()
	token, _, err := n.store.IssueRealtimeSubscriptionTicket(t.Context(), n.endpoint, principal, channels, ttl)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (n *pgConnectionsNode) changeAccess(t *testing.T, principal, action, channel string) {
	t.Helper()
	if _, err := n.store.ChangeManagedRealtimeAccess(t.Context(), n.endpoint, principal, action, channel); err != nil {
		t.Fatal(err)
	}
}

type sseTestEvent struct {
	ID, Event string
	Data      map[string]any
}

// openSSE starts an SSE stream. A non-200 response is returned with a nil
// event channel; otherwise events arrive on the channel until the stream ends.
func (n *pgConnectionsNode) openSSE(t *testing.T, token, channel, lastEventID string) (int, <-chan sseTestEvent) {
	t.Helper()
	return n.openSSEQuery(t, token, "channel="+url.QueryEscape(channel), lastEventID)
}

// openSSEQuery is openSSE with a raw query after transport=sse.
func (n *pgConnectionsNode) openSSEQuery(t *testing.T, token, query, lastEventID string) (int, <-chan sseTestEvent) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	target := n.server.URL + ManagedPathPrefix + n.endpoint + "?transport=sse&" + query
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return resp.StatusCode, nil
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	t.Cleanup(func() { cancel(); _ = resp.Body.Close() })
	events := make(chan sseTestEvent, 64)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(resp.Body)
		var ev sseTestEvent
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				events <- ev
				ev = sseTestEvent{}
			case strings.HasPrefix(line, "id: "):
				ev.ID = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				ev.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.Data)
			}
		}
	}()
	return resp.StatusCode, events
}

// nextSSE returns the next non-heartbeat event, or ok=false when the stream
// ended.
func nextSSE(t *testing.T, events <-chan sseTestEvent, wait time.Duration) (sseTestEvent, bool) {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return sseTestEvent{}, false
			}
			if ev.Event != "heartbeat" {
				return ev, true
			}
		case <-deadline:
			t.Fatalf("no SSE event within %s", wait)
		}
	}
}

func expectSSEMessage(t *testing.T, events <-chan sseTestEvent, id, dataBase64 string) {
	t.Helper()
	ev, ok := nextSSE(t, events, 3*time.Second)
	if !ok || ev.Event != "message" || ev.ID != id || ev.Data["sequence"] != id || ev.Data["data_base64"] != dataBase64 {
		t.Fatalf("event = %+v (open %v), want message %s %s", ev, ok, id, dataBase64)
	}
}

func TestSSETicketStreamsPostgresHistoryAndEndsOnRevoke(t *testing.T) {
	node := startPGConnectionsNode(t)
	node.publish(t, "updates", "one")
	node.publish(t, "updates", "two")
	ticket := node.ticket(t, "user-1", 60, "updates")

	status, events := node.openSSE(t, ticket, "updates", "")
	if status != http.StatusOK {
		t.Fatalf("ticket SSE status = %d", status)
	}
	if ev, _ := nextSSE(t, events, 3*time.Second); ev.Event != "subscribed" || ev.Data["latest_sequence"] != "2" || ev.Data["sequence"] != "0" {
		t.Fatalf("first event = %+v, want subscribed at 0 of 2", ev)
	}
	expectSSEMessage(t, events, "1", "b25l")
	expectSSEMessage(t, events, "2", "dHdv")
	// Live tail: a publish after the replay arrives on the open stream.
	node.publish(t, "updates", "three")
	expectSSEMessage(t, events, "3", "dGhyZWU=")

	// Reconnecting from Last-Event-ID resumes after it.
	status, resumed := node.openSSE(t, ticket, "updates", "2")
	if status != http.StatusOK {
		t.Fatalf("resume status = %d", status)
	}
	if ev, _ := nextSSE(t, resumed, 3*time.Second); ev.Event != "subscribed" || ev.Data["sequence"] != "2" {
		t.Fatalf("resume subscribed = %+v", ev)
	}
	expectSSEMessage(t, resumed, "3", "dGhyZWU=")
	// Usage counts message events only: 3 on the first stream, 1 on the resume.
	node.waitDelivered(t, 4, "SSE message events")

	if status, _ := node.openSSE(t, ticket, "other", ""); status != http.StatusForbidden {
		t.Fatalf("channel outside the ticket status = %d, want 403", status)
	}
	if status, _ := node.openSSE(t, "grt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "updates", ""); status != http.StatusUnauthorized {
		t.Fatalf("unknown ticket status = %d, want 401", status)
	}

	// Revoking the channel ends both open streams with access_revoked.
	node.changeAccess(t, "user-1", "revoke_channel", "updates")
	for name, stream := range map[string]<-chan sseTestEvent{"first": events, "resumed": resumed} {
		ev, ok := nextSSE(t, stream, 5*time.Second)
		if !ok || ev.Event != "error" || ev.Data["code"] != "access_revoked" {
			t.Fatalf("%s stream after revoke = %+v (open %v), want access_revoked", name, ev, ok)
		}
		if _, ok := nextSSE(t, stream, 3*time.Second); ok {
			t.Fatalf("%s stream stayed open after access_revoked", name)
		}
	}
	if status, _ := node.openSSE(t, ticket, "updates", ""); status != http.StatusForbidden {
		t.Fatalf("reconnect after revoke status = %d, want 403", status)
	}
}

func TestSSETicketStreamEndsAtTicketExpiry(t *testing.T) {
	node := startPGConnectionsNode(t)
	ticket := node.ticket(t, "user-1", 1, "updates")
	status, events := node.openSSE(t, ticket, "updates", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if ev, _ := nextSSE(t, events, 3*time.Second); ev.Event != "subscribed" {
		t.Fatalf("first event = %+v", ev)
	}
	// Expiry ends the stream silently so the client reconnects with a new ticket.
	if ev, ok := nextSSE(t, events, 5*time.Second); ok {
		t.Fatalf("event after ticket expiry = %+v, want the stream to end", ev)
	}
	if status, _ := node.openSSE(t, ticket, "updates", ""); status != http.StatusUnauthorized {
		t.Fatalf("expired ticket status = %d, want 401", status)
	}
}

func TestSSEOIDCStreamUsesChannelAuthorization(t *testing.T) {
	node := startPGConnectionsNode(t)
	node.publish(t, "updates", "one")
	status, events := node.openSSE(t, "test-jwt", "updates", "")
	if status != http.StatusOK {
		t.Fatalf("OIDC SSE status = %d", status)
	}
	if ev, _ := nextSSE(t, events, 3*time.Second); ev.Event != "subscribed" {
		t.Fatalf("first event = %+v", ev)
	}
	expectSSEMessage(t, events, "1", "b25l")
	node.hooks.mu.Lock()
	authorized := len(node.hooks.events) > 0 && node.hooks.events[0].Type == EventAuthorizeChannel &&
		node.hooks.events[0].Principal == "user-123" && node.hooks.events[0].Channel == "updates" && node.hooks.events[0].Permission == "read"
	node.hooks.allowed = false
	node.hooks.mu.Unlock()
	if !authorized {
		t.Fatalf("channel callback = %+v, want a read authorization for user-123 on updates", node.hooks.events)
	}
	if status, _ := node.openSSE(t, "test-jwt", "updates", ""); status != http.StatusForbidden {
		t.Fatalf("SSE after the app denies the channel status = %d, want 403", status)
	}
}

func dialTicketWebSocket(t *testing.T, node *pgConnectionsNode, ticket string) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{ResumeSubprotocol}}
	target := "ws" + strings.TrimPrefix(node.server.URL, "http") + ManagedPathPrefix + node.endpoint
	client, response, err := dialer.Dial(target, http.Header{"Authorization": {"Bearer " + ticket}})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestWebSocketTicketIsReadOnlyAndClosesOnSessionRevoke(t *testing.T) {
	node := startPGConnectionsNode(t)
	node.publish(t, "updates", "one")
	ticket := node.ticket(t, "user-1", 60, "updates")
	client := dialTicketWebSocket(t, node, ticket)

	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	if frame := readResumeTestFrame(t, client); frame.Type != "subscribed" || frame.Channel != "updates" || frame.LatestSequence != 1 {
		t.Fatalf("subscribe = %+v", frame)
	}
	if frame := readResumeTestFrame(t, client); frame.Type != "message" || frame.Sequence != 1 || frame.DataBase64 != "b25l" {
		t.Fatalf("replay = %+v", frame)
	}
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "other"})
	if frame := readResumeTestFrame(t, client); frame.Type != "error" || frame.Code != "access_revoked" || frame.Channel != "other" {
		t.Fatalf("subscribe outside the ticket = %+v, want access_revoked", frame)
	}
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "publish", Channel: "updates"})
	if frame := readResumeTestFrame(t, client); frame.Type != "error" || frame.Code != "ticket_read_only" {
		t.Fatalf("publish on a ticket = %+v, want ticket_read_only", frame)
	}
	node.publish(t, "updates", "two")
	if frame := readResumeTestFrame(t, client); frame.Type != "message" || frame.Sequence != 2 || frame.DataBase64 != "dHdv" {
		t.Fatalf("live message = %+v", frame)
	}
	// subscribed and error frames are control frames, not usage.
	if got := node.delivered(t); got != 2 {
		t.Fatalf("delivered usage = %d, want the 2 message frames", got)
	}

	// Revoking the principal's sessions invalidates the ticket; the monitor
	// closes the socket on its next refresh.
	node.changeAccess(t, "user-1", "revoke_sessions", "")
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := client.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
				t.Fatalf("close after session revoke = %v, want policy violation", err)
			}
			break
		}
	}
	dialer := websocket.Dialer{Subprotocols: []string{ResumeSubprotocol}}
	target := "ws" + strings.TrimPrefix(node.server.URL, "http") + ManagedPathPrefix + node.endpoint
	_, response, err := dialer.Dial(target, http.Header{"Authorization": {"Bearer " + ticket}})
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("redial with a revoked ticket = %v, %+v, want 401", err, response)
	}
	_ = response.Body.Close()
}

func TestTicketRevocationClosesLiveConnections(t *testing.T) {
	node := startPGConnectionsNode(t)
	node.publish(t, "updates", "one")
	token, ticket, err := node.store.IssueRealtimeSubscriptionTicket(t.Context(), node.endpoint, "user-1", []string{"updates"}, 60)
	if err != nil {
		t.Fatal(err)
	}
	other := node.ticket(t, "user-1", 60, "updates")
	client := dialTicketWebSocket(t, node, token)
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	if frame := readResumeTestFrame(t, client); frame.Type != "subscribed" {
		t.Fatalf("subscribe = %+v", frame)
	}
	status, events := node.openSSE(t, token, "updates", "")
	if status != http.StatusOK {
		t.Fatalf("SSE status = %d", status)
	}
	if ev, _ := nextSSE(t, events, 3*time.Second); ev.Event != "subscribed" {
		t.Fatalf("SSE first event = %+v", ev)
	}

	if err := node.store.RevokeRealtimeSubscriptionTicket(t.Context(), node.endpoint, ticket.ID); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := client.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
				t.Fatalf("socket after revoke = %v, want a policy-violation close", err)
			}
			break
		}
	}
	deadline := time.After(5 * time.Second)
	for open := true; open; {
		select {
		case _, open = <-events:
		case <-deadline:
			t.Fatal("SSE stream stayed open after its ticket was revoked")
		}
	}
	if status, _ := node.openSSE(t, token, "updates", ""); status != http.StatusUnauthorized {
		t.Fatalf("reconnect with a revoked ticket = %d, want 401", status)
	}
	// Another ticket of the same principal is unaffected by a single revoke.
	if status, _ := node.openSSE(t, other, "updates", ""); status != http.StatusOK {
		t.Fatalf("sibling ticket status = %d, want 200", status)
	}
}
