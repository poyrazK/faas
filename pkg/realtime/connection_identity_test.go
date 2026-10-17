package realtime

// adr: 1332

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const firstAuthSession = "d654628b-965b-4f84-965b-cc994ff0411b"
const secondAuthSession = "05af6001-a967-4d3f-8fbf-393e827ade46"

type identityTestAuthorizer struct {
	mu         sync.Mutex
	identities map[string]JWTIdentity
	err        error
	calls      int
}

func (a *identityTestAuthorizer) Authorize(ctx context.Context, token string, policy AuthPolicy) (string, error) {
	identity, err := a.AuthorizeIdentity(ctx, token, policy)
	return identity.Principal, err
}

func (a *identityTestAuthorizer) AuthorizeIdentity(_ context.Context, token string, _ AuthPolicy) (JWTIdentity, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return a.identities[token], a.err
}

func expireConnectionIdentity(a *connectionIdentity) {
	a.mu.Lock()
	a.checkedAt = time.Now().Add(-channelReauthorizeInterval)
	a.mu.Unlock()
}

func identityTestConnection(t *testing.T) (*connection, *identityTestAuthorizer) {
	t.Helper()
	m := newSyntheticResumeTestManager(t, &testResumeHistory{}, &testChannelHooks{allowed: true}, nil)
	c := addSyntheticResumeTestConnection(t, m)
	identity := JWTIdentity{Principal: c.info.Principal, SessionID: firstAuthSession, ExpiresAt: time.Now().Add(time.Minute)}
	authorizer := &identityTestAuthorizer{identities: map[string]JWTIdentity{"verified-token": identity}}
	m.cfg.JWTAuthorizer = authorizer
	c.identity = &connectionIdentity{token: "verified-token", identity: identity, endpoint: c.state.config.Load(), checkedAt: time.Now()}
	return c, authorizer
}

func TestConnectionIdentityCoalescesRefreshAndInvalidatesPolicy(t *testing.T) {
	c, authorizer := identityTestConnection(t)
	if got := c.identity.check(t.Context(), c); got != channelAuthorizationAllowed {
		t.Fatalf("cached identity = %v", got)
	}
	expireConnectionIdentity(c.identity)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if got := c.identity.check(t.Context(), c); got != channelAuthorizationAllowed {
				t.Errorf("concurrent identity refresh = %v", got)
			}
		})
	}
	wg.Wait()
	endpoint := *c.state.config.Load()
	endpoint.CallbackAuthToken = "rotated-policy"
	if err := c.m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	if got := c.identity.check(t.Context(), c); got != channelAuthorizationAllowed {
		t.Fatalf("new policy identity = %v", got)
	}
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	if authorizer.calls != 2 {
		t.Fatalf("identity refreshes = %d, want one coalesced refresh and one policy refresh", authorizer.calls)
	}
}

func TestConnectionIdentityPinsSessionSubjectAndExpiry(t *testing.T) {
	for _, name := range []string{"session", "subject", "expiry", "outage"} {
		t.Run(name, func(t *testing.T) {
			c, authorizer := identityTestConnection(t)
			expireConnectionIdentity(c.identity)
			want := channelAuthorizationDenied
			switch name {
			case "expiry":
				c.identity.identity.ExpiresAt = time.Now().Add(-time.Second)
			case "session", "subject":
				identity := authorizer.identities["verified-token"]
				if name == "session" {
					identity.SessionID = secondAuthSession
				} else {
					identity.Principal = "different-user"
				}
				authorizer.identities["verified-token"] = identity
			case "outage":
				authorizer.err = errors.New("JWKS unavailable")
				want = channelAuthorizationUnavailable
			}
			if got := c.identity.check(t.Context(), c); got != want {
				t.Fatalf("%s identity = %v, want %v", name, got, want)
			}
		})
	}
}

func TestConnectionIdentityRechecksCustomPrincipalProjection(t *testing.T) {
	c, _ := identityTestConnection(t)
	projected := "projected-user"
	endpoint := *c.state.config.Load()
	endpoint.Authorize = func(_ context.Context, request *http.Request) (string, error) {
		if request.Header.Get("X-Identity-Test") != "retained-request" {
			t.Error("custom projection did not receive the original request")
		}
		return projected, nil
	}
	if err := c.m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	c.info.Principal = projected
	c.identity.request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.identity.request.Header.Set("X-Identity-Test", "retained-request")
	if got := c.identity.check(t.Context(), c); got != channelAuthorizationAllowed {
		t.Fatalf("stable projection = %v", got)
	}
	projected = "different-projection"
	expireConnectionIdentity(c.identity)
	if got := c.identity.check(t.Context(), c); got != channelAuthorizationDenied {
		t.Fatalf("changed projection = %v", got)
	}
}

type identityAuthorizerFunc func(context.Context, string, AuthPolicy) (JWTIdentity, error)

func (f identityAuthorizerFunc) Authorize(ctx context.Context, token string, policy AuthPolicy) (string, error) {
	identity, err := f(ctx, token, policy)
	return identity.Principal, err
}

func (f identityAuthorizerFunc) AuthorizeIdentity(ctx context.Context, token string, policy AuthPolicy) (JWTIdentity, error) {
	return f(ctx, token, policy)
}

func TestConnectionIdentityRejectsLateAndObsoleteVerification(t *testing.T) {
	c, _ := identityTestConnection(t)
	expireConnectionIdentity(c.identity)
	entered, release := make(chan struct{}), make(chan struct{})
	c.m.cfg.JWTAuthorizer = identityAuthorizerFunc(func(context.Context, string, AuthPolicy) (JWTIdentity, error) {
		close(entered)
		<-release
		return c.identity.identity, nil
	})
	result := make(chan channelVerdict, 1)
	go func() { result <- c.identity.check(t.Context(), c) }()
	<-entered
	endpoint := *c.state.config.Load()
	endpoint.CallbackAuthToken = "replacement-policy"
	if err := c.m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	close(release)
	if got := <-result; got != channelAuthorizationUnavailable {
		t.Fatalf("obsolete verification = %v", got)
	}
	c.m.cfg.CallbackTimeout = time.Millisecond
	c.m.cfg.JWTAuthorizer = identityAuthorizerFunc(func(ctx context.Context, _ string, _ AuthPolicy) (JWTIdentity, error) {
		<-ctx.Done()
		return c.identity.identity, nil
	})
	if got := c.identity.check(t.Context(), c); got != channelAuthorizationUnavailable {
		t.Fatalf("late verification = %v", got)
	}
}

func dialIdentityTest(t *testing.T, server *httptest.Server, token string, browser bool) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{ResumeSubprotocol}}
	headers := http.Header{"Authorization": {"Bearer " + token}}
	if browser {
		headers = http.Header{"Origin": {"https://app.example"}}
		dialer.Subprotocols = append(dialer.Subprotocols, ResumeBearerSubprotocolPrefix+token)
	}
	client, response, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+ManagedPathPrefix+"resume-endpoint", headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestResumeLogoutIsBoundToVerifiedSession(t *testing.T) {
	history := &testResumeHistory{}
	m, server := newResumeTestManager(t, history, &testChannelHooks{allowed: true})
	identities := map[string]JWTIdentity{}
	for token, session := range map[string]string{"first.jwt.token": firstAuthSession, "second.jwt.token": secondAuthSession} {
		identities[token] = JWTIdentity{Principal: "same-user", SessionID: session, ExpiresAt: time.Now().Add(time.Minute)}
	}
	m.cfg.JWTAuthorizer = &identityTestAuthorizer{identities: identities}
	var mu sync.Mutex
	active := map[string]bool{firstAuthSession: true, secondAuthSession: true}
	var events []Event
	m.hooks = callbackChannelHooks{authorize: func(_ context.Context, event Event) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
		return active[event.SessionID], nil
	}}
	first := dialIdentityTest(t, server, "first.jwt.token", false)
	// An untrusted session_id in a client frame cannot replace the verified ID.
	if err := first.WriteJSON(map[string]any{"type": "subscribe", "channel": "updates", "session_id": secondAuthSession}); err != nil {
		t.Fatal(err)
	}
	readResumeTestFrame(t, first)
	readResumeTestFrame(t, first)
	firstConnection, ok := m.connection(m.Snapshot()[0].ID)
	if !ok {
		t.Fatal("first connection disappeared")
	}
	second := dialIdentityTest(t, server, "second.jwt.token", true)
	sendResumeTestFrame(t, second, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	readResumeTestFrame(t, second)
	readResumeTestFrame(t, second)
	readResumeTestFrame(t, first)
	mu.Lock()
	active[firstAuthSession] = false
	mu.Unlock()
	firstConnection.mu.RLock()
	sub := firstConnection.resumeSubs["updates"]
	firstConnection.mu.RUnlock()
	expireChannelGrant(&sub.authorization)
	if got := readResumeTestFrame(t, first); got.Code != "access_revoked" {
		t.Fatalf("logged-out session response = %+v", got)
	}
	if got := readResumeTestFrame(t, second); got.Type != "presence" || got.Event != "left" {
		t.Fatalf("remaining session departure = %+v", got)
	}
	history.appendMessage(1, "other device remains authorized")
	if got := readResumeTestFrame(t, second); got.Type != "message" || got.Sequence != 1 {
		t.Fatalf("remaining session lost delivery: %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) < 3 || events[0].SessionID != firstAuthSession || events[1].SessionID != secondAuthSession {
		t.Fatalf("callback session handoff = %+v", events)
	}
	wire, err := json.Marshal(events)
	if err != nil || strings.Contains(string(wire), "first.jwt.token") || strings.Contains(string(wire), "second.jwt.token") {
		t.Fatalf("callback exposed an access token: err=%v", err)
	}
}

func TestResumeTokenExpiryClosesIdleConnection(t *testing.T) {
	m, server := newResumeTestManager(t, &testResumeHistory{}, &testChannelHooks{allowed: true})
	m.cfg.JWTAuthorizer = &identityTestAuthorizer{identities: map[string]JWTIdentity{"expiring": {
		Principal: "user", SessionID: firstAuthSession, ExpiresAt: time.Now().Add(time.Second),
	}}}
	client := dialIdentityTest(t, server, "expiring", false)
	_ = client.SetReadDeadline(time.Now().Add(4 * time.Second))
	_, _, err := client.ReadMessage()
	if !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
		t.Fatalf("expired idle token = %v", err)
	}
}

func TestResumeBufferedFrameRequiresFreshIdentity(t *testing.T) {
	m, server := newResumeTestManager(t, &testResumeHistory{}, &testChannelHooks{allowed: true})
	authorizer := &identityTestAuthorizer{identities: map[string]JWTIdentity{"token": {
		Principal: "user", SessionID: firstAuthSession, ExpiresAt: time.Now().Add(time.Minute),
	}}}
	m.cfg.JWTAuthorizer = authorizer
	client := dialIdentityTest(t, server, "token", false)
	var c *connection
	deadline := time.Now().Add(time.Second)
	for c == nil && time.Now().Before(deadline) {
		if connections := m.Snapshot(); len(connections) == 1 {
			c, _ = m.connection(connections[0].ID)
		} else {
			time.Sleep(time.Millisecond)
		}
	}
	if c == nil {
		t.Fatal("connection did not register")
	}
	authorizer.mu.Lock()
	authorizer.err = errors.New("JWKS unavailable")
	authorizer.mu.Unlock()
	expireConnectionIdentity(c.identity)
	if err := m.queueResume(t.Context(), c, resumeServerFrame{Type: "message", Channel: "updates", DataBase64: "cHJpdmF0ZQ=="}); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := client.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseTryAgainLater) {
		t.Fatalf("unavailable identity allowed buffered frame: %v", err)
	}
}

func TestSSEAuthorizationUsesVerifiedSessionAndExpiry(t *testing.T) {
	f := newSSEAuthFixture(false)
	identity := JWTIdentity{Principal: "user-1", SessionID: firstAuthSession, ExpiresAt: time.Now().Add(time.Minute)}
	f.auth.identity = identity
	f.auth.m.cfg.JWTAuthorizer = &identityTestAuthorizer{identities: map[string]JWTIdentity{"token": identity}}
	var event Event
	f.auth.authorizer = callbackChannelHooks{authorize: func(_ context.Context, got Event) (bool, error) { event = got; return true, nil }}
	if got := f.auth.check(t.Context()); got != sseAllowed || event.SessionID != firstAuthSession {
		t.Fatalf("SSE session handoff: verdict=%v session=%q", got, event.SessionID)
	}
	authorizer := f.auth.m.cfg.JWTAuthorizer.(*identityTestAuthorizer)
	changed := identity
	changed.SessionID = secondAuthSession
	authorizer.identities["token"] = changed
	f.auth.authorizedAt = time.Now().Add(-channelReauthorizeInterval)
	if got := f.auth.check(t.Context()); got != sseDenied {
		t.Fatalf("SSE accepted a different session: %v", got)
	}
	f.auth.identity.ExpiresAt = time.Now().Add(-time.Second)
	if got := f.auth.check(t.Context()); got != sseDenied {
		t.Fatalf("SSE cached grant outlived token: %v", got)
	}
}
