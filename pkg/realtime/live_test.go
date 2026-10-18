package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeLive records a connection's live frames and lets the test answer.
type fakeLive struct {
	mu       sync.Mutex
	received [][]byte
	routes   func() []LiveRoute
	send     func([]byte) error
	closed   bool
	got      chan struct{}
}

func (f *fakeLive) Attach(routes func() []LiveRoute, send func([]byte) error) LiveSession {
	f.mu.Lock()
	f.routes, f.send = routes, send
	f.mu.Unlock()
	return f
}

func (f *fakeLive) Receive(_ context.Context, frame []byte) {
	f.mu.Lock()
	f.received = append(f.received, append([]byte(nil), frame...))
	f.mu.Unlock()
	f.got <- struct{}{}
}

func (f *fakeLive) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

// testLiveRoute makes an endpoint offer the live protocol; an endpoint
// without live routes answers live frames like unknown ones.
var testLiveRoute = LiveRoute{KID: "0123456789abcdef", Database: "db", URL: "wss://live.internal/db/live"}

func newLiveTestManager(t *testing.T, live LiveQueries) *httptest.Server {
	t.Helper()
	m := NewManager(Config{
		JWTAuthorizer: &recordingJWTAuthorizer{}, ResumePreview: true,
		HistoryReader: &testResumeHistory{}, ResumePollInterval: 15 * time.Millisecond, Live: live,
	}, &testChannelHooks{})
	if err := m.RegisterEndpoint(Endpoint{
		ID: "resume-endpoint", AppID: "app-1", AccountID: "acct-1", CallbackURL: "https://app.example",
		AllowedOrigins: []string{"https://app.example"},
		ClientAuth: AuthPolicy{
			Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"},
		},
		LiveRoutes: []LiveRoute{testLiveRoute},
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { server.Close(); _ = m.Close() })
	return server
}

// adr: 20261016151947 — an endpoint's live routes are checked when it registers, and
// a live session reads them as they are now, so a re-registration reaches
// connections already open.
func TestLiveRoutesFollowRegistration(t *testing.T) {
	route := LiveRoute{KID: "0123456789abcdef", Database: "db_1", URL: "wss://live.internal/db_1/live"}
	for name, routes := range map[string][]LiveRoute{
		"bad key":     {{KID: "XYZ", Database: "db_1", URL: route.URL}},
		"repeated":    {route, route},
		"no database": {{KID: route.KID, URL: route.URL}},
		"http hub":    {{KID: route.KID, Database: "db_1", URL: "https://live.internal/live"}},
		"credentials": {{KID: route.KID, Database: "db_1", URL: "wss://u:p@live.internal/live"}},
	} {
		if _, err := validLiveRoutes(routes); err == nil {
			t.Errorf("%s: accepted %v", name, routes)
		}
	}

	live := &fakeLive{got: make(chan struct{}, 4)}
	m := NewManager(Config{
		JWTAuthorizer: &recordingJWTAuthorizer{}, ResumePreview: true,
		HistoryReader: &testResumeHistory{}, ResumePollInterval: 15 * time.Millisecond, Live: live,
	}, &testChannelHooks{})
	endpoint := Endpoint{
		ID: "resume-endpoint", AppID: "app-1", AccountID: "acct-1", CallbackURL: "https://app.example",
		AllowedOrigins: []string{"https://app.example"},
		ClientAuth: AuthPolicy{
			Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"},
		},
		LiveRoutes: []LiveRoute{testLiveRoute},
	}
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { server.Close(); _ = m.Close() })
	client := dialResumeTest(t, server)
	if err := client.WriteMessage(1, []byte(`{"type":"live.subscribe","subscription":"feed","sealed":"gls1.0123456789abcdef.x"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-live.got:
	case <-time.After(5 * time.Second):
		t.Fatal("the live session never received the frame")
	}
	live.mu.Lock()
	routes := live.routes
	live.mu.Unlock()
	if got := routes(); len(got) != 1 || got[0] != testLiveRoute {
		t.Fatalf("routes before re-registration = %v", got)
	}
	endpoint.LiveRoutes = []LiveRoute{route}
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	if got := routes(); len(got) != 1 || got[0] != route {
		t.Fatalf("routes after re-registration = %v", got)
	}
}

// adr: 20261016151947 — an endpoint without live routes (its account is outside the
// FAAS_LIVE_QUERIES_ACCOUNTS rollout) answers live frames exactly like an
// unknown frame type, and nothing reaches a live session.
func TestLiveProtocolHiddenWithoutRoutes(t *testing.T) {
	live := &fakeLive{got: make(chan struct{}, 4)}
	m := NewManager(Config{
		JWTAuthorizer: &claimsJWT{}, ResumePreview: true,
		HistoryReader: &testResumeHistory{}, ResumePollInterval: 15 * time.Millisecond, Live: live,
	}, &testChannelHooks{})
	if err := m.RegisterEndpoint(Endpoint{
		ID: "resume-endpoint", AppID: "app-1", AccountID: "acct-1", CallbackURL: "https://app.example",
		AllowedOrigins: []string{"https://app.example"},
		ClientAuth: AuthPolicy{
			Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { server.Close(); _ = m.Close() })
	client := dialResumeTest(t, server)
	answer := func(frame string) string {
		t.Helper()
		if err := client.WriteMessage(1, []byte(frame)); err != nil {
			t.Fatal(err)
		}
		_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, data, err := client.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	unknown := answer(`{"type":"bogus.frame","subscription":"feed"}`)
	for _, frame := range []string{
		`{"type":"live.subscribe","subscription":"feed","sealed":"gls1.0123456789abcdef.x"}`,
		`{"type":"live.subscribe","subscription":"feed","database":"db","rest":"messages?select=id","token":"user:alice"}`,
		`{"type":"live.unsubscribe","subscription":"feed"}`,
	} {
		if got := answer(frame); got != unknown {
			t.Fatalf("%s answered %s, an unknown frame %s", frame, got, unknown)
		}
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	if len(live.received) != 0 {
		t.Fatalf("a live session received %d frames", len(live.received))
	}
}

// claimsJWT verifies tokens of the form "user:<sub>".
type claimsJWT struct{ recordingJWTAuthorizer }

func (claimsJWT) AuthorizeClaims(_ context.Context, token string, _ AuthPolicy) (json.RawMessage, time.Time, error) {
	sub, ok := strings.CutPrefix(token, "user:")
	if !ok {
		return nil, time.Time{}, errors.New("bad token")
	}
	return json.RawMessage(`{"sub":"` + sub + `","role":"authenticated","exp":4102444800}`), time.Unix(4102444800, 0), nil
}

// adr: 20261016151947 — an RLS-mode frame reaches the live session with the token's
// verified claims in place of the token; a client may not send claims, and
// a token the endpoint cannot verify is refused.
func TestLiveTokensBecomeClaims(t *testing.T) {
	live := &fakeLive{got: make(chan struct{}, 4)}
	m := NewManager(Config{
		JWTAuthorizer: &claimsJWT{}, ResumePreview: true,
		HistoryReader: &testResumeHistory{}, ResumePollInterval: 15 * time.Millisecond, Live: live,
	}, &testChannelHooks{})
	if err := m.RegisterEndpoint(Endpoint{
		ID: "resume-endpoint", AppID: "app-1", AccountID: "acct-1", CallbackURL: "https://app.example",
		AllowedOrigins: []string{"https://app.example"},
		ClientAuth: AuthPolicy{
			Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"},
		},
		LiveRoutes: []LiveRoute{testLiveRoute},
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { server.Close(); _ = m.Close() })
	client := dialResumeTest(t, server)
	read := `"database":"db","rest":"messages?select=id"`
	if err := client.WriteMessage(1, []byte(`{"type":"live.subscribe","subscription":"feed",`+read+`,"token":"user:alice"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-live.got:
	case <-time.After(5 * time.Second):
		t.Fatal("the live session never received the frame")
	}
	live.mu.Lock()
	forwarded := string(live.received[0])
	live.mu.Unlock()
	if strings.Contains(forwarded, "token") || !strings.Contains(forwarded, `"claims":{"sub":"alice"`) || !strings.Contains(forwarded, `"claims_expires":4102444800`) {
		t.Fatalf("forwarded %s", forwarded)
	}
	for _, tc := range []struct{ frame, code string }{
		{`{"type":"live.subscribe","subscription":"forged",` + read + `,"claims":{"sub":"admin"}}`, "protocol_error"},
		{`{"type":"live.subscribe","subscription":"bad",` + read + `,"token":"nonsense"}`, "seal_rejected"},
		{`{"type":"live.subscribe","subscription":"named",` + read + `,"token":"user:alice","role":"postgres"}`, "protocol_error"},
		{`{"type":"live.subscribe","subscription":"sealed-named","sealed":"gls1.0123456789abcdef.x","role":"postgres"}`, "protocol_error"},
	} {
		if err := client.WriteMessage(1, []byte(tc.frame)); err != nil {
			t.Fatal(err)
		}
		_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, data, err := client.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var refusal struct {
			Code   string
			Closed bool
		}
		if json.Unmarshal(data, &refusal) != nil || refusal.Code != tc.code || !refusal.Closed {
			t.Fatalf("%s: refusal %s", tc.frame, data)
		}
	}
	live.mu.Lock()
	n := len(live.received)
	live.mu.Unlock()
	if n != 1 {
		t.Fatalf("refused frames reached the live session (%d frames)", n)
	}
}

// adr: 20261016151947 — live frames count against the connection's outbound queue like
// any other traffic (ADR-1323): a client too slow for them is closed with
// 1013 so it reconnects, rather than growing realtimed's memory.
func TestLiveFramesUseTheOutboundBudget(t *testing.T) {
	live := &fakeLive{got: make(chan struct{}, 4)}
	m := NewManager(Config{
		JWTAuthorizer: &recordingJWTAuthorizer{}, ResumePreview: true,
		HistoryReader: &testResumeHistory{}, ResumePollInterval: 15 * time.Millisecond, Live: live,
		OutboundQueueBytes: 1024,
	}, &testChannelHooks{})
	if err := m.RegisterEndpoint(Endpoint{
		ID: "resume-endpoint", AppID: "app-1", AccountID: "acct-1", CallbackURL: "https://app.example",
		AllowedOrigins: []string{"https://app.example"},
		ClientAuth: AuthPolicy{
			Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"},
		},
		LiveRoutes: []LiveRoute{testLiveRoute},
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { server.Close(); _ = m.Close() })
	client := dialResumeTest(t, server)
	if err := client.WriteMessage(1, []byte(`{"type":"live.subscribe","subscription":"feed","sealed":"gls1.0123456789abcdef.x"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-live.got:
	case <-time.After(5 * time.Second):
		t.Fatal("the live session never received the frame")
	}
	live.mu.Lock()
	send := live.send
	live.mu.Unlock()
	big := `{"type":"live.rows","subscription":"feed","rows":["` + strings.Repeat("x", 2048) + `"]}`
	if err := send([]byte(big)); !errors.Is(err, ErrOutboundQueueFull) {
		t.Fatalf("an oversized live frame queued: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, _, err := client.ReadMessage()
		var closeErr *websocket.CloseError
		if errors.As(err, &closeErr) {
			if closeErr.Code != websocket.CloseTryAgainLater {
				t.Fatalf("closed with %d, want 1013", closeErr.Code)
			}
			return
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
	}
}

// adr: 20261016151947 — v2 connections carry live.* frames to the live-query proxy
// unchanged and queue its frames back; without one, a live subscription is
// refused, and closing the connection ends the live session.
func TestLiveFramesRideV2Connections(t *testing.T) {
	live := &fakeLive{got: make(chan struct{}, 4)}
	client := dialResumeTest(t, newLiveTestManager(t, live))
	subscribe := []byte(`{"type":"live.subscribe","subscription":"feed","sealed":"gls1.0123456789abcdef.x"}`)
	if err := client.WriteMessage(1, subscribe); err != nil {
		t.Fatal(err)
	}
	select {
	case <-live.got:
	case <-time.After(5 * time.Second):
		t.Fatal("the live session never received the frame")
	}
	live.mu.Lock()
	if string(live.received[0]) != string(subscribe) {
		t.Fatalf("received %s", live.received[0])
	}
	send := live.send
	live.mu.Unlock()
	rows := `{"type":"live.rows","subscription":"feed","database":"db","version":1,"snapshot":"1:1:","expires":1,"columns":[],"rows":[],"complete":true}`
	if err := send([]byte(rows)); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != rows {
		t.Fatalf("client received %s", data)
	}
	_ = client.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		live.mu.Lock()
		closed := live.closed
		live.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("closing the connection did not end the live session")
		}
		time.Sleep(10 * time.Millisecond)
	}

	plain := dialResumeTest(t, newLiveTestManager(t, nil))
	if err := plain.WriteMessage(1, subscribe); err != nil {
		t.Fatal(err)
	}
	_ = plain.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err = plain.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var refusal struct {
		Type, Subscription, Code string
		Closed                   bool
	}
	if err := json.Unmarshal(data, &refusal); err != nil || refusal.Type != "live.error" || refusal.Code != "unavailable" ||
		!refusal.Closed || refusal.Subscription != "feed" {
		t.Fatalf("refusal %s", data)
	}
}
