package devbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// echoWebSocket is the local (or remote dependency) process: it records the
// handshake headers it saw and echoes every message with a prefix.
func echoWebSocket(t *testing.T, prefix string, seen chan<- http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/decline" {
			http.Error(w, "nope", http.StatusForbidden)
			return
		}
		if seen != nil {
			select {
			case seen <- r.Header.Clone():
			default:
			}
		}
		socket, err := (&websocket.Upgrader{Subprotocols: []string{"chat"}}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer socket.Close()
		for {
			kind, message, err := socket.ReadMessage()
			if err != nil {
				return
			}
			if err := socket.WriteMessage(kind, append([]byte(prefix), message...)); err != nil {
				return
			}
		}
	}))
}

type upgradeHarness struct {
	t          *testing.T
	relay      *Relay
	server     *httptest.Server
	session    Session
	creds      Credentials
	inspector  *Inspector
	mu         sync.Mutex
	revoked    bool
	dependency string
}

func newUpgradeHarness(t *testing.T, limits UpgradeLimits, local, dependency *httptest.Server) *upgradeHarness {
	t.Helper()
	h := &upgradeHarness{t: t, relay: NewRelay(4).WithUpgradeLimits(limits), inspector: NewInspector(10, 256), dependency: "dependency-app"}
	session, creds, err := NewSession(Scope{AccountID: "acct", DeveloperID: "dev", EnvironmentID: "env", TargetAppID: "app", DependencyAppIDs: []string{h.dependency}}, time.Now(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	h.session, h.creds = session, creds
	lookup := func(_ context.Context, account, id string) (Session, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if account != session.Scope.AccountID || id != session.ID {
			return Session{}, ErrUnauthorized
		}
		current := session
		if h.revoked {
			now := time.Now()
			current.RevokedAt = &now
		}
		return current, nil
	}
	var dependencies http.Handler
	if dependency != nil {
		target, _ := url.Parse(dependency.URL)
		dependencies = httputil.NewSingleHostReverseProxy(target)
	}
	h.server = httptest.NewServer(NewServer(h.relay, lookup, dependencies))
	t.Cleanup(h.server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	socket, response, err := websocket.DefaultDialer.DialContext(ctx, h.ws("/connect"), http.Header{AccountHeader: {"acct"}, TokenHeader: {creds.AttachmentToken}})
	if response != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	target, _ := url.Parse(local.URL)
	go func() { _ = ServeLocal(ctx, NewWebSocketConn(socket), target, 8, h.inspector) }()
	deadline := time.Now().Add(5 * time.Second)
	for !h.relay.Connected(session.ID) {
		if time.Now().After(deadline) {
			t.Fatal("laptop did not attach")
		}
		time.Sleep(time.Millisecond)
	}
	return h
}

func (h *upgradeHarness) ws(suffix string) string {
	return "ws" + strings.TrimPrefix(h.server.URL, "http") + "/v1/dev/bridges/" + h.session.ID + suffix
}

// dial returns the handshake status (0 when no response arrived) rather than
// the response itself, so its body is always closed here.
func (h *upgradeHarness) dial(suffix, token string) (*websocket.Conn, int, error) {
	header := http.Header{AccountHeader: {"acct"}, TokenHeader: {token}, SessionHeader: {h.session.ID}}
	socket, response, err := (&websocket.Dialer{Subprotocols: []string{"chat"}, HandshakeTimeout: 5 * time.Second}).Dial(h.ws(suffix), header)
	status := 0
	if response != nil {
		status = response.StatusCode
		if response.Body != nil {
			_ = response.Body.Close()
		}
	}
	return socket, status, err
}

func (h *upgradeHarness) revoke() {
	h.mu.Lock()
	h.revoked = true
	h.mu.Unlock()
}

func roundTripMessage(t *testing.T, socket *websocket.Conn, message, want string) {
	t.Helper()
	_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := socket.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
		t.Fatal(err)
	}
	_, got, err := socket.ReadMessage()
	if err != nil || string(got) != want {
		t.Fatalf("echo = %q, %v; want %q", got, err, want)
	}
}

func expectClosed(t *testing.T, socket *websocket.Conn, within time.Duration) {
	t.Helper()
	_ = socket.SetReadDeadline(time.Now().Add(within))
	for {
		if _, _, err := socket.ReadMessage(); err != nil {
			if strings.Contains(err.Error(), "timeout") {
				t.Fatalf("socket still open after %s", within)
			}
			return
		}
	}
}

var testUpgradeLimits = UpgradeLimits{MaxConnections: 2, IdleTimeout: time.Minute, MaxBytes: 1 << 20}

func TestWebSocketTrafficReachesLocalProcessWithoutBridgeAuthority(t *testing.T) {
	seen := make(chan http.Header, 1)
	local := echoWebSocket(t, "local:", seen)
	defer local.Close()
	h := newUpgradeHarness(t, testUpgradeLimits, local, nil)
	socket, status, err := h.dial("/traffic/live?room=1", h.creds.RequestToken)
	if err != nil {
		t.Fatalf("dial: %v (status %d)", err, status)
	}
	if socket.Subprotocol() != "chat" {
		t.Fatalf("subprotocol = %q", socket.Subprotocol())
	}
	roundTripMessage(t, socket, "hello", "local:hello")
	roundTripMessage(t, socket, strings.Repeat("x", 100<<10), "local:"+strings.Repeat("x", 100<<10))
	headers := <-seen
	if headers.Get(TokenHeader) != "" || headers.Get(SessionHeader) != "" || headers.Get(AccountHeader) != "" || headers.Get(UpgradeHeader) != "" {
		t.Fatalf("bridge authority reached the local process: %v", headers)
	}
	if _, err := ParseRequestContext(headers.Get(ContextHeader)); err != nil {
		t.Fatal("local process did not receive request authority for propagation")
	}
	if h.relay.Upgrades(h.session.ID) != 1 {
		t.Fatalf("upgrades = %d", h.relay.Upgrades(h.session.ID))
	}
	_ = socket.Close()
	deadline := time.Now().Add(5 * time.Second)
	for h.relay.Upgrades(h.session.ID) != 0 || !h.inspector.Snapshot()[0].Complete {
		if time.Now().After(deadline) {
			t.Fatalf("upgrade slot or inspection record not released after close: upgrades=%d records=%+v", h.relay.Upgrades(h.session.ID), h.inspector.Snapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
	record := h.inspector.Snapshot()[0]
	if record.Upgrade != "websocket" || record.Status != http.StatusSwitchingProtocols || record.RequestBytes == 0 || record.ResponseBytes == 0 || strings.Contains(record.Path, "room") {
		t.Fatalf("inspection record = %+v", record)
	}
}

func TestWebSocketTrafficRequiresRequestAuthority(t *testing.T) {
	local := echoWebSocket(t, "local:", nil)
	defer local.Close()
	h := newUpgradeHarness(t, testUpgradeLimits, local, nil)
	if _, status, err := h.dial("/traffic/live", h.creds.AttachmentToken); err == nil || status != http.StatusForbidden {
		t.Fatalf("attachment token opened scoped traffic: %v %v", err, status)
	}
}

func TestWebSocketTrafficEnforcesConnectionLimit(t *testing.T) {
	local := echoWebSocket(t, "local:", nil)
	defer local.Close()
	h := newUpgradeHarness(t, UpgradeLimits{MaxConnections: 1, IdleTimeout: time.Minute, MaxBytes: 1 << 20}, local, nil)
	first, _, err := h.dial("/traffic/live", h.creds.RequestToken)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	roundTripMessage(t, first, "a", "local:a")
	_, status, err := h.dial("/traffic/live", h.creds.RequestToken)
	if err == nil || status != http.StatusTooManyRequests {
		t.Fatalf("second upgrade admitted: %v (status %d)", err, status)
	}
}

func TestWebSocketTrafficIdleAndByteBounds(t *testing.T) {
	local := echoWebSocket(t, "local:", nil)
	defer local.Close()
	idle := newUpgradeHarness(t, UpgradeLimits{MaxConnections: 2, IdleTimeout: 200 * time.Millisecond, MaxBytes: 1 << 20}, local, nil)
	socket, _, err := idle.dial("/traffic/live", idle.creds.RequestToken)
	if err != nil {
		t.Fatal(err)
	}
	roundTripMessage(t, socket, "a", "local:a")
	expectClosed(t, socket, 3*time.Second)

	capped := newUpgradeHarness(t, UpgradeLimits{MaxConnections: 2, IdleTimeout: time.Minute, MaxBytes: 1024}, local, nil)
	socket, _, err = capped.dial("/traffic/live", capped.creds.RequestToken)
	if err != nil {
		t.Fatal(err)
	}
	_ = socket.WriteMessage(websocket.BinaryMessage, make([]byte, 4096))
	expectClosed(t, socket, 3*time.Second)
}

func TestWebSocketTrafficClosesOnRevocationAndSessionClose(t *testing.T) {
	local := echoWebSocket(t, "local:", nil)
	defer local.Close()
	h := newUpgradeHarness(t, testUpgradeLimits, local, nil)
	socket, _, err := h.dial("/traffic/live", h.creds.RequestToken)
	if err != nil {
		t.Fatal(err)
	}
	roundTripMessage(t, socket, "a", "local:a")
	h.revoke()
	expectClosed(t, socket, 5*time.Second)

	fenced := newUpgradeHarness(t, testUpgradeLimits, local, nil)
	socket, _, err = fenced.dial("/traffic/live", fenced.creds.RequestToken)
	if err != nil {
		t.Fatal(err)
	}
	roundTripMessage(t, socket, "a", "local:a")
	fenced.relay.CloseSession(fenced.session.ID)
	expectClosed(t, socket, 3*time.Second)
}

func TestWebSocketTrafficRefusals(t *testing.T) {
	local := echoWebSocket(t, "local:", nil)
	defer local.Close()
	h := newUpgradeHarness(t, testUpgradeLimits, local, nil)
	if _, status, err := h.dial("/traffic/decline", h.creds.RequestToken); err == nil || status != http.StatusForbidden {
		t.Fatalf("declined handshake: %v %v", err, status)
	}
	request, _ := http.NewRequest(http.MethodGet, h.server.URL+"/v1/dev/bridges/"+h.session.ID+"/traffic/live", nil)
	request.Header.Set(AccountHeader, "acct")
	request.Header.Set(TokenHeader, h.creds.RequestToken)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "h2c")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotImplemented {
		t.Fatalf("non-WebSocket upgrade status = %d", response.StatusCode)
	}

	disabled := newUpgradeHarness(t, UpgradeLimits{}, local, nil)
	if _, status, err := disabled.dial("/traffic/live", disabled.creds.RequestToken); err == nil || status != http.StatusNotImplemented {
		t.Fatalf("relay without upgrade limits forwarded a WebSocket: %v (status %d)", err, status)
	}
}

func TestWebSocketDependencyIsBoundedAndShareSessionBudget(t *testing.T) {
	local := echoWebSocket(t, "local:", nil)
	defer local.Close()
	seen := make(chan http.Header, 1)
	remote := echoWebSocket(t, "remote:", seen)
	defer remote.Close()
	h := newUpgradeHarness(t, UpgradeLimits{MaxConnections: 2, IdleTimeout: time.Minute, MaxBytes: 1 << 20}, local, remote)
	dependency, _, err := h.dial("/dependencies/"+h.dependency+"/feed", h.creds.AttachmentToken)
	if err != nil {
		t.Fatal(err)
	}
	roundTripMessage(t, dependency, "hi", "remote:hi")
	if headers := <-seen; headers.Get(TokenHeader) != h.creds.AttachmentToken || headers.Get("X-Gregale-Dev-Dependency") != h.dependency {
		t.Fatalf("dependency route identity missing: %v", headers)
	}
	if _, status, err := h.dial("/dependencies/other-app/feed", h.creds.AttachmentToken); err == nil || status != http.StatusForbidden {
		t.Fatalf("undeclared dependency upgraded: %v %v", err, status)
	}
	traffic, _, err := h.dial("/traffic/live", h.creds.RequestToken)
	if err != nil {
		t.Fatal(err)
	}
	defer traffic.Close()
	if _, status, err := h.dial("/dependencies/"+h.dependency+"/feed", h.creds.AttachmentToken); err == nil || status != http.StatusTooManyRequests {
		t.Fatalf("dependency upgrade exceeded the shared session budget: %v %v", err, status)
	}
	h.revoke()
	expectClosed(t, dependency, 5*time.Second)
	expectClosed(t, traffic, 5*time.Second)
}

func TestInspectionTransportKeepsUpgradesWritable(t *testing.T) {
	remote := echoWebSocket(t, "remote:", nil)
	defer remote.Close()
	target, _ := url.Parse(remote.URL)
	inspector := NewInspector(4, 256)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = inspector.Transport(http.DefaultTransport)
	front := httptest.NewServer(proxy)
	defer front.Close()
	socket, response, err := (&websocket.Dialer{Subprotocols: []string{"chat"}}).Dial("ws"+strings.TrimPrefix(front.URL, "http")+"/feed", nil)
	if response != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	roundTripMessage(t, socket, "x", "remote:x")
	_ = socket.Close()
	deadline := time.Now().Add(5 * time.Second)
	for len(inspector.Snapshot()) == 0 || !inspector.Snapshot()[0].Complete {
		if time.Now().After(deadline) {
			t.Fatal("upgrade record never completed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if record := inspector.Snapshot()[0]; record.Upgrade != "websocket" || record.Status != http.StatusSwitchingProtocols || record.RequestBytes == 0 || record.ResponseBytes == 0 {
		t.Fatalf("record = %+v", record)
	}
}
