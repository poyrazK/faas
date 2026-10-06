package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestIdempotent_GateRefusalIsNotReplayed — the CLI derives deploy keys
// from the deploy's content, and every response was cached for 24 h. A
// deploy refused while the account was past_due replayed that 402 after
// the customer paid; the same held for the email-verification 403 and the
// hourly deploy limit's 429.
func TestIdempotent_GateRefusalIsNotReplayed(t *testing.T) {
	env, _ := setupChangePlan(t, api.PlanHobby, "si_test")
	do := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/apps/idem-gate/deployments", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+env.key)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "gregale-deploy-same-content")
		env.h.ServeHTTP(rec, r)
		return rec
	}
	create := httptest.NewRecorder()
	cr := httptest.NewRequest(http.MethodPost, "/v1/apps", strings.NewReader(`{"slug":"idem-gate","runtime":"node22"}`))
	cr.Header.Set("Authorization", "Bearer "+env.key)
	cr.Header.Set("Content-Type", "application/json")
	env.h.ServeHTTP(create, cr)
	if create.Code != http.StatusCreated {
		t.Fatalf("create app = %d %s", create.Code, create.Body)
	}
	if err := env.store.MarkDunningStep(t.Context(), env.acct.ID, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	deploy := `{"image":"registry.gregale.dev/app@sha256:a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"}`
	if rec := do(deploy); rec.Code != http.StatusPaymentRequired {
		t.Fatalf("past_due deploy = %d, want 402", rec.Code)
	}
	if err := env.store.UpdateAccountStatus(t.Context(), env.acct.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	rec := do(deploy)
	if rec.Code != http.StatusAccepted || rec.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("same deploy after paying = %d replayed=%q, want a fresh 202", rec.Code, rec.Header().Get("Idempotent-Replayed"))
	}
	if again := do(deploy); again.Code != http.StatusAccepted || again.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry of the accepted deploy = %d replayed=%q, want the 202 replayed", again.Code, again.Header().Get("Idempotent-Replayed"))
	}
}

func TestIdempotencyReplayable(t *testing.T) {
	for status, want := range map[int]bool{
		200: true, 201: true, 202: true, 400: true, 404: true, 409: true, 422: true, 500: true,
		401: false, 402: false, 403: false, 429: false, 503: false,
	} {
		if got := idempotencyReplayable(status); got != want {
			t.Errorf("idempotencyReplayable(%d) = %v, want %v", status, got, want)
		}
	}
}

func TestIdempotencyBindingRefusalsAreRetryable(t *testing.T) {
	for _, code := range []string{"bindings_check_failed", "bindings_check_changed", api.CodeBindingReleaseRequired, api.CodeBindingReleasePolicyChanged} {
		if idempotencyResponseReplayable(http.StatusConflict, []byte(`{"code":"`+code+`"}`)) {
			t.Errorf("binding refusal %s would freeze the retry key", code)
		}
	}
	for _, body := range []string{`{"code":"conflict"}`, `{"code":"canary_step_conflict"}`, `not-json`} {
		if !idempotencyResponseReplayable(http.StatusConflict, []byte(body)) {
			t.Errorf("unrelated conflict lost replay behavior: %s", body)
		}
	}
	if !idempotencyResponseReplayable(http.StatusOK, []byte(`{"code":"bindings_check_failed"}`)) {
		t.Fatal("successful responses must remain replayable")
	}
}

// TestIdempotent_FailedDeployIsNotReplayed — the CLI derives the deploy
// Idempotency-Key from the source digest and flags, so re-running the same
// `gregale deploy` after a transient build failure replayed the 202 for the
// failed deployment and streamed the old failure for 24 h (production-us).
// A failed, cancelled or superseded deployment now runs again; one still in
// progress is replayed, so a network retry still cannot deploy twice.
func TestIdempotent_FailedDeployIsNotReplayed(t *testing.T) {
	env, _ := setupChangePlan(t, api.PlanHobby, "si_test")
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+env.key)
		r.Header.Set("Content-Type", "application/json")
		if method == http.MethodPost && strings.HasSuffix(path, "/deployments") {
			r.Header.Set("Idempotency-Key", "gregale-deploy-same-content")
		}
		env.h.ServeHTTP(rec, r)
		return rec
	}
	if rec := do(http.MethodPost, "/v1/apps", `{"slug":"idem-fail","runtime":"node22"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create app = %d %s", rec.Code, rec.Body)
	}
	deploy := `{"image":"registry.gregale.dev/app@sha256:a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"}`
	deployID := func(rec *httptest.ResponseRecorder) string {
		t.Helper()
		var dep api.DeploymentResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &dep); err != nil || dep.ID == "" {
			t.Fatalf("decode deployment: %v (body=%s)", err, rec.Body)
		}
		return dep.ID
	}

	first := do(http.MethodPost, "/v1/apps/idem-fail/deployments", deploy)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first deploy = %d %s", first.Code, first.Body)
	}
	firstID := deployID(first)
	if again := do(http.MethodPost, "/v1/apps/idem-fail/deployments", deploy); again.Header().Get("Idempotent-Replayed") != "true" || deployID(again) != firstID {
		t.Fatalf("retry while in progress = %d replayed=%q, want the first deployment replayed", again.Code, again.Header().Get("Idempotent-Replayed"))
	}

	for i, status := range []state.DeploymentStatus{state.DeployFailed, state.DeployCancelled, state.DeploySuperseded} {
		prevID := firstID
		if err := env.store.UpdateDeploymentStatus(t.Context(), prevID, status, "builder lost"); err != nil {
			t.Fatal(err)
		}
		rerun := do(http.MethodPost, "/v1/apps/idem-fail/deployments", deploy)
		if rerun.Code != http.StatusAccepted || rerun.Header().Get("Idempotent-Replayed") != "" {
			t.Fatalf("rerun %d after %s = %d replayed=%q, want a fresh 202", i, status, rerun.Code, rerun.Header().Get("Idempotent-Replayed"))
		}
		firstID = deployID(rerun)
		if firstID == prevID {
			t.Fatalf("rerun after %s returned the %s deployment %s again", status, status, prevID)
		}
		if replay := do(http.MethodPost, "/v1/apps/idem-fail/deployments", deploy); replay.Header().Get("Idempotent-Replayed") != "true" || deployID(replay) != firstID {
			t.Fatalf("retry of the new deployment = replayed=%q, want it replayed", replay.Header().Get("Idempotent-Replayed"))
		}
	}
}
