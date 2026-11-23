package apihostingreceipt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A gateway that has not received the challenge refuses 503 without
// forwarding; the session retries it, replaying the body, until one serves.
func TestCandidateSessionRetriesGatewayRefusals(t *testing.T) {
	var mu sync.Mutex
	var token string
	refusals := 0
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			if refusals < 2 {
				refusals++
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			body, _ := io.ReadAll(r.Body)
			bodies = append(bodies, string(body))
		}
		w.Header().Set(ServedDeploymentHeader, "dep-1")
		w.Header().Set(ServedResponseHeader, CandidateResponseProof("dep-1", token))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	v := Verifier{BaseURL: srv.URL, RequestTimeout: 5 * time.Second, RetryInterval: 5 * time.Millisecond,
		Authorize: func(_ context.Context, _, published string, _ time.Time) error {
			mu.Lock()
			token = published
			mu.Unlock()
			return nil
		}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	_ = resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if resp.StatusCode != http.StatusCreated || refusals != 2 || len(bodies) != 1 || bodies[0] != `{"sku":"a"}` {
		t.Fatalf("status=%d refusals=%d bodies=%q", resp.StatusCode, refusals, bodies)
	}
}

// An app's own 503 carries the candidate's headers and is its answer, not a
// refusal to retry.
func TestCandidateSessionDoesNotRetryTheAppsOwn503(t *testing.T) {
	var mu sync.Mutex
	var token string
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set(ServedDeploymentHeader, "dep-1")
		w.Header().Set(ServedResponseHeader, CandidateResponseProof("dep-1", token))
		if r.Method == http.MethodPost {
			posts++
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	v := Verifier{BaseURL: srv.URL, RequestTimeout: 5 * time.Second,
		Authorize: func(_ context.Context, _, published string, _ time.Time) error {
			mu.Lock()
			token = published
			mu.Unlock()
			return nil
		}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := v.OpenCandidateSession(ctx, "shop", "dep-1", "/", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, session.URL("/orders"), strings.NewReader("x"))
	resp, err := session.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || posts != 1 {
		t.Fatalf("status=%d posts=%d", resp.StatusCode, posts)
	}
}
