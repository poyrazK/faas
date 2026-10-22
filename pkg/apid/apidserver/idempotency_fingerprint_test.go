package apidserver_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// withIdempotencyHostKey installs the host HMAC key the request fingerprint
// is keyed with; apid refuses to start without one in production.
func withIdempotencyHostKey(t *testing.T) {
	t.Helper()
	previous := *apidserver.HostHMACKey
	*apidserver.HostHMACKey = func() []byte { return []byte("idempotency-test-host-hmac-key-32") }
	t.Cleanup(func() { *apidserver.HostHMACKey = previous })
}

// TestIdempotent_KeyIsBoundToItsRequest pins H12-26: `gregale send` with the
// same --idempotency-key and a new payload replayed the first message as
// queued. A key is now bound to its first request's fingerprint: a different
// request with the key is refused with idempotency_key_reused and does not
// run, while a retry of the same request still replays.
func TestIdempotent_KeyIsBoundToItsRequest(t *testing.T) {
	const first = `{"queue":"jobs","body":"one"}`
	for _, tc := range []struct {
		name string
		// family is the route family under test: "" (any keyed route),
		// "deploy" or "intent" (project apply).
		family    string
		noHostKey bool
		// legacyReceipt stores the first response without a fingerprint, as
		// apid before ADR-20261022204036 did, instead of running it.
		legacyReceipt bool
		retry         string
		wantStatus    int
		wantReplayed  bool
		wantRuns      int32
	}{
		{name: "same request replays", retry: first, wantStatus: http.StatusCreated, wantReplayed: true, wantRuns: 1},
		{name: "equivalent JSON replays", retry: "{ \"body\": \"one\", \"queue\": \"jobs\" }\n", wantStatus: http.StatusCreated, wantReplayed: true, wantRuns: 1},
		{name: "different payload is refused", retry: `{"queue":"jobs","body":"two"}`, wantStatus: http.StatusUnprocessableEntity, wantRuns: 1},
		{name: "receipt from an earlier release keeps replaying", legacyReceipt: true, retry: `{"queue":"jobs","body":"two"}`, wantStatus: http.StatusCreated, wantReplayed: true},
		{name: "no host key keeps replaying", noHostKey: true, retry: `{"queue":"jobs","body":"two"}`, wantStatus: http.StatusCreated, wantReplayed: true, wantRuns: 1},
		{name: "deploy key names an intent", family: "deploy", retry: `{"queue":"jobs","body":"two"}`, wantStatus: http.StatusCreated, wantReplayed: true, wantRuns: 1},
		{name: "project apply key names an intent", family: "intent", retry: `{"queue":"jobs","body":"two"}`, wantStatus: http.StatusCreated, wantReplayed: true, wantRuns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.noHostKey {
				withIdempotencyHostKey(t)
			}
			e := apidserver.Setup(t, api.PlanPro)
			var runs atomic.Int32
			handler := func(w http.ResponseWriter, r *http.Request, _ state.Account) {
				runs.Add(1)
				body, _ := io.ReadAll(r.Body)
				apidserver.WriteJSON(w, http.StatusCreated, map[string]string{"queued": string(body)})
			}
			h := (*e.S()).Idempotent(handler)
			switch tc.family {
			case "deploy":
				h = (*e.S()).IdempotentDeploy(handler)
			case "intent":
				h = (*e.S()).IdempotentIntent(handler)
			}
			call := func(body string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, "/v1/apps/a/queues/send", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", "send-once")
				rec := httptest.NewRecorder()
				h(rec, req, *e.Acct())
				return rec
			}

			var original string
			if tc.legacyReceipt {
				original = `{"queued":"legacy"}`
				if err := (*e.Store()).PutIdempotent(t.Context(), (*e.Acct()).ID, "POST /v1/apps/a/queues/send\nsend-once", http.StatusCreated, []byte(original)); err != nil {
					t.Fatal(err)
				}
			} else {
				rec := call(first)
				if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `\"body\":\"one\"`) {
					t.Fatalf("first request = %d %s; want 201 echoing the full body", rec.Code, rec.Body)
				}
				original = rec.Body.String()
			}

			retry := call(tc.retry)
			if retry.Code != tc.wantStatus || (retry.Header().Get("Idempotent-Replayed") == "true") != tc.wantReplayed {
				t.Fatalf("retry = %d replayed=%q body=%s; want %d replayed=%v", retry.Code, retry.Header().Get("Idempotent-Replayed"), retry.Body, tc.wantStatus, tc.wantReplayed)
			}
			if tc.wantReplayed && retry.Body.String() != original {
				t.Fatalf("retry replayed %s, want the first response %s", retry.Body, original)
			}
			if tc.wantStatus == http.StatusUnprocessableEntity {
				if code := problemCode(t, retry.Body.Bytes()); code != api.CodeIdempotencyKeyReused {
					t.Fatalf("refusal code = %q, want %q", code, api.CodeIdempotencyKeyReused)
				}
				// The refusal is not stored: the original request still replays.
				again := call(first)
				if again.Code != http.StatusCreated || again.Header().Get("Idempotent-Replayed") != "true" || again.Body.String() != original {
					t.Fatalf("original after refusal = %d replayed=%q body=%s; want its first response replayed", again.Code, again.Header().Get("Idempotent-Replayed"), again.Body)
				}
			}
			if got := runs.Load(); got != tc.wantRuns {
				t.Fatalf("handler ran %d times, want %d", got, tc.wantRuns)
			}
		})
	}
}

// TestIdempotent_InFlightKeyRefusesADifferentRequest — while the first
// request still holds the key, the same request waits (409, Retry-After)
// but a different one is refused outright: retrying it can never succeed.
func TestIdempotent_InFlightKeyRefusesADifferentRequest(t *testing.T) {
	withIdempotencyHostKey(t)
	e := apidserver.Setup(t, api.PlanPro)
	release := make(chan struct{})
	entered := make(chan struct{})
	var runs atomic.Int32
	h := (*e.S()).Idempotent(func(w http.ResponseWriter, _ *http.Request, _ state.Account) {
		if runs.Add(1) == 1 {
			close(entered)
			<-release
		}
		apidserver.WriteJSON(w, http.StatusCreated, map[string]string{"id": "msg-1"})
	})
	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/apps/a/inbox", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "inbox-once")
		rec := httptest.NewRecorder()
		h(rec, req, *e.Acct())
		return rec
	}
	first := make(chan *httptest.ResponseRecorder)
	go func() { first <- call(`{"body":"one"}`) }()
	<-entered
	if same := call(`{"body":"one"}`); same.Code != http.StatusConflict || same.Header().Get("Retry-After") == "" {
		t.Fatalf("same request in flight = %d retry-after=%q, want 409 with Retry-After", same.Code, same.Header().Get("Retry-After"))
	}
	if other := call(`{"body":"two"}`); other.Code != http.StatusUnprocessableEntity || problemCode(t, other.Body.Bytes()) != api.CodeIdempotencyKeyReused {
		t.Fatalf("different request in flight = %d %s, want 422 %s", other.Code, other.Body, api.CodeIdempotencyKeyReused)
	}
	close(release)
	if rec := <-first; rec.Code != http.StatusCreated {
		t.Fatalf("first request = %d, want 201", rec.Code)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("handler ran %d times, want 1", got)
	}
}

// TestIdempotent_ReusedKeyOnARealRoute drives the full router: creating a
// second app under the first app's key is refused, not answered with the
// first app.
func TestIdempotent_ReusedKeyOnARealRoute(t *testing.T) {
	withIdempotencyHostKey(t)
	env, _ := apidserver.SetupChangePlan(t, api.PlanPro, "si_test")
	create := func(slug string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/apps", strings.NewReader(`{"slug":"`+slug+`","type":"app"}`))
		r.Header.Set("Authorization", "Bearer "+(*env.Key()))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "create-app-once")
		(*env.H()).ServeHTTP(rec, r)
		return rec
	}
	if rec := create("idem-first"); rec.Code != http.StatusCreated {
		t.Fatalf("create idem-first = %d %s", rec.Code, rec.Body)
	}
	if rec := create("idem-first"); rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry of idem-first = %d replayed=%q, want a 201 replay", rec.Code, rec.Header().Get("Idempotent-Replayed"))
	}
	rec := create("idem-second")
	if rec.Code != http.StatusUnprocessableEntity || problemCode(t, rec.Body.Bytes()) != api.CodeIdempotencyKeyReused {
		t.Fatalf("create idem-second under the same key = %d %s, want 422 %s", rec.Code, rec.Body, api.CodeIdempotencyKeyReused)
	}
	if _, err := (*env.Store()).AppBySlug(t.Context(), "idem-second"); err == nil {
		t.Fatal("the refused request created idem-second")
	}
}
