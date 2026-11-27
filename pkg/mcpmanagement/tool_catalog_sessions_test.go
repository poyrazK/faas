package mcpmanagement

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// connectSessions opens n client sessions on one management server, as a
// hosted grant's server serves several connections.
func connectSessions(t *testing.T, backend *httptest.Server, opts Options, n int, tune func(*management)) ([]*mcp.ClientSession, context.Context) {
	t.Helper()
	c := api.NewClient(backend.URL, testToken)
	c.SetCompletionCache(nil)
	s, m, err := newManagementServer(c, opts)
	if err != nil {
		t.Fatal(err)
	}
	if tune != nil {
		tune(m)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	var sessions []*mcp.ClientSession
	for range n {
		ct, st := mcp.NewInMemoryTransports()
		ss, err := s.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ss.Close() })
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "catalog-test", Version: "1"}, nil).Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cs.Close() })
		sessions = append(sessions, cs)
	}
	return sessions, ctx
}

func listedNames(t *testing.T, ctx context.Context, cs *mcp.ClientSession) []string {
	t.Helper()
	catalog, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return toolNamesOf(catalog.Tools)
}

// A found tool joins only the finding session's listing; another session on
// the same server keeps its own catalog, and a known name is still callable.
func TestFoundToolsBelongToTheirSession(t *testing.T) {
	backend := httptest.NewServer(http.NotFoundHandler())
	defer backend.Close()
	sessions, ctx := connectSessions(t, backend, Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, Toolsets: []string{ToolsetAll}, MaxListedTools: 40}, 2, nil)
	alice, bob := sessions[0], sessions[1]
	callTool(t, ctx, alice, findToolsName, map[string]any{"query": "postgres schema", "limit": 1}, false)
	if !slices.Contains(listedNames(t, ctx, alice), "gregale_get_postgres_schema") {
		t.Fatal("finding session does not list its found tool")
	}
	if slices.Contains(listedNames(t, ctx, bob), "gregale_get_postgres_schema") {
		t.Fatal("another session lists a tool it did not find")
	}
	// Unlisted is not unavailable: the backend 404s, the tool ran.
	callTool(t, ctx, bob, "gregale_get_postgres_schema", map[string]any{"app": "alpha", "database": "main"}, true)
}

// A capability released while a connection is open adds its tools on a later
// tools/list, and one withdrawn removes them.
func TestCapabilityChangesReachAnOpenConnection(t *testing.T) {
	var released atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/capabilities" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if released.Load() {
			_, _ = w.Write([]byte(`{"plan":"pro","capabilities":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"plan":"pro","capabilities":[],"hidden_capabilities":["incidents"]}`))
	}))
	defer backend.Close()
	sessions, ctx := connectSessions(t, backend, Options{APIBase: backend.URL, Token: testToken, Toolsets: []string{ToolsetAll}, HideUnavailableCapabilities: true}, 1, func(m *management) { m.catalog.refreshEvery = 0 })
	cs := sessions[0]
	if slices.Contains(listedNames(t, ctx, cs), "gregale_list_incidents") {
		t.Fatal("hidden capability listed")
	}
	released.Store(true)
	if !slices.Contains(listedNames(t, ctx, cs), "gregale_list_incidents") {
		t.Fatal("released capability not listed")
	}
	released.Store(false)
	if slices.Contains(listedNames(t, ctx, cs), "gregale_list_incidents") {
		t.Fatal("withdrawn capability still listed")
	}
}
