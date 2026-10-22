package realtime

// adr: 1385

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func expireChannelGrant(a *channelAuthorization) {
	a.mu.Lock()
	a.checkedAt = time.Now().Add(-channelReauthorizeInterval)
	a.mu.Unlock()
}

func TestChannelAuthorizationCoalescesRefreshAndInvalidatesPolicy(t *testing.T) {
	hooks := &testChannelHooks{allowed: true}
	m := newSyntheticResumeTestManager(t, &testResumeHistory{}, hooks, nil)
	c := addSyntheticResumeTestConnection(t, m)
	var grant channelAuthorization
	if got := grant.check(t.Context(), c, "updates"); got != channelAuthorizationAllowed {
		t.Fatalf("initial authorization = %v", got)
	}
	expireChannelGrant(&grant)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if got := grant.check(t.Context(), c, "updates"); got != channelAuthorizationAllowed {
				t.Errorf("concurrent refresh = %v", got)
			}
		})
	}
	wg.Wait()
	hooks.mu.Lock()
	calls := len(hooks.events)
	hooks.mu.Unlock()
	if calls != 2 {
		t.Fatalf("authorization calls = %d, want one admission and one shared refresh", calls)
	}
	endpoint := *c.state.config.Load()
	endpoint.CallbackURL, endpoint.CallbackAuthToken = "https://new.example", "rotated-callback-token"
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	if got := grant.check(t.Context(), c, "updates"); got != channelAuthorizationAllowed {
		t.Fatalf("changed endpoint authorization = %v", got)
	}
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if len(hooks.events) != 3 || hooks.events[2].CallbackURL != endpoint.CallbackURL || hooks.events[2].CallbackAuthToken != endpoint.CallbackAuthToken {
		t.Fatal("endpoint change did not refresh using the new callback policy")
	}
}

type callbackChannelHooks struct {
	NopHooks
	authorize func(context.Context, Event) (bool, error)
}

func (h callbackChannelHooks) AuthorizeChannel(ctx context.Context, event Event) (bool, error) {
	return h.authorize(ctx, event)
}

func TestChannelAuthorizationRejectsLateAndCanceledGrants(t *testing.T) {
	hooks := &testChannelHooks{allowed: true}
	m := newSyntheticResumeTestManager(t, &testResumeHistory{}, hooks, nil)
	c := addSyntheticResumeTestConnection(t, m)
	var grant channelAuthorization
	entered, release := make(chan Event, 2), make(chan struct{})
	m.hooks = callbackChannelHooks{authorize: func(ctx context.Context, event Event) (bool, error) {
		entered <- event
		select {
		case <-release:
			return true, nil
		case <-ctx.Done():
			return true, nil // A misbehaving hook cannot authorize after its deadline.
		}
	}}
	result := make(chan channelVerdict, 1)
	go func() { result <- grant.check(t.Context(), c, "updates") }()
	<-entered
	endpoint := *c.state.config.Load()
	endpoint.CallbackAuthToken = "new-token"
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	close(release)
	if got := <-result; got != channelAuthorizationUnavailable {
		t.Fatalf("obsolete in-flight grant = %v, want unavailable", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := grant.check(ctx, c, "updates"); got != channelAuthorizationUnavailable {
		t.Fatalf("canceled grant = %v, want unavailable", got)
	}
	m.cfg.CallbackTimeout = time.Millisecond
	m.hooks = callbackChannelHooks{authorize: func(ctx context.Context, _ Event) (bool, error) {
		<-ctx.Done()
		return true, nil
	}}
	if got := grant.check(t.Context(), c, "updates"); got != channelAuthorizationUnavailable {
		t.Fatalf("late grant = %v, want unavailable", got)
	}
}

func expireLiveChannelGrant(t *testing.T, m *Manager) {
	t.Helper()
	connections := m.Snapshot()
	if len(connections) != 1 {
		t.Fatalf("connections = %d, want one", len(connections))
	}
	c, ok := m.connection(connections[0].ID)
	if !ok {
		t.Fatal("connection disappeared")
	}
	c.mu.RLock()
	sub := c.resumeSubs["updates"]
	c.mu.RUnlock()
	if sub == nil {
		t.Fatal("subscription disappeared")
	}
	expireChannelGrant(&sub.authorization)
}

func TestResumeIdleChannelRevocationRemovesPresenceAndRoute(t *testing.T) {
	hooks := &testChannelHooks{allowed: true}
	reporter := &testChannelRouteReporter{}
	m, server := newResumeTestManagerWithReporter(t, &testResumeHistory{}, hooks, reporter)
	client := dialResumeTest(t, server)
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	if got := readResumeTestFrame(t, client); got.Type != "subscribed" {
		t.Fatalf("subscription response = %+v", got)
	}
	readResumeTestFrame(t, client) // Initial presence snapshot.
	hooks.mu.Lock()
	hooks.allowed = false
	hooks.mu.Unlock()
	expireLiveChannelGrant(t, m)
	if got := readResumeTestFrame(t, client); got.Type != "error" || got.Code != "access_revoked" {
		t.Fatalf("revocation response = %+v", got)
	}
	waitForResumeTestWorkers(t, m)
	if routes := m.ChannelRouteSnapshot(); len(routes) != 0 {
		t.Fatalf("revoked channel still routed: %+v", routes)
	}
	m.mu.RLock()
	presence := len(m.presenceMembers[channelKey{endpointID: "resume-endpoint", channel: "updates"}])
	m.mu.RUnlock()
	if presence != 0 {
		t.Fatal("revoked channel retained its presence")
	}
	reports := reporter.snapshot()
	if len(reports) != 2 || reports[1].subscribed {
		t.Fatalf("route reports = %+v, want add then remove", reports)
	}
}

func TestResumeAuthorizationOutageClosesForRetry(t *testing.T) {
	hooks := &testChannelHooks{allowed: true}
	m, server := newResumeTestManager(t, &testResumeHistory{}, hooks)
	client := dialResumeTest(t, server)
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	readResumeTestFrame(t, client)
	readResumeTestFrame(t, client)
	hooks.mu.Lock()
	hooks.err = errors.New("authorization unavailable")
	hooks.mu.Unlock()
	expireLiveChannelGrant(t, m)
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := client.ReadMessage()
	var closed *websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != websocket.CloseTryAgainLater {
		t.Fatalf("outage response = %v, want retryable close", err)
	}
}

func TestResumeChannelRevocationPreservesOtherSubscribers(t *testing.T) {
	history := &testResumeHistory{}
	hooks := &testChannelHooks{allowed: true}
	m, server := newResumeTestManager(t, history, hooks)
	first := dialResumeTest(t, server)
	sendResumeTestFrame(t, first, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	readResumeTestFrame(t, first)
	readResumeTestFrame(t, first)
	firstConnection, ok := m.connection(m.Snapshot()[0].ID)
	if !ok {
		t.Fatal("first connection disappeared")
	}
	second := dialResumeTest(t, server)
	sendResumeTestFrame(t, second, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	readResumeTestFrame(t, second)
	readResumeTestFrame(t, second)
	readResumeTestFrame(t, first) // Second subscriber joined.
	hooks.mu.Lock()
	hooks.allowed = false
	hooks.mu.Unlock()
	firstConnection.mu.RLock()
	sub := firstConnection.resumeSubs["updates"]
	firstConnection.mu.RUnlock()
	expireChannelGrant(&sub.authorization)
	if got := readResumeTestFrame(t, first); got.Type != "error" || got.Code != "access_revoked" {
		t.Fatalf("revocation response = %+v", got)
	}
	if got := readResumeTestFrame(t, second); got.Type != "presence" || got.Event != "left" {
		t.Fatalf("remaining subscriber missed departure: %+v", got)
	}
	if routes := m.ChannelRouteSnapshot(); len(routes) != 1 {
		t.Fatalf("remaining subscription lost its route: %+v", routes)
	}
	history.appendMessage(1, "still authorized")
	if got := readResumeTestFrame(t, second); got.Type != "message" || got.Sequence != 1 {
		t.Fatalf("remaining subscriber lost delivery: %+v", got)
	}
}

func TestResumePresenceCommandCannotExtendExpiredGrant(t *testing.T) {
	hooks := &testChannelHooks{allowed: true}
	m, server := newResumeTestManager(t, &testResumeHistory{}, hooks)
	// Prevent idle polling from being the reason this command is rejected.
	m.cfg.ResumePollInterval = time.Minute
	client := dialResumeTest(t, server)
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	readResumeTestFrame(t, client)
	readResumeTestFrame(t, client)
	hooks.mu.Lock()
	hooks.allowed = false
	hooks.mu.Unlock()
	expireLiveChannelGrant(t, m)
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "presence", Channel: "updates"})
	if got := readResumeTestFrame(t, client); got.Type != "error" || got.Code != "access_revoked" {
		t.Fatalf("expired presence command = %+v", got)
	}
}

func TestHTTPChannelAuthorizationDistinguishesOutages(t *testing.T) {
	for _, status := range []int{200, 204, 401, 403, 400, 404, 408, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				if status != http.StatusNoContent {
					_, _ = w.Write([]byte("private upstream failure detail"))
				}
			}))
			defer server.Close()
			hooks := HTTPHooks{Client: server.Client(), MaxAttempts: 1}
			allowed, err := hooks.AuthorizeChannel(t.Context(), Event{Type: EventAuthorizeChannel, Principal: "user", Channel: "updates", CallbackURL: server.URL})
			wantAllowed := status >= 200 && status < 300
			wantError := !wantAllowed && status != 401 && status != 403
			if allowed != wantAllowed || (err != nil) != wantError {
				t.Fatalf("HTTP %d: allowed=%v err=%v", status, allowed, err)
			}
			if err != nil && strings.Contains(err.Error(), "private upstream") {
				t.Fatal("authorization error leaked upstream details")
			}
		})
	}
}

func channelTestSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	peers, done := make(chan *websocket.Conn, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		peers <- ws
		<-done
	}))
	client, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		close(done)
		server.Close()
		t.Fatal(err)
	}
	peer := <-peers
	t.Cleanup(func() { close(done); _ = client.Close(); _ = peer.Close(); server.Close() })
	return client, peer
}

func TestResumeBufferedChannelFramesRequireFreshAuthorization(t *testing.T) {
	for _, kind := range []string{"subscribed", "message", "checkpoint", "presence", "signal", "read_receipt"} {
		t.Run(kind, func(t *testing.T) {
			hooks := &testChannelHooks{allowed: true}
			m := newSyntheticResumeTestManager(t, &testResumeHistory{}, hooks, nil)
			c := addSyntheticResumeTestConnection(t, m)
			sub := &resumeSubscription{cancel: func() {}}
			if got := sub.authorization.check(t.Context(), c, "updates"); got != channelAuthorizationAllowed {
				t.Fatalf("admission = %v", got)
			}
			c.resumeSubs["updates"], c.channels["updates"] = sub, struct{}{}
			if err := m.queueResume(t.Context(), c, resumeServerFrame{Type: kind, Channel: "updates", DataBase64: "cHJpdmF0ZQ=="}); err != nil {
				t.Fatal(err)
			}
			hooks.mu.Lock()
			hooks.allowed = false
			hooks.mu.Unlock()
			expireChannelGrant(&sub.authorization)
			client, peer := channelTestSocketPair(t)
			c.ws = peer
			stopped := make(chan struct{})
			go func() { defer close(stopped); c.writePump() }()
			t.Cleanup(func() { c.closeOutboundQueue(); <-stopped })
			if got := readResumeTestFrame(t, client); got.Type != "error" || got.Code != "access_revoked" {
				t.Fatalf("buffered %s escaped revocation: %+v", kind, got)
			}
		})
	}
}
