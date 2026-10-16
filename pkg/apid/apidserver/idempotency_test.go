package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestIdempotentReplayCannotBeSniffedAsHTML(t *testing.T) {
	body := []byte(`{"value":"<script>alert(1)</script>"}`)
	w := httptest.NewRecorder()
	apidserver.ReplayIdempotent(w, http.StatusCreated, body)
	if w.Code != http.StatusCreated || w.Body.String() != string(body) {
		t.Fatalf("replay changed status or cached body: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "application/json" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay lost its JSON security headers: %v", w.Header())
	}
}

// TestIdempotent_ConcurrentRetryRunsOnce — the middleware checked for a
// cached response, ran the handler, then stored the response, so a client
// retrying after a timeout while the first request was still running
// executed the operation twice. The key is now reserved first: the
// concurrent retry gets 409, and a retry after completion replays.
func TestIdempotent_ConcurrentRetryRunsOnce(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	var runs atomic.Int32
	release := make(chan struct{})
	entered := make(chan struct{})
	h := (*e.S()).Idempotent(func(w http.ResponseWriter, _ *http.Request, _ state.Account) {
		if runs.Add(1) == 1 {
			close(entered)
			<-release
		}
		apidserver.WriteJSON(w, http.StatusCreated, map[string]string{"id": "dep-1"})
	})
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/apps/a/deployments", nil)
		req.Header.Set("Idempotency-Key", "ci-retry")
		rec := httptest.NewRecorder()
		h(rec, req, (*e.Acct()))
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
	e := apidserver.Setup(t, api.PlanPro)
	var runs atomic.Int32
	h := (*e.S()).Idempotent(func(w http.ResponseWriter, r *http.Request, _ state.Account) {
		runs.Add(1)
		apidserver.WriteJSON(w, http.StatusCreated, map[string]string{"path": r.URL.Path})
	})
	for _, path := range []string{"/v1/apps/a/deployments", "/v1/apps/b/deployments", "/v1/apps/a/secrets"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Idempotency-Key", "commit-abc123")
		rec := httptest.NewRecorder()
		h(rec, req, (*e.Acct()))
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
	env, _ := apidserver.SetupChangePlan(t, api.PlanHobby, "si_test")
	do := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/apps/idem-gate/deployments", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+(*env.Key()))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "gregale-deploy-same-content")
		(*env.H()).ServeHTTP(rec, r)
		return rec
	}
	create := httptest.NewRecorder()
	cr := httptest.NewRequest(http.MethodPost, "/v1/apps", strings.NewReader(`{"slug":"idem-gate","type":"app"}`))
	cr.Header.Set("Authorization", "Bearer "+(*env.Key()))
	cr.Header.Set("Content-Type", "application/json")
	(*env.H()).ServeHTTP(create, cr)
	if create.Code != http.StatusCreated {
		t.Fatalf("create app = %d %s", create.Code, create.Body)
	}
	if err := (*env.Store()).MarkDunningStep(t.Context(), (*env.Acct()).ID, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	deploy := `{"image":"registry.gregale.dev/app@sha256:a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"}`
	if rec := do(deploy); rec.Code != http.StatusPaymentRequired {
		t.Fatalf("past_due deploy = %d, want 402", rec.Code)
	}
	if err := (*env.Store()).UpdateAccountStatus(t.Context(), (*env.Acct()).ID, state.AccountActive); err != nil {
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

// adr: 1330
// TestIdempotent_OneTimeCredentialIsNotReplayed — an interactive session's
// create response carries a one-time attach token. Storing it would put the
// plaintext token in the idempotency table and hand the same token to every
// retry with the key; a retry runs the operation again.
func TestIdempotent_OneTimeCredentialIsNotReplayed(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	var runs atomic.Int32
	h := (*e.S()).Idempotent(func(w http.ResponseWriter, _ *http.Request, _ state.Account) {
		n := runs.Add(1)
		w.Header().Set("Cache-Control", "no-store")
		httpkit.SkipIdempotentReplay(w)
		apidserver.WriteJSON(w, http.StatusCreated, map[string]string{"token": "one-time-" + string(rune('0'+n))})
	})
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/image-runs", nil)
		req.Header.Set("Idempotency-Key", "interactive-run")
		rec := httptest.NewRecorder()
		h(rec, req, (*e.Acct()))
		return rec
	}
	first, second := call(), call()
	for i, rec := range []*httptest.ResponseRecorder{first, second} {
		if rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "" {
			t.Fatalf("call %d = %d replayed=%q, want a fresh 201", i+1, rec.Code, rec.Header().Get("Idempotent-Replayed"))
		}
	}
	if first.Body.String() == second.Body.String() || runs.Load() != 2 {
		t.Fatalf("handler runs=%d first=%s second=%s; want two runs with distinct tokens", runs.Load(), first.Body, second.Body)
	}
	if _, _, err := (*e.Store()).GetIdempotent(t.Context(), (*e.Acct()).ID, "POST /v1/image-runs\ninteractive-run"); err == nil {
		t.Fatal("no-store response was stored for replay")
	}
	// The key was released, not left in flight: a later request still runs.
	if third := call(); third.Code != http.StatusCreated || runs.Load() != 3 {
		t.Fatalf("third call = %d runs=%d", third.Code, runs.Load())
	}
}

// adr: 1330
// TestIdempotent_NoStoreHeaderAloneIsStillReplayed — many handlers send
// Cache-Control: no-store only to keep HTTP caches away. Their keyed
// mutations must still replay, or a retry would run them twice.
func TestIdempotent_NoStoreHeaderAloneIsStillReplayed(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	var runs atomic.Int32
	h := (*e.S()).Idempotent(func(w http.ResponseWriter, _ *http.Request, _ state.Account) {
		runs.Add(1)
		w.Header().Set("Cache-Control", "no-store")
		apidserver.WriteJSON(w, http.StatusOK, map[string]string{"status": "pending"})
	})
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/workflows/runs/r1/resume", nil)
		req.Header.Set("Idempotency-Key", "resume-once")
		rec := httptest.NewRecorder()
		h(rec, req, (*e.Acct()))
		return rec
	}
	call()
	if replay := call(); replay.Header().Get("Idempotent-Replayed") != "true" || runs.Load() != 1 {
		t.Fatalf("retry replayed=%q runs=%d; want a replay of the first response", replay.Header().Get("Idempotent-Replayed"), runs.Load())
	}
}

func TestIdempotencyReplayable(t *testing.T) {
	for status, want := range map[int]bool{
		200: true, 201: true, 202: true, 400: true, 409: true, 422: true, 500: true,
		401: false, 402: false, 403: false, 404: false, 429: false, 501: false, 503: false,
	} {
		if got := apidserver.IdempotencyReplayable(status); got != want {
			t.Errorf("idempotencyReplayable(%d) = %v, want %v", status, got, want)
		}
	}
}

func TestIdempotencyBindingRefusalsAreRetryable(t *testing.T) {
	for _, code := range []string{"bindings_check_failed", "bindings_check_changed", api.CodeBindingReleaseRequired, api.CodeBindingReleasePolicyChanged} {
		if apidserver.IdempotencyResponseReplayable(http.StatusConflict, []byte(`{"code":"`+code+`"}`)) {
			t.Errorf("binding refusal %s would freeze the retry key", code)
		}
	}
	for _, body := range []string{`{"code":"conflict"}`, `{"code":"canary_step_conflict"}`, `not-json`} {
		if !apidserver.IdempotencyResponseReplayable(http.StatusConflict, []byte(body)) {
			t.Errorf("unrelated conflict lost replay behavior: %s", body)
		}
	}
	if !apidserver.IdempotencyResponseReplayable(http.StatusOK, []byte(`{"code":"bindings_check_failed"}`)) {
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
	env, _ := apidserver.SetupChangePlan(t, api.PlanHobby, "si_test")
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+(*env.Key()))
		r.Header.Set("Content-Type", "application/json")
		if method == http.MethodPost && strings.HasSuffix(path, "/deployments") {
			r.Header.Set("Idempotency-Key", "gregale-deploy-same-content")
		}
		(*env.H()).ServeHTTP(rec, r)
		return rec
	}
	if rec := do(http.MethodPost, "/v1/apps", `{"slug":"idem-fail","type":"app"}`); rec.Code != http.StatusCreated {
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
		if err := (*env.Store()).UpdateDeploymentStatus(t.Context(), prevID, status, "builder lost"); err != nil {
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
