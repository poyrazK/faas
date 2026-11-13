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

// fakeLive maps a project to its live resources. A project with live
// resources is named by account "acct-<project>"; "accounts/<project>" lists
// the accounts of a project explicitly.
type fakeLive map[string][]string

func (f fakeLive) LiveResourcesInProject(_ context.Context, project string) ([]string, error) {
	if project == "broken" {
		return nil, errors.New("database unavailable")
	}
	return f[project], nil
}

func (f fakeLive) AccountsInProject(_ context.Context, project string) ([]string, error) {
	if project == "broken" {
		return nil, errors.New("database unavailable")
	}
	if accounts, ok := f["accounts/"+project]; ok {
		return accounts, nil
	}
	if len(f[project]) > 0 {
		return []string{"acct-" + project}, nil
	}
	return nil, nil
}

func TestPolicy(t *testing.T) {
	live := fakeLive{"live": {"live"}, "branched": {"branched/br-live"}, "accounts/deleting": {"acct-9"}}
	defaults := map[string]bool{"live/br-main": true}
	branch := func(_ context.Context, project, b string) (bool, error) { return defaults[project+"/"+b], nil }
	check := func(method, path string) error {
		t.Helper()
		route, params, ok := Match(method, path)
		if !ok {
			t.Fatalf("%s %s not on the allow-list", method, path)
		}
		return NewPolicy(live, branch, Limits{PerHour: 100, PerSubjectPerHour: 100}, nil).Check(context.Background(), route, params, path)
	}
	for _, tc := range []struct {
		method, path string
		want         error
	}{
		{"GET", "/projects/live", nil},
		{"PATCH", "/projects/live", nil},
		// Quota writes and role deletes only on a project a database names,
		// in any state.
		{"PATCH", "/projects/stranger", ErrRefused},
		{"PATCH", "/projects/deleting", nil},
		{"DELETE", "/projects/stranger/branches/br-1/roles/gregale_0123456789abcdef0123456789abcdef01234567", ErrRefused},
		{"PATCH", "/projects/stranger/endpoints/ep-1", nil},
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
	l := NewLimiter(2, 100, time.Hour, func() time.Time { return now })
	allow := func(subjects ...string) bool {
		_, ok := l.Allow(subjects)
		return ok
	}
	if !allow() || !allow() || allow() {
		t.Fatal("want two then a refusal")
	}
	now = now.Add(time.Hour)
	if !allow() {
		t.Fatal("window passed: want admitted")
	}
}

func TestLimiterPerSubject(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	l := NewLimiter(5, 2, time.Hour, func() time.Time { return now })
	for i := range 2 {
		if subject, ok := l.Allow([]string{"acct-a"}); !ok {
			t.Fatalf("call %d refused for %q", i, subject)
		}
	}
	if subject, ok := l.Allow([]string{"acct-b", "acct-a"}); ok || subject != "acct-a" {
		t.Fatalf("third call of acct-a = %q %v; want refused for acct-a", subject, ok)
	}
	// The refused call counted against nobody: acct-b and the fleet still
	// have room.
	if _, ok := l.Allow([]string{"acct-b"}); !ok {
		t.Fatal("acct-b refused")
	}
	if _, ok := l.Allow(nil); !ok {
		t.Fatal("unowned call refused")
	}
	if _, ok := l.Allow([]string{"acct-b"}); !ok {
		t.Fatal("acct-b's second call refused")
	}
	// The fleet limit (5) is now reached for everyone.
	if subject, ok := l.Allow([]string{"acct-c"}); ok || subject != "" {
		t.Fatalf("past the fleet limit = %q %v", subject, ok)
	}
	now = now.Add(time.Hour)
	if _, ok := l.Allow([]string{"acct-a"}); !ok {
		t.Fatal("window passed: want acct-a admitted")
	}
}

func TestPolicyPerSubjectLimit(t *testing.T) {
	live := fakeLive{"accounts/old-a": {"acct-a"}, "accounts/old-a2": {"acct-a"}, "accounts/old-b": {"acct-b"}}
	policy := NewPolicy(live, nil, Limits{PerHour: 100, PerSubjectPerHour: 1}, nil)
	check := func(path string) error {
		t.Helper()
		route, params, _ := Match(http.MethodDelete, path)
		return policy.Check(context.Background(), route, params, path)
	}
	if err := check("/projects/old-a/snapshots/s-1"); err != nil {
		t.Fatal(err)
	}
	err := check("/projects/old-a2")
	if !errors.Is(err, ErrSubjectRateLimited) || !errors.Is(err, ErrRateLimited) {
		t.Fatalf("acct-a's second delete = %v; want the account limit", err)
	}
	if err := check("/projects/old-b"); err != nil {
		t.Fatalf("acct-b = %v", err)
	}
	// Projects no database names (failed creates, copy targets) count only
	// against the fleet limit.
	for _, path := range []string{"/projects/orphan-1", "/projects/orphan-2"} {
		if err := check(path); err != nil {
			t.Fatalf("%s = %v", path, err)
		}
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
	return startNeondLimits(t, live, Limits{PerHour: perHour, PerSubjectPerHour: perHour})
}

func startNeondLimits(t *testing.T, live LiveResources, limits Limits) (*Client, *neonFake, *prometheus.Registry) {
	t.Helper()
	fake := &neonFake{}
	neon := httptest.NewServer(fake.handler(t))
	t.Cleanup(neon.Close)
	base, _ := url.Parse(neon.URL + "/api/v2")
	reg := prometheus.NewRegistry()
	server, err := NewServer(base, neon.Client(), "admin-key", live, limits, reg, slog.New(slog.DiscardHandler))
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
	requests := 0
	for _, family := range families {
		if family.GetName() == "neond_requests_total" {
			requests = len(family.GetMetric())
		}
	}
	if err != nil || requests < 4 {
		t.Fatalf("neond_requests_total has %d series, %v; want denied, refused, forwarded and rate_limited", requests, err)
	}
}
