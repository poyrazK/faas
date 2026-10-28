package mcpmanagement

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func onlyGrant(t *testing.T, r *remoteServer) *oauthGrant {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.grants) != 1 {
		t.Fatalf("grants = %d, want 1", len(r.grants))
	}
	for _, g := range r.grants {
		return g
	}
	return nil
}

// ageAndPrune makes g look idle past idleServerTTL and runs the prune every
// request runs, reporting whether g kept its server.
func ageAndPrune(r *remoteServer, g *oauthGrant) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	g.lastUsed = time.Now().Add(-idleServerTTL - time.Second)
	r.pruneLocked()
	return g.server != nil
}

// A restart restores connections without building their servers; the first
// request builds one, an idle one is dropped, and the next request rebuilds it.
func TestConnectionServersAreBuiltOnUseAndDroppedWhenIdle(t *testing.T) {
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
	g := onlyGrant(t, service.remote)
	if !g.approved || g.server != nil {
		t.Fatalf("restored grant approved=%v built=%v, want approved and unbuilt", g.approved, g.server != nil)
	}

	cs, ctx := f.connect(tokens, "2026-07-28")
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	service.remote.mu.Lock()
	built, inflight := g.server != nil, g.inflight
	service.remote.mu.Unlock()
	if !built || inflight != 0 {
		t.Fatalf("after a request built=%v inflight=%d", built, inflight)
	}

	if ageAndPrune(service.remote, g) {
		t.Fatal("an idle server was kept")
	}
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatalf("a dropped server was not rebuilt: %v", err)
	}
	if !g.approved {
		t.Fatal("dropping the server ended the connection")
	}
}

// A server is kept while a request uses it or it holds a secret request,
// which lives only in memory.
func TestConnectionServersKeepWhatARebuildWouldLose(t *testing.T) {
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
	r := service.remote
	g := onlyGrant(t, r)

	r.mu.Lock()
	if _, err := r.serverLocked(g); err != nil {
		r.mu.Unlock()
		t.Fatal(err)
	}
	g.inflight = 1
	r.mu.Unlock()
	if !ageAndPrune(r, g) {
		t.Fatal("a server serving a request was dropped")
	}

	r.mu.Lock()
	g.inflight = 0
	g.management.secretRequests["pending"] = &secretRequest{ID: "pending", expiresAt: time.Now().Add(time.Minute)}
	r.mu.Unlock()
	if !ageAndPrune(r, g) {
		t.Fatal("a server holding a pending secret request was dropped")
	}

	r.mu.Lock()
	g.management.secretRequests["pending"].expiresAt = time.Now().Add(-time.Second)
	r.mu.Unlock()
	if ageAndPrune(r, g) {
		t.Fatal("an expired secret request kept the server")
	}
}

func scrape(t *testing.T, telemetry *Telemetry) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	telemetry.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body, err := io.ReadAll(recorder.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestTelemetryReportsConnectionsOnceABridgeAttaches(t *testing.T) {
	if strings.Contains(scrape(t, NewTelemetry()), "gregale_management_mcp_connections") {
		t.Fatal("telemetry without a bridge reported connections")
	}
	telemetry := NewTelemetry()
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", Telemetry: telemetry})
	code := f.authorizeAccount("fp_test_alice_remote_key", false)
	var tokens tokenResponse
	reply := f.exchange(code, testVerifier, f.resource, f.clientID)
	decodeResponse(t, reply, &tokens)
	_ = reply.Body.Close()
	body := scrape(t, telemetry)
	for _, want := range []string{
		"gregale_management_mcp_connections 1",
		"gregale_management_mcp_connection_servers 1",
		"gregale_management_mcp_oauth_entry_limit 256",
		`gregale_management_mcp_oauth_entries{kind="clients"} 1`,
		`gregale_management_mcp_oauth_entries{kind="grants"} 1`,
		"gregale_management_mcp_state_available 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q", want)
		}
	}
	if strings.Contains(body, "alice") || strings.Contains(body, f.clientID) {
		t.Fatal("metrics carry an account or client identity")
	}
}
