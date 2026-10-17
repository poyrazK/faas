package realtime

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
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
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { server.Close(); _ = m.Close() })
	return server
}

// adr: 1384 — an endpoint's live routes are checked when it registers, and
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
	if got := routes(); len(got) != 0 {
		t.Fatalf("routes before binding = %v", got)
	}
	endpoint.LiveRoutes = []LiveRoute{route}
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	if got := routes(); len(got) != 1 || got[0] != route {
		t.Fatalf("routes after re-registration = %v", got)
	}
}

// adr: 1384 — v2 connections carry live.* frames to the live-query proxy
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
