package apihostingreceipt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCandidateGateway proves responses for the published token once it has
// "received" the challenge, and answers from a different deployment before.
type fakeCandidateGateway struct {
	mu       sync.Mutex
	token    string
	known    atomic.Bool
	methods  []string
	hosts    []string
	lateness int32
}

func (g *fakeCandidateGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.methods = append(g.methods, r.Method+" "+r.URL.Path)
	g.hosts = append(g.hosts, r.Host)
	token := g.token
	g.mu.Unlock()
	if atomic.AddInt32(&g.lateness, -1) >= 0 || !g.known.Load() || r.Header.Get(PlatformSmokeTokenHeader) != token {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set(ServedDeploymentHeader, "dep-1")
	w.Header().Set(ServedResponseHeader, CandidateResponseProof("dep-1", token))
	w.Header().Set("Content-Type", "application/json")
	body, _ := io.ReadAll(r.Body)
	_, _ = w.Write([]byte(`{"method":"` + r.Method + `","body":"` + strings.ReplaceAll(string(body), `"`, `'`) + `"}`))
}

func TestCandidateSessionWaitsForRoutingAndPinsRequests(t *testing.T) {
	gw := &fakeCandidateGateway{lateness: 2}
	srv := httptest.NewServer(gw)
	defer srv.Close()
	v := Verifier{BaseURL: srv.URL, AppsDomain: "apps.example", RequestTimeout: time.Second, RetryInterval: 5 * time.Millisecond,
		Authorize: func(_ context.Context, dep, token string, _ time.Time) error {
			if dep != "dep-1" {
				t.Fatalf("authorized %q", dep)
			}
			gw.mu.Lock()
			gw.token = token
			gw.mu.Unlock()
			gw.known.Store(true)
			return nil
		}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := v.OpenCandidateSession(ctx, "shop", "dep-1", "/healthz", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, session.URL("/orders"), strings.NewReader(`{"sku":"a"}`))
	resp, err := session.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get(ServedResponseHeader) != "" {
		t.Fatal("proof header leaked to the caller")
	}
	gw.mu.Lock()
	defer gw.mu.Unlock()
	if gw.methods[0] != "GET /healthz" || gw.methods[len(gw.methods)-1] != "POST /orders" {
		t.Fatalf("requests = %v", gw.methods)
	}
	for _, h := range gw.hosts {
		if h != "shop.apps.example" {
			t.Fatalf("host = %q", h)
		}
	}
}

func TestCandidateSessionRejectsUnprovenResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(ServedDeploymentHeader, "dep-1")
		w.Header().Set(ServedResponseHeader, "forged")
	}))
	defer srv.Close()
	v := Verifier{BaseURL: srv.URL, RetryInterval: 5 * time.Millisecond,
		Authorize: func(context.Context, string, string, time.Time) error { return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := v.OpenCandidateSession(ctx, "shop", "dep-1", "/", time.Minute)
	if err == nil || !errors.Is(err, ErrCandidateUnproven) {
		t.Fatalf("want ErrCandidateUnproven, got %v", err)
	}
}
