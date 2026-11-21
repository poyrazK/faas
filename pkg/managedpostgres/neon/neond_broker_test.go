// adr: 20261112160000 — with neond configured, the backend holds no Neon key:
// its calls go through neond, which calls Neon with its own key and refuses
// deleting a live database's project.
package neon

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	neondpb "github.com/onebox-faas/faas/api/proto/onebox/faas/neond/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/neond"
)

type liveProjects map[string][]string

func (l liveProjects) LiveResourcesInProject(_ context.Context, project string) ([]string, error) {
	return l[project], nil
}

func (l liveProjects) AccountsInProject(_ context.Context, project string) ([]string, error) {
	if len(l[project]) == 0 {
		return nil, nil
	}
	return []string{"acct-1"}, nil
}

// With the broker configured, apid holds no Neon key: a key left in its
// environment stops the backend from loading (ADR-20261112160000 slice 3).
func TestBrokerRefusesAKeyBesideIt(t *testing.T) {
	t.Setenv(neond.BrokerEnv, "unix:///run/faas/neond-test.sock")
	_, err := New(testBackend(), func(string) string { return "napi_left_behind" })
	if err == nil || !strings.Contains(err.Error(), "TEST_NEON_API_KEY") || strings.Contains(err.Error(), "napi_left_behind") {
		t.Fatalf("key beside the broker = %v; want a refusal naming the variable, never the key", err)
	}
	provider, err := New(testBackend(), func(string) string { return "" })
	if err != nil {
		t.Fatalf("broker without a key = %v", err)
	}
	if p := provider.(*Provider); p.apiKey != "" || p.broker == nil {
		t.Fatalf("provider holds key %q, broker %v", p.apiKey, p.broker)
	}
}

func TestBrokerCallsNeonWithNeondsKey(t *testing.T) {
	var mu sync.Mutex
	var seen, auth []string
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen, auth = append(seen, r.Method+" "+r.URL.Path), append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{
			"id": "quiet-river-12345678", "compute_time_seconds": 12, "consumption_period_start": "2026-11-01T00:00:00Z",
			"settings": map[string]any{"quota": map[string]any{"compute_time_seconds": 3600}},
		}})
	}))
	server, err := neond.NewServer(p.baseURL, p.httpClient, "neond-admin-key",
		liveProjects{"quiet-river-12345678": {"quiet-river-12345678"}}, neond.Limits{PerHour: 100, PerSubjectPerHour: 100}, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	neondpb.RegisterNeondServer(srv, server)
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///neond", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	p.apiKey, p.broker = "", neond.NewClient(conn)

	observed, err := p.ObserveComputeCap(context.Background(), "quiet-river-12345678")
	if err != nil || observed.UsedUnitSeconds != 12 || observed.QuotaUnitSeconds != 3600 {
		t.Fatalf("observe through neond = %+v, %v", observed, err)
	}
	err = p.doJSON(context.Background(), http.MethodDelete, "/projects/quiet-river-12345678", nil, nil, nil, http.StatusOK)
	if !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("delete of a live project = %v; want refused as unavailable", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "GET /api/v2/projects/quiet-river-12345678" || auth[0] != "Bearer neond-admin-key" {
		t.Fatalf("Neon saw %v with %v; want one read with neond's key and no delete", seen, auth)
	}
}
