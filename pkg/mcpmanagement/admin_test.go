package mcpmanagement

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func adminRequest(t *testing.T, handler http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

func TestAdminListRevocationIsolationAndRestart(t *testing.T) {
	store, path, key := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", StateStore: store})
	aliceCode := f.authorizeAccount("fp_test_alice_remote_key", false)
	var alice tokenResponse
	reply := f.exchange(aliceCode, testVerifier, f.resource, f.clientID)
	decodeResponse(t, reply, &alice)
	_ = reply.Body.Close()
	if reply.StatusCode != http.StatusOK || alice.AccessToken == "" || alice.RefreshToken == "" {
		t.Fatal("alice token exchange did not return credentials")
	}
	bobCode := f.authorizeAccount("fp_test_bob_remote_key", false)
	var bob tokenResponse
	reply = f.exchange(bobCode, testVerifier, f.resource, f.clientID)
	decodeResponse(t, reply, &bob)
	_ = reply.Body.Close()
	if reply.StatusCode != http.StatusOK || bob.AccessToken == "" || bob.RefreshToken == "" {
		t.Fatal("bob token exchange did not return credentials")
	}
	service, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(service.Handler())
	// Both restored grants inherit the service, and only the selected one is revoked.
	listed := adminRequest(t, service.AdminHandler(), http.MethodGet, "/connections?limit=1")
	if listed.Code != 200 {
		t.Fatal(listed.Body.String())
	}
	var page struct {
		Items      []ConnectionSummary `json:"items"`
		Total      int                 `json:"total"`
		NextOffset int                 `json:"next_offset"`
	}
	if json.Unmarshal(listed.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Total != 2 || page.NextOffset != 1 {
		t.Fatal("connection pagination failed")
	}
	all := adminRequest(t, service.AdminHandler(), http.MethodGet, "/connections")
	if all.Code != http.StatusOK || json.Unmarshal(all.Body.Bytes(), &page) != nil {
		t.Fatal("invalid metadata")
	}
	id := ""
	for _, item := range page.Items {
		if item.AccountID == "alice" {
			id = item.GrantID
		}
	}
	if id == "" {
		t.Fatal("alice grant missing")
	}
	secrets := []string{alice.AccessToken, alice.RefreshToken, bob.AccessToken, bob.RefreshToken, "fp_test_alice_remote_key", "fp_test_bob_remote_key"}
	// Keep the raw-value check too: decoding duplicate JSON fields can hide
	// an earlier value, even though the current projection emits unique keys.
	for _, secret := range secrets {
		if strings.Contains(all.Body.String(), secret) {
			t.Fatal("admin metadata leaked credential data")
		}
	}
	var metadata any
	if json.Unmarshal(all.Body.Bytes(), &metadata) != nil {
		t.Fatal("invalid metadata")
	}
	if adminMetadataLeaksCredentials(metadata, secrets) {
		t.Fatal("admin metadata leaked credential data")
	}
	aliceClient, aliceCtx := f.connect(alice, "2026-07-28")
	bobClient, bobCtx := f.connect(bob, "2026-07-28")
	callTool(t, aliceCtx, aliceClient, "gregale_context", map[string]any{}, false)
	target := service.remote.grants[id]
	observing, finish, err := target.management.grantLifecycle.enter(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	// A request authenticated before revocation must also fail at the grant gate.
	response := adminRequest(t, service.AdminHandler(), http.MethodDelete, "/connections/"+id)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"durable":true`) {
		t.Fatalf("revoke=%d %s", response.Code, response.Body.String())
	}
	select {
	case <-observing.Done():
	case <-time.After(time.Second):
		t.Fatal("revocation did not cancel observations")
	}
	if _, err := aliceClient.CallTool(aliceCtx, &mcp.CallToolParams{Name: "gregale_context", Arguments: map[string]any{}}); err == nil {
		t.Fatal("revoked bearer remained usable")
	}
	callTool(t, bobCtx, bobClient, "gregale_context", map[string]any{}, false)
	if response := adminRequest(t, service.AdminHandler(), http.MethodDelete, "/connections/"+id); response.Code != 404 {
		t.Fatal("unknown/revoked grant accepted")
	}
	_ = aliceClient.Close()
	_ = bobClient.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	opts := f.opts
	opts.StateStore = restored
	restarted, err := NewHTTPService(opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(restarted.Handler())
	listing := adminRequest(t, restarted.AdminHandler(), http.MethodGet, "/connections")
	if json.Unmarshal(listing.Body.Bytes(), &page) != nil || page.Total != 1 || page.Items[0].AccountID != "bob" {
		t.Fatal("revoked grant resurrected after restart")
	}
	fresh, bctx := f.connect(bob, "2026-07-28")
	callTool(t, bctx, fresh, "gregale_context", map[string]any{}, false)
	public := f.request(http.MethodGet, "/connections", nil, "", bob.AccessToken)
	defer public.Body.Close()
	if public.StatusCode != 404 {
		t.Fatal("operator controls exposed on public mux")
	}
}

// Grant/client IDs are public base64url strings and can contain "Key".
// Check decoded field names and credential values, including escaped JSON,
// instead of rejecting that substring inside an otherwise valid identifier.
func adminMetadataLeaksCredentials(value any, secrets []string) bool {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			name := strings.ToLower(key)
			if strings.Contains(name, "key") || strings.Contains(name, "access_hash") || strings.Contains(name, "refresh_hash") || name == "access_token" || name == "refresh_token" {
				return true
			}
			if adminMetadataLeaksCredentials(key, secrets) || adminMetadataLeaksCredentials(child, secrets) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if adminMetadataLeaksCredentials(child, secrets) {
				return true
			}
		}
	case string:
		for _, secret := range secrets {
			if secret != "" && strings.Contains(node, secret) {
				return true
			}
		}
	}
	return false
}

func TestAdminMetadataCredentialGuard(t *testing.T) {
	publicIDs, err := json.Marshal(map[string]any{"items": []ConnectionSummary{{
		GrantID: "gmcp_grant_Key" + strings.Repeat("A", 40), ClientID: "gmcp_client_Key" + strings.Repeat("A", 40), AccountID: "alice",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		leaks      bool
	}{
		{"public IDs containing Key", string(publicIDs), false},
		{"native key field", `{"items":[{"Key":"redacted"}]}`, true},
		{"escaped key field", `{"items":[{"\u004bey":"redacted"}]}`, true},
		{"nested private key field", `{"items":[{"debug":{"privateKey":"redacted"}}]}`, true},
		{"nested access hash", `{"items":[{"debug":{"access_hash":"redacted"}}]}`, true},
		{"prefixed access hash", `{"items":[{"previous_access_hash":"redacted"}]}`, true},
		{"refresh hash", `{"items":[{"refresh_hash":"redacted"}]}`, true},
		{"API key field", `{"items":[{"api_key":"redacted"}]}`, true},
		{"empty access token field", `{"items":[{"access_token":""}]}`, true},
		{"refresh token field", `{"items":[{"refresh_token":"redacted"}]}`, true},
		{"credential in a value", `{"items":[{"note":"prefix fixture-access-token suffix"}]}`, true},
		{"escaped credential value", `{"items":[{"note":"fixture\u002daccess-token"}]}`, true},
		{"credential in a field name", `{"fixture-access-token":"redacted"}`, true},
		{"nested refresh credential", `{"items":[{"debug":["fixture-refresh-token"]}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var metadata any
			if err := json.Unmarshal([]byte(tc.body), &metadata); err != nil {
				t.Fatal(err)
			}
			if got := adminMetadataLeaksCredentials(metadata, []string{"fixture-access-token", "fixture-refresh-token"}); got != tc.leaks {
				t.Fatalf("credential leak detected = %v, want %v", got, tc.leaks)
			}
		})
	}
}

func TestAdminRevocationCancelsOnlyOneServer(t *testing.T) {
	first := NewLifecycle()
	second := NewLifecycle()
	a, releaseA, err := first.enter(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()
	b, releaseB, err := second.enter(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseB()
	first.cancelAll()
	if a.Err() == nil || b.Err() != nil {
		t.Fatal("grant cancellation crossed scopes")
	}
}

func TestAdminRejectsMalformedPagesAndUnavailableState(t *testing.T) {
	store, _, _ := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", StateStore: store})
	service, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/connections?limit=0", "/connections?limit=101", "/connections?offset=-1", "/connections?token=secret", "/connections?limit=1&limit=2"} {
		if response := adminRequest(t, service.AdminHandler(), "GET", path); response.Code != 400 {
			t.Fatal("invalid page accepted")
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if response := adminRequest(t, service.AdminHandler(), "GET", "/connections"); response.Code != 503 {
		t.Fatal("failed state disclosed stale grants")
	}
}

func TestAdminRevocationClosesLiveSubscriptionsAndPreauthenticatedGate(t *testing.T) {
	store, _, _ := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", StateStore: store})
	code := f.authorizeAccount("fp_test_alice_remote_key", false)
	var tokens tokenResponse
	reply := f.exchange(code, testVerifier, f.resource, f.clientID)
	decodeResponse(t, reply, &tokens)
	_ = reply.Body.Close()
	service, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(service.Handler())
	var grant *oauthGrant
	for _, g := range service.remote.grants {
		grant = g
	}
	if grant == nil {
		t.Fatal("grant missing")
	}
	// A stateless session authenticated before revocation must fail its private gate.
	service.remote.mu.Lock()
	server, err := service.remote.serverLocked(grant)
	service.remote.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	ack := make(chan struct{}, 1)
	client := mcp.NewClient(&mcp.Implementation{Name: "grant-revoke-test", Version: "1"}, nil)
	client.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "notifications/subscriptions/acknowledged" {
				ack <- struct{}{}
			}
			return next(ctx, method, req)
		}
	})
	cs, err := client.Connect(t.Context(), ct, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	if err := cs.Subscribe(t.Context(), &mcp.SubscribeParams{URI: "gregale://default/apps/alpha/health"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ack:
	case <-time.After(time.Second):
		t.Fatal("subscription not acknowledged")
	}
	if response := adminRequest(t, service.AdminHandler(), "DELETE", "/connections/"+grant.id); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	h := &subscriptionHarness{manager: grant.management, ctx: t.Context()}
	awaitResourceGroups(t, h, 0)
	awaitAdmissionEmpty(t, grant.management.admission)
	result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "gregale_context", Arguments: map[string]any{}})
	if err != nil || !result.IsError {
		t.Fatal("preauthenticated session escaped revocation")
	}
	data, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(data), "mcp_connection_revoked") {
		t.Fatalf("wrong revoked-connection diagnostic: %s", data)
	}
}

func TestAdminRevocationCancelsWatchWithoutCrossingGrant(t *testing.T) {
	root := NewLifecycle()
	f := progressFixture()
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(backend.Close)
	first := connectSubscriptionHarness(t, backend, Options{APIBase: backend.URL, Lifecycle: root}, "2026-07-28", true)
	second := connectSubscriptionHarness(t, backend, Options{APIBase: backend.URL, Lifecycle: root}, "2026-07-28", true)
	secondCtx, cancelSecond := context.WithCancel(second.ctx)
	defer cancelSecond()
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	go func() { defer close(firstDone); _, _ = first.client.CallTool(first.ctx, watchParams(nil, 5)) }()
	go func() { defer close(secondDone); _, _ = second.client.CallTool(secondCtx, watchParams(nil, 5)) }()
	deadline := time.Now().Add(time.Second)
	for len(first.manager.admission.watches) != 1 || len(second.manager.admission.watches) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("watches did not start")
		}
		time.Sleep(time.Millisecond)
	}
	grant := &oauthGrant{id: "gmcp_grant_target", approved: true, lastUsed: time.Now(), server: first.manager.resourceServer, management: first.manager}
	remote := &remoteServer{opts: HTTPOptions{Lifecycle: root}, grants: map[string]*oauthGrant{grant.id: grant}}
	service := &RemoteService{remote: remote}
	if reply := adminRequest(t, service.AdminHandler(), "DELETE", "/connections/"+grant.id); reply.Code != 200 {
		t.Fatal(reply.Body.String())
	}
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("revoked watch did not stop")
	}
	select {
	case <-secondDone:
		t.Fatal("another grant's watch stopped")
	case <-time.After(20 * time.Millisecond):
	}
	cancelSecond()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("watch cleanup failed")
	}
	awaitAdmissionEmpty(t, first.manager.admission)
	awaitAdmissionEmpty(t, second.manager.admission)
}

func TestAdminRevocationCommitFailureIsNotAcknowledged(t *testing.T) {
	store, _, _ := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", StateStore: store})
	code := f.authorizeAccount("fp_test_alice_remote_key", false)
	var tokens tokenResponse
	response := f.exchange(code, testVerifier, f.resource, f.clientID)
	decodeResponse(t, response, &tokens)
	_ = response.Body.Close()
	service, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for key := range service.remote.grants {
		id = key
	}
	// Force atomic journal replacement to fail without relying on UID permissions.
	store.mu.Lock()
	store.persist.(*fileState).path = filepath.Join(t.TempDir(), "missing-parent", "state.enc")
	store.mu.Unlock()
	reply := adminRequest(t, service.AdminHandler(), http.MethodDelete, "/connections/"+id)
	if reply.Code != http.StatusServiceUnavailable || strings.Contains(reply.Body.String(), `"revoked":true`) {
		t.Fatal("uncommitted revocation acknowledged")
	}
	if store.available() {
		t.Fatal("commit failure did not disable bridge storage")
	}
	if reply := adminRequest(t, service.AdminHandler(), http.MethodGet, "/connections"); reply.Code != http.StatusServiceUnavailable {
		t.Fatal("failed storage still exposed connections")
	}
}
