// adr: 20261112160000 — neond admits only the Neon calls apid's backend
// makes, refuses destroying a live database's project or branch, rate-limits
// destructive calls and forwards the rest with its own key.
package neond

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	neondpb "github.com/onebox-faas/faas/api/proto/onebox/faas/neond/v1"
)

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		route        string
		params       Params
	}{
		{"GET", "/projects", "list projects", Params{}},
		{"POST", "/projects", "create project", Params{}},
		{"DELETE", "/projects/quiet-river-123", "delete project", Params{Project: "quiet-river-123"}},
		{"DELETE", "/projects/p-1/branches/br-x", "delete branch", Params{Project: "p-1", Branch: "br-x"}},
		{"POST", "/projects/p-1/snapshots/snap-1/restore", "restore snapshot", Params{Project: "p-1"}},
		{"GET", "/consumption_history/v2/projects", "read consumption", Params{}},
		{"DELETE", "/projects", "", Params{}},
		{"PUT", "/projects/p-1", "", Params{}},
		{"GET", "/projects/../private", "", Params{}},
		{"GET", "/projects/p%2F1", "", Params{}},
		{"GET", "/projects/p-1/", "", Params{}},
		{"GET", "/users/me", "", Params{}},
		{"POST", "/organizations/org/api_keys", "", Params{}},
		{"GET", "projects", "", Params{}},
	} {
		route, params, ok := Match(tc.method, tc.path)
		if ok != (tc.route != "") || route.Name != tc.route || params != tc.params {
			t.Errorf("Match(%s %s) = %q %+v %v; want %q %+v", tc.method, tc.path, route.Name, params, ok, tc.route, tc.params)
		}
	}
}

type fakeLive map[string][]string

func (f fakeLive) LiveResourcesInProject(_ context.Context, project string) ([]string, error) {
	if project == "broken" {
		return nil, errors.New("database unavailable")
	}
	return f[project], nil
}

func TestPolicy(t *testing.T) {
	live := fakeLive{"live": {"live"}, "branched": {"branched/br-live"}}
	defaults := map[string]bool{"live/br-main": true}
	branch := func(_ context.Context, project, b string) (bool, error) { return defaults[project+"/"+b], nil }
	check := func(method, path string) error {
		t.Helper()
		route, params, ok := Match(method, path)
		if !ok {
			t.Fatalf("%s %s not on the allow-list", method, path)
		}
		return NewPolicy(live, branch, 100, nil).Check(context.Background(), route, params, path)
	}
	for _, tc := range []struct {
		method, path string
		want         error
	}{
		{"GET", "/projects/live", nil},
		{"PATCH", "/projects/live", nil},
		{"DELETE", "/projects/live", ErrRefused},
		{"DELETE", "/projects/gone", nil},
		{"DELETE", "/projects/live/branches/br-main", ErrRefused},
		{"DELETE", "/projects/live/branches/br-restore", nil},
		{"DELETE", "/projects/branched/branches/br-live", ErrRefused},
		{"DELETE", "/projects/branched/branches/br-main", nil},
		{"DELETE", "/projects/branched", ErrRefused},
		{"DELETE", "/projects/live/branches/br-1/roles/gregale_owner", ErrRefused},
		{"DELETE", "/projects/live/branches/br-1/roles/gregale_0123456789abcdef0123456789abcdef01234567", nil},
		{"DELETE", "/projects/live/snapshots/snap-1", nil},
	} {
		if err := check(tc.method, tc.path); !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Errorf("%s %s = %v; want %v", tc.method, tc.path, err, tc.want)
		}
	}
	if err := check("DELETE", "/projects/broken"); err == nil || errors.Is(err, ErrRefused) {
		t.Fatalf("unreadable live set = %v; want a failure, not a decision", err)
	}
}

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	l := NewLimiter(2, time.Hour, func() time.Time { return now })
	if !l.Allow() || !l.Allow() || l.Allow() {
		t.Fatal("want two then a refusal")
	}
	now = now.Add(time.Hour)
	if !l.Allow() {
		t.Fatal("window passed: want admitted")
	}
}

// neonFake is Neon's API as neond sees it: it records each request and its
// Authorization header.
type neonFake struct {
	mu       sync.Mutex
	requests []string
	auth     []string
}

func (f *neonFake) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/api/v2/projects/slow":
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/live/branches/br-main":
			_ = json.NewEncoder(w).Encode(map[string]any{"branch": map[string]any{"id": "br-main", "default": true}})
		default:
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"echo": string(body)})
		}
	})
}

func startNeond(t *testing.T, live LiveResources, perHour int) (*Client, *neonFake, *prometheus.Registry) {
	t.Helper()
	fake := &neonFake{}
	neon := httptest.NewServer(fake.handler(t))
	t.Cleanup(neon.Close)
	base, _ := url.Parse(neon.URL + "/api/v2")
	reg := prometheus.NewRegistry()
	server, err := NewServer(base, neon.Client(), "admin-key", live, perHour, reg, slog.New(slog.DiscardHandler))
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
	return NewClient(conn), fake, reg
}

func TestServerForwardsWithItsOwnKey(t *testing.T) {
	client, fake, _ := startNeond(t, fakeLive{"live": {"live"}}, 100)
	ctx := context.Background()
	status, body, _, err := client.Forward(ctx, http.MethodPatch, "/projects/live", "a=1", []byte(`{"project":{}}`))
	if err != nil || status != http.StatusOK || string(body) != "{\"echo\":\"{\\\"project\\\":{}}\"}\n" {
		t.Fatalf("forward = %d %q %v", status, body, err)
	}
	if fake.requests[0] != "PATCH /api/v2/projects/live?a=1" || fake.auth[0] != "Bearer admin-key" {
		t.Fatalf("Neon saw %v with %v", fake.requests, fake.auth)
	}
	if status, _, retry, err := client.Forward(ctx, http.MethodGet, "/projects/slow", "", nil); err != nil || status != http.StatusTooManyRequests || retry != "7" {
		t.Fatalf("429 = %d %q %v; want Retry-After passed through", status, retry, err)
	}
}

func TestServerRefusesWithoutCallingNeon(t *testing.T) {
	client, fake, reg := startNeond(t, fakeLive{"live": {"live"}}, 1)
	ctx := context.Background()
	if _, _, _, err := client.Forward(ctx, http.MethodGet, "/users/me", "", nil); !errors.Is(err, ErrRefused) {
		t.Fatalf("off the allow-list = %v", err)
	}
	if _, _, _, err := client.Forward(ctx, http.MethodDelete, "/projects/live", "", nil); !errors.Is(err, ErrRefused) {
		t.Fatalf("live project delete = %v", err)
	}
	// The default branch is read to decide; the delete is never sent.
	if _, _, _, err := client.Forward(ctx, http.MethodDelete, "/projects/live/branches/br-main", "", nil); !errors.Is(err, ErrRefused) {
		t.Fatalf("live default branch delete = %v", err)
	}
	for _, request := range fake.requests {
		if request[:6] == "DELETE" {
			t.Fatalf("Neon saw a refused delete: %v", fake.requests)
		}
	}
	if _, _, _, err := client.Forward(ctx, http.MethodDelete, "/projects/gone", "", nil); err != nil {
		t.Fatalf("first destructive call = %v", err)
	}
	if _, _, _, err := client.Forward(ctx, http.MethodDelete, "/projects/gone-too", "", nil); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("past the limit = %v", err)
	}
	families, err := reg.Gather()
	if err != nil || len(families) != 1 || len(families[0].GetMetric()) < 4 {
		t.Fatalf("metrics = %v, %v; want denied, refused, forwarded and rate_limited", families, err)
	}
}
