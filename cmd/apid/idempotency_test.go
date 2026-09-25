package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestIdempotent_ConcurrentRetryRunsOnce — the middleware checked for a
// cached response, ran the handler, then stored the response, so a client
// retrying after a timeout while the first request was still running
// executed the operation twice. The key is now reserved first: the
// concurrent retry gets 409, and a retry after completion replays.
func TestIdempotent_ConcurrentRetryRunsOnce(t *testing.T) {
	e := setup(t, api.PlanPro)
	var runs atomic.Int32
	release := make(chan struct{})
	entered := make(chan struct{})
	h := e.s.idempotent(func(w http.ResponseWriter, _ *http.Request, _ state.Account) {
		if runs.Add(1) == 1 {
			close(entered)
			<-release
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": "dep-1"})
	})
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/apps/a/deployments", nil)
		req.Header.Set("Idempotency-Key", "ci-retry")
		rec := httptest.NewRecorder()
		h(rec, req, e.acct)
		return rec
	}

	first := make(chan *httptest.ResponseRecorder)
	go func() { first <- call() }()
	<-entered
	if retry := call(); retry.Code != http.StatusConflict {
		t.Fatalf("concurrent retry = %d, want 409 while the first request holds the key (body=%s)", retry.Code, retry.Body)
	}
	close(release)
	if rec := <-first; rec.Code != http.StatusCreated {
		t.Fatalf("first request = %d, want 201", rec.Code)
	}
	replay := call()
	if replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry after completion = %d replayed=%q, want a 201 replay", replay.Code, replay.Header().Get("Idempotent-Replayed"))
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("handler ran %d times, want 1", got)
	}
}

// TestIdempotent_KeyIsScopedToEndpoint — keys were stored per account only,
// so reusing one on another endpoint replayed that endpoint's response and
// the new operation silently never ran.
func TestIdempotent_KeyIsScopedToEndpoint(t *testing.T) {
	e := setup(t, api.PlanPro)
	var runs atomic.Int32
	h := e.s.idempotent(func(w http.ResponseWriter, r *http.Request, _ state.Account) {
		runs.Add(1)
		writeJSON(w, http.StatusCreated, map[string]string{"path": r.URL.Path})
	})
	for _, path := range []string{"/v1/apps/a/deployments", "/v1/apps/b/deployments", "/v1/apps/a/secrets"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Idempotency-Key", "commit-abc123")
		rec := httptest.NewRecorder()
		h(rec, req, e.acct)
		if rec.Header().Get("Idempotent-Replayed") == "true" {
			t.Fatalf("POST %s replayed another endpoint's response: %s", path, rec.Body)
		}
	}
	if got := runs.Load(); got != 3 {
		t.Fatalf("handler ran %d times, want 3 (one per endpoint)", got)
	}
}

// TestIdempotent_AbandonedReservationIsTakenOver — a request that crashed
// mid-handler leaves an in-flight reservation; once it is older than
// idempotencyAbandonAfter a retry must run rather than 409 forever.
func TestIdempotent_AbandonedReservationIsTakenOver(t *testing.T) {
	store := state.NewMemStore()
	res, err := store.ReserveIdempotent(t.Context(), "acct", "k", time.Hour)
	if err != nil || !res.Reserved {
		t.Fatalf("first reserve = %+v err=%v, want reserved", res, err)
	}
	if res, _ := store.ReserveIdempotent(t.Context(), "acct", "k", time.Hour); !res.InFlight {
		t.Fatalf("second reserve = %+v, want in-flight", res)
	}
	if _, _, err := store.GetIdempotent(t.Context(), "acct", "k"); err == nil {
		t.Fatal("GetIdempotent returned an in-flight reservation as a completed response")
	}
	if res, _ := store.ReserveIdempotent(t.Context(), "acct", "k", 0); !res.Reserved {
		t.Fatalf("reserve past abandonAfter = %+v, want reserved", res)
	}
}
