// adr: 201
package gateway

// ADR-201 §1 safety rules. Every test here pins one rule, because the cost of
// getting a rule wrong is not a failed request — it is a customer's side
// effect running twice.

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/reqbudget"
)

type recordingObserver struct {
	attempts  []string
	exhausted []string
}

func (o *recordingObserver) IncRetryAttempt(outcome string) {
	o.attempts = append(o.attempts, outcome)
}
func (o *recordingObserver) IncRetryExhausted(reason string) {
	o.exhausted = append(o.exhausted, reason)
}

func testPolicy() RetryPolicy {
	return RetryPolicy{Enabled: true, MaxAttempts: 2, MinRemaining: 250 * time.Millisecond}
}

// replayableRequest builds a request shaped the way admitRequestBody leaves
// one: body fully buffered, GetBody populated.
func replayableRequest(method, body string) *http.Request {
	r := httptest.NewRequest(method, "http://app.example/x", strings.NewReader(body))
	payload := []byte(body)
	r.Body = io.NopCloser(bytes.NewReader(payload))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload)), nil
	}
	r.ContentLength = int64(len(payload))
	return r
}

// deadTargetAttempt simulates the forwarding bridge losing its peer: it arms
// the stale signal and writes the proxy's own 502, exactly as the real
// forwarder does.
func deadTargetAttempt(w http.ResponseWriter, r *http.Request, _ Target) {
	markStaleTarget(r.Context())
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte("bad gateway"))
}

func okAttempt(body string) retryAttempt {
	return func(w http.ResponseWriter, _ *http.Request, _ Target) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

func TestRetryReplaysTransportFailureAgainstSibling(t *testing.T) {
	rec := httptest.NewRecorder()
	r := replayableRequest(http.MethodGet, "")
	obs := &recordingObserver{}

	var seen []string
	attempt := func(w http.ResponseWriter, req *http.Request, target Target) {
		seen = append(seen, target.InstanceID)
		if target.InstanceID == "dead" {
			deadTargetAttempt(w, req, target)
			return
		}
		okAttempt("served by sibling")(w, req, target)
	}
	repick := func() (Target, bool) { return Target{InstanceID: "healthy"}, true }

	runWithRetry(rec, r, Target{InstanceID: "dead"}, testPolicy(),
		func(Target) {}, attempt, repick, obs)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the replay should have served the request", rec.Code)
	}
	if got := rec.Body.String(); got != "served by sibling" {
		t.Fatalf("body = %q, want the sibling's response", got)
	}
	if len(seen) != 2 || seen[0] != "dead" || seen[1] != "healthy" {
		t.Fatalf("targets = %v, want [dead healthy]", seen)
	}
}

// Rule 2, the load-bearing one: an application answer is never replayed, even
// a 5xx one, because the guest has already run the customer's side effects.
func TestRetryNeverReplaysApplicationErrors(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusBadRequest} {
		rec := httptest.NewRecorder()
		r := replayableRequest(http.MethodGet, "")
		calls := 0
		attempt := func(w http.ResponseWriter, _ *http.Request, _ Target) {
			calls++
			w.WriteHeader(status)
			_, _ = w.Write([]byte("app said so"))
		}
		repick := func() (Target, bool) {
			t.Fatalf("repick called for an application %d — rule 2 violated", status)
			return Target{}, false
		}

		runWithRetry(rec, r, Target{InstanceID: "a"}, testPolicy(),
			func(Target) {}, attempt, repick, &recordingObserver{})

		if calls != 1 {
			t.Fatalf("status %d: attempts = %d, want 1", status, calls)
		}
		if rec.Code != status {
			t.Fatalf("status = %d, want %d passed through untouched", rec.Code, status)
		}
	}
}

// Rule 3: POST is not replayed unless the customer explicitly opted in.
func TestRetrySkipsNonIdempotentByDefault(t *testing.T) {
	rec := httptest.NewRecorder()
	r := replayableRequest(http.MethodPost, `{"charge":1}`)
	obs := &recordingObserver{}
	calls := 0
	attempt := func(w http.ResponseWriter, req *http.Request, target Target) {
		calls++
		deadTargetAttempt(w, req, target)
	}

	runWithRetry(rec, r, Target{InstanceID: "dead"}, testPolicy(),
		func(Target) {}, attempt, func() (Target, bool) { return Target{}, true }, obs)

	if calls != 1 {
		t.Fatalf("attempts = %d, want 1 — POST must not replay by default", calls)
	}
	if len(obs.exhausted) == 0 || obs.exhausted[0] != RetrySkipNonIdempotent {
		t.Fatalf("exhausted = %v, want %q", obs.exhausted, RetrySkipNonIdempotent)
	}
}

func TestRetryReplaysPostWhenExplicitlyAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	r := replayableRequest(http.MethodPost, `{"charge":1}`)
	policy := testPolicy()
	policy.AllowNonIdempotent = true

	calls := 0
	var bodies []string
	attempt := func(w http.ResponseWriter, req *http.Request, target Target) {
		calls++
		b, _ := io.ReadAll(req.Body)
		bodies = append(bodies, string(b))
		if calls == 1 {
			deadTargetAttempt(w, req, target)
			return
		}
		okAttempt("ok")(w, req, target)
	}

	runWithRetry(rec, r, Target{InstanceID: "dead"}, policy,
		func(Target) {}, attempt, func() (Target, bool) { return Target{InstanceID: "b"}, true },
		&recordingObserver{})

	if calls != 2 {
		t.Fatalf("attempts = %d, want 2", calls)
	}
	// The replay must carry the full body, not a drained reader. This is the
	// regression the GetBody enabler exists to prevent.
	if len(bodies) != 2 || bodies[0] != bodies[1] || bodies[1] != `{"charge":1}` {
		t.Fatalf("bodies = %q, want the same full body on both attempts", bodies)
	}
}

// Rule 4: with no healthy sibling the original failure is returned as-is.
func TestRetryReturnsOriginalFailureWhenNoSibling(t *testing.T) {
	rec := httptest.NewRecorder()
	r := replayableRequest(http.MethodGet, "")
	obs := &recordingObserver{}

	runWithRetry(rec, r, Target{InstanceID: "dead"}, testPolicy(),
		func(Target) {}, deadTargetAttempt,
		func() (Target, bool) { return Target{}, false }, obs)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want the original 502 to reach the client", rec.Code)
	}
	if got := rec.Body.String(); got != "bad gateway" {
		t.Fatalf("body = %q, want the original error body", got)
	}
	if len(obs.exhausted) == 0 || obs.exhausted[0] != RetrySkipNoTarget {
		t.Fatalf("exhausted = %v, want %q", obs.exhausted, RetrySkipNoTarget)
	}
}

// Rule 5: a replay borrows from the customer's deadline and may never extend
// it. With less than MinRemaining left, the 502 is returned rather than
// converted into a 504.
func TestRetrySkippedWhenBudgetNearlyExhausted(t *testing.T) {
	rec := httptest.NewRecorder()
	r := replayableRequest(http.MethodGet, "")
	obs := &recordingObserver{}

	// A 3s budget started 2.9s ago leaves 100ms, under the 250ms floor.
	budget := reqbudget.Budget{
		Total:   3 * time.Second,
		Started: time.Now().Add(-2900 * time.Millisecond),
		Route:   "forward",
	}
	r = r.WithContext(reqbudget.NewContext(r.Context(), budget))

	calls := 0
	attempt := func(w http.ResponseWriter, req *http.Request, target Target) {
		calls++
		deadTargetAttempt(w, req, target)
	}
	runWithRetry(rec, r, Target{InstanceID: "dead"}, testPolicy(),
		func(Target) {}, attempt, func() (Target, bool) { return Target{InstanceID: "b"}, true }, obs)

	if calls != 1 {
		t.Fatalf("attempts = %d, want 1 — a replay must not outlive the customer's budget", calls)
	}
	if len(obs.exhausted) == 0 || obs.exhausted[0] != RetrySkipBudget {
		t.Fatalf("exhausted = %v, want %q", obs.exhausted, RetrySkipBudget)
	}
}

func TestRetryProceedsWithAmpleBudget(t *testing.T) {
	rec := httptest.NewRecorder()
	r := replayableRequest(http.MethodGet, "")
	budget := reqbudget.Budget{
		Total:   30 * time.Second,
		Started: time.Now(),
		Route:   "forward",
	}
	r = r.WithContext(reqbudget.NewContext(r.Context(), budget))

	calls := 0
	attempt := func(w http.ResponseWriter, req *http.Request, target Target) {
		calls++
		if calls == 1 {
			deadTargetAttempt(w, req, target)
			return
		}
		okAttempt("ok")(w, req, target)
	}
	runWithRetry(rec, r, Target{InstanceID: "dead"}, testPolicy(),
		func(Target) {}, attempt, func() (Target, bool) { return Target{InstanceID: "b"}, true },
		&recordingObserver{})

	if calls != 2 || rec.Code != http.StatusOK {
		t.Fatalf("attempts = %d status = %d, want 2 and 200", calls, rec.Code)
	}
}

// Rule 1: once bytes have reached the client the request cannot be taken back,
// even if the transport dies mid-stream.
func TestRetryNotAttemptedAfterResponseCommitted(t *testing.T) {
	rec := httptest.NewRecorder()
	r := replayableRequest(http.MethodGet, "")
	obs := &recordingObserver{}

	calls := 0
	attempt := func(w http.ResponseWriter, req *http.Request, _ Target) {
		calls++
		// A streaming response: headers out, some bytes flushed, then the
		// peer dies.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		markStaleTarget(req.Context())
	}
	runWithRetry(rec, r, Target{InstanceID: "dead"}, testPolicy(),
		func(Target) {}, attempt, func() (Target, bool) { return Target{InstanceID: "b"}, true }, obs)

	if calls != 1 {
		t.Fatalf("attempts = %d, want 1 — a committed response must not be replayed", calls)
	}
	if len(obs.exhausted) == 0 || obs.exhausted[0] != RetrySkipCommitted {
		t.Fatalf("exhausted = %v, want %q", obs.exhausted, RetrySkipCommitted)
	}
}

// Rule 6.
func TestRetryStopsAtMaxAttempts(t *testing.T) {
	rec := httptest.NewRecorder()
	r := replayableRequest(http.MethodGet, "")
	obs := &recordingObserver{}
	policy := testPolicy()
	policy.MaxAttempts = 3

	calls := 0
	attempt := func(w http.ResponseWriter, req *http.Request, target Target) {
		calls++
		deadTargetAttempt(w, req, target)
	}
	runWithRetry(rec, r, Target{InstanceID: "dead"}, policy,
		func(Target) {}, attempt, func() (Target, bool) { return Target{InstanceID: "b"}, true }, obs)

	if calls != 3 {
		t.Fatalf("attempts = %d, want 3 (MaxAttempts counts attempts, not retries)", calls)
	}
	if len(obs.exhausted) == 0 || obs.exhausted[len(obs.exhausted)-1] != RetrySkipAttempts {
		t.Fatalf("exhausted = %v, want to end with %q", obs.exhausted, RetrySkipAttempts)
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want the last 502 to reach the client", rec.Code)
	}
}

// The flag-off equivalence claim: a disabled policy must call the attempt
// exactly once, with the caller's own writer, and leave the response byte for
// byte as the pre-ADR-201 path produced it.
func TestRetryDisabledIsPassThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	r := replayableRequest(http.MethodGet, "")

	calls := 0
	var gotWriter http.ResponseWriter
	attempt := func(w http.ResponseWriter, req *http.Request, target Target) {
		calls++
		gotWriter = w
		deadTargetAttempt(w, req, target)
	}
	runWithRetry(rec, r, Target{InstanceID: "dead"}, RetryPolicy{},
		func(Target) {}, attempt, func() (Target, bool) { return Target{}, true }, nil)

	if calls != 1 {
		t.Fatalf("attempts = %d, want 1 when retry is disabled", calls)
	}
	if gotWriter != http.ResponseWriter(rec) {
		t.Fatal("disabled retry must hand the attempt the caller's own writer, unwrapped")
	}
	if rec.Code != http.StatusBadGateway || rec.Body.String() != "bad gateway" {
		t.Fatalf("status = %d body = %q, want the untouched 502", rec.Code, rec.Body.String())
	}
}

// A non-replayable body (admission was skipped, so GetBody is nil) must fall
// through to a single attempt rather than forwarding a drained reader.
func TestRetrySkippedWhenBodyNotReplayable(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "http://app.example/x", strings.NewReader("payload"))
	r.GetBody = nil
	obs := &recordingObserver{}

	calls := 0
	attempt := func(w http.ResponseWriter, req *http.Request, target Target) {
		calls++
		deadTargetAttempt(w, req, target)
	}
	runWithRetry(rec, r, Target{InstanceID: "dead"}, testPolicy(),
		func(Target) {}, attempt, func() (Target, bool) { return Target{InstanceID: "b"}, true }, obs)

	if calls != 1 {
		t.Fatalf("attempts = %d, want 1 for a non-replayable body", calls)
	}
	if len(obs.exhausted) == 0 || obs.exhausted[0] != RetrySkipBodyNotReplay {
		t.Fatalf("exhausted = %v, want %q", obs.exhausted, RetrySkipBodyNotReplay)
	}
}

// The failed target must be reported for eviction/breaker accounting, and the
// report must name the instance that actually failed — not the one picked
// after it.
func TestRetryReportsEachFailedTarget(t *testing.T) {
	rec := httptest.NewRecorder()
	r := replayableRequest(http.MethodGet, "")
	policy := testPolicy()
	policy.MaxAttempts = 3

	var reported []string
	targets := []string{"b", "c"}
	idx := 0
	repick := func() (Target, bool) {
		tgt := Target{InstanceID: targets[idx]}
		idx++
		return tgt, true
	}
	runWithRetry(rec, r, Target{InstanceID: "a"}, policy,
		func(tgt Target) { reported = append(reported, tgt.InstanceID) },
		deadTargetAttempt, repick, &recordingObserver{})

	if len(reported) != 3 || reported[0] != "a" || reported[1] != "b" || reported[2] != "c" {
		t.Fatalf("reported = %v, want [a b c] — each attempt must report its own target", reported)
	}
}
