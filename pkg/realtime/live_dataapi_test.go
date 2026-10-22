package realtime

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
)

// policyJWT is claimsJWT that records the policy each token was checked with.
type policyJWT struct {
	claimsJWT
	mu       sync.Mutex
	policies []AuthPolicy
}

func (p *policyJWT) AuthorizeClaims(ctx context.Context, token string, policy AuthPolicy) (json.RawMessage, time.Time, error) {
	p.mu.Lock()
	p.policies = append(p.policies, policy)
	p.mu.Unlock()
	return p.claimsJWT.AuthorizeClaims(ctx, token, policy)
}

// refusingHooks refuses every connection the app is asked about.
type refusingHooks struct {
	NopHooks
	mu          sync.Mutex
	connects    int
	disconnects int
}

func (h *refusingHooks) Connect(context.Context, Event) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connects++
	return false, nil
}

func (h *refusingHooks) Disconnect(context.Context, Event) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.disconnects++
	return nil
}

func (h *refusingHooks) counts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.connects, h.disconnects
}

// adr: 20261016151947 — a Data API's live endpoint needs no callbacks and no
// OIDC settings of its own: live-only connections never ask the app, and a
// user's token is verified with the issuer, JWKS and audience the Data API
// reported, so live reads accept exactly the tokens REST reads do.
func TestDataAPILiveReadsUseTheDataAPIsTokenSettingsWithoutAppCallbacks(t *testing.T) {
	live := &fakeLive{got: make(chan struct{}, 4)}
	verifier := &policyJWT{}
	hooks := &refusingHooks{}
	m := NewManager(Config{JWTAuthorizer: verifier, ResumePreview: true, HistoryReader: &testResumeHistory{}, ResumePollInterval: 15 * time.Millisecond, Live: live}, hooks)
	t.Cleanup(func() { _ = m.Close() })
	auth := &api.DataAPIUserAuth{Issuer: "https://auth.shop.example", JWKSURL: "https://auth.shop.example/jwks", Audience: "shop"}
	shop := LiveRoute{KID: "0123456789abcdef", Database: "shop", URL: "wss://live.internal/shop/live", DataAPIRole: "shop_api",
		DataAPIAccess: &api.DataAPIAccessConfig{Version: 1, Schemas: []string{"api"}, Auth: auth}}
	unreported := LiveRoute{KID: "fedcba9876543210", Database: "legacy", URL: "wss://live.internal/legacy/live", DataAPIRole: "legacy_api"}
	if err := m.RegisterEndpoint(Endpoint{ID: "data", AppID: "app-1", AccountID: "acct-1", CallbackURL: "https://shop-api.example",
		ConnectPath: "/realtime/connect", DisconnectPath: "/realtime/disconnect", ClientAuth: AuthPolicy{Mode: AuthModeNone},
		LiveRoutes: []LiveRoute{shop, unreported}}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	t.Cleanup(server.Close)
	url := "ws" + strings.TrimPrefix(server.URL, "http") + ManagedPathPrefix + "data"

	if legacy, response, err := websocket.DefaultDialer.Dial(url, nil); err == nil {
		_ = legacy.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, _, err := legacy.ReadMessage(); err == nil {
			t.Fatal("the app refused a v1 connection, but it stayed open")
		}
		_ = legacy.Close()
	} else if response != nil {
		_ = response.Body.Close()
	}
	time.Sleep(100 * time.Millisecond)
	connects, disconnects := hooks.counts()
	if connects != 1 {
		t.Fatalf("a v1 connection asks the app: %d connect callbacks", connects)
	}

	client, response, err := (&websocket.Dialer{Subprotocols: []string{ResumeSubprotocol}}).Dial(url, nil)
	if err != nil {
		t.Fatalf("live-only connection: %v", err)
	}
	_ = response.Body.Close()
	if err := client.WriteMessage(1, []byte(`{"type":"live.subscribe","subscription":"mine","database":"shop","rest":"orders?select=id","token":"user:alice"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-live.got:
	case <-time.After(5 * time.Second):
		t.Fatal("the user's live read never reached the live session")
	}
	live.mu.Lock()
	forwarded := string(live.received[0])
	live.mu.Unlock()
	if strings.Contains(forwarded, "user:alice") || !strings.Contains(forwarded, `"claims":{"sub":"alice"`) {
		t.Fatalf("forwarded %s", forwarded)
	}
	verifier.mu.Lock()
	policy := verifier.policies[0]
	verifier.mu.Unlock()
	if policy.Mode != AuthModeOIDCJWT || policy.Issuer != auth.Issuer || policy.JWKSURL != auth.JWKSURL ||
		strings.Join(policy.Audience, ",") != "shop" || strings.Join(policy.Algorithms, ",") != "RS256,ES256" {
		t.Fatalf("token checked with %+v", policy)
	}

	// A Data API that has not reported its settings cannot verify a token here.
	if err := client.WriteMessage(1, []byte(`{"type":"live.subscribe","subscription":"old","database":"legacy","rest":"orders?select=id","token":"user:alice"}`)); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := client.ReadMessage()
	if err != nil || !strings.Contains(string(data), `"code":"forbidden"`) || !strings.Contains(string(data), "usage reporting") {
		t.Fatalf("unreported settings: %s %v", data, err)
	}
	_ = client.Close()
	time.Sleep(100 * time.Millisecond)
	if c, d := hooks.counts(); c != connects || d != disconnects {
		t.Fatalf("a live-only connection asked the app: %d connects, %d disconnects", c-connects, d-disconnects)
	}
}
