package gateway

// Request retry against a different healthy instance (ADR-201 §1).
//
// The gateway is the only component that knows the *other* healthy instances
// of an app, so replaying a request that died in transport is work only the
// platform can do. Before this file the public path detected the dead target,
// evicted it from the route cache, kicked recovery — and then returned 502 to
// a client whose request could have been served by a sibling that was already
// running.
//
// The single rule that governs everything here: a retry may only follow a
// TRANSPORT failure, never an application answer. A guest that replies 500 has
// served the request; replaying it would run the customer's side effects
// twice. The arming signal is therefore `markStaleTarget` — set by the
// forwarding bridge on vmmd/netns death — and nothing else. Status codes are
// never consulted.

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// retryBufferLimit bounds how much of an in-flight response is held back
// while the outcome is still undecided. Only responses that have not
// committed are buffered, and a transport failure produces a small
// proxy-generated error body, so this never holds a real payload.
const retryBufferLimit = 64 * 1024

// Retry exhaustion reasons, reported on
// gateway_retry_exhausted_total{reason}. Each names the specific safety rule
// that stopped the replay so an operator can tell "we chose not to retry"
// apart from "we tried and ran out".
// A successful attempt — or any attempt that ended in an application answer
// rather than a transport failure — deliberately reports NO reason: it is not
// an exhaustion, it is the normal path, and counting it would drown the metric.
const (
	RetrySkipCommitted     = "response_committed"
	RetrySkipNonIdempotent = "non_idempotent_method"
	RetrySkipNoTarget      = "no_healthy_sibling"
	RetrySkipBudget        = "insufficient_budget"
	RetrySkipAttempts      = "max_attempts"
	RetrySkipBodyNotReplay = "body_not_replayable"
	RetrySkipIdempotency   = "missing_idempotency_key"
	RetrySkipAggregate     = "aggregate_budget"
)

// RetryPolicy is the compiled kind=retry rule. The zero value is disabled,
// which is what every request carries until a rule matches.
type RetryPolicy struct {
	Enabled bool
	// MaxAttempts counts total attempts, not retries: 2 means one original
	// plus one replay. Clamped to api.EdgeRuleRetryMaxAttempts at compile
	// time.
	MaxAttempts int
	// AllowNonIdempotent opts POST and PATCH into replay. Off by default
	// because a replayed POST runs the customer's side effect twice if their
	// handler is not idempotent.
	AllowNonIdempotent bool
	// MinRemaining is the floor of request budget below which a replay is not
	// started. Without it a retry could be admitted with 5 ms left and simply
	// convert a 502 into a 504.
	MinRemaining time.Duration
	// Backoff delays a replay. Defaults to zero: the failure being retried is
	// a dead peer, and the next instance is a different process, so waiting
	// buys nothing. Bounded for the case where the sibling is still waking.
	Backoff time.Duration
	// BudgetPercent caps aggregate retries as a percentage of original
	// requests in a short per-app window. BudgetMinRetries permits recovery
	// from a single dead peer even at low traffic.
	BudgetPercent    int
	BudgetMinRetries int
}

// retryable reports whether the policy permits replaying this request.
func (p RetryPolicy) retryable(r *http.Request) (bool, string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions,
		http.MethodTrace, http.MethodPut, http.MethodDelete:
		return true, ""
	case http.MethodPost, http.MethodPatch:
		if !p.AllowNonIdempotent {
			return false, RetrySkipNonIdempotent
		}
		if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
			return false, RetrySkipIdempotency
		}
		return true, ""
	default:
		return false, RetrySkipNonIdempotent
	}
}

// retryWriter holds a response back until its outcome is known.
//
// It mirrors serviceProxyResponseWriter's contract deliberately: anything
// below 500 commits immediately, so a normal response streams through with no
// added latency and no added memory, and only a proxy-generated error is held
// where a replay might still replace it. A Flush always commits — a caller
// that has begun streaming has, by definition, committed.
type retryWriter struct {
	dst       http.ResponseWriter
	header    http.Header
	status    int
	committed bool
	buffer    bytes.Buffer
}

func newRetryWriter(dst http.ResponseWriter) *retryWriter {
	return &retryWriter{dst: dst, header: make(http.Header)}
}

func (w *retryWriter) Header() http.Header { return w.header }

func (w *retryWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if status < http.StatusInternalServerError {
		w.commit()
	}
}

func (w *retryWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.committed {
		return w.dst.Write(p)
	}
	if w.buffer.Len()+len(p) > retryBufferLimit {
		w.commit()
		return w.dst.Write(p)
	}
	return w.buffer.Write(p)
}

func (w *retryWriter) Flush() {
	w.commit()
	if flusher, ok := w.dst.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *retryWriter) Unwrap() http.ResponseWriter { return w.dst }

// commit releases the buffered status, headers and body to the client. After
// this the attempt is irreversible and no replay may follow.
func (w *retryWriter) commit() {
	if w.committed {
		return
	}
	w.committed = true
	for key, values := range w.header {
		w.dst.Header()[key] = append([]string(nil), values...)
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.dst.WriteHeader(w.status)
	if w.buffer.Len() > 0 {
		_, _ = w.dst.Write(w.buffer.Bytes())
		w.buffer.Reset()
	}
}

// discard drops a buffered attempt so a replay can produce the real response.
// Only legal while uncommitted.
func (w *retryWriter) discard() {
	w.buffer.Reset()
	w.status = 0
	w.header = make(http.Header)
}

// retryAttempt runs one proxy attempt against target.
type retryAttempt func(w http.ResponseWriter, r *http.Request, target Target)

// retryRepick selects a different target after the previous one proved dead.
// It returns false when no healthy sibling exists, which ends the loop.
type retryRepick func() (Target, bool)

// retryObserver reports attempts and exhaustion for the metric surface.
type retryObserver interface {
	IncRetryAttempt(outcome string)
	IncRetryExhausted(reason string)
}

// runWithRetry executes attempt against target, replaying against a fresh
// target when the bridge reports a transport failure and every ADR-201 §1
// safety rule holds.
//
// When the policy is disabled this calls attempt exactly once with the caller's
// own writer and request, which is byte-identical to the pre-ADR-201 path —
// no buffering, no body rewind, no wrapper in the way of the streaming and
// upgrade paths.
func runWithRetry(
	w http.ResponseWriter,
	r *http.Request,
	target Target,
	policy RetryPolicy,
	onStale func(Target),
	attempt retryAttempt,
	repick retryRepick,
	obs retryObserver,
	admissions ...retryBudgetAdmission,
) {
	var admission retryBudgetAdmission
	if len(admissions) > 0 {
		admission = admissions[0]
	}
	retryable, skipReason := policy.retryable(r)
	if !policy.Enabled || policy.MaxAttempts < 2 || !retryable {
		if policy.Enabled && obs != nil && !retryable {
			obs.IncRetryExhausted(skipReason)
		}
		attempt(w, r, target)
		return
	}
	// A request whose body was admitted must be rewindable, or a replay would
	// forward an already-drained reader and the sibling would see an empty
	// body. Bodyless requests need no rewind and are always replayable.
	hasBody := r.Body != nil && r.Body != http.NoBody
	if hasBody && r.GetBody == nil {
		if obs != nil {
			obs.IncRetryExhausted(RetrySkipBodyNotReplay)
		}
		attempt(w, r, target)
		return
	}
	// The admitted body owns a spool file; GetBody hands out non-owning
	// readers so one attempt's Close cannot delete the file the next attempt
	// needs. Ownership is held here and released exactly once.
	if hasBody {
		owner := r.Body
		defer func() { _ = owner.Close() }()
	}
	admission.budget.ObserveOriginal(admission.scope)
	runAttempts(w, r, target, policy, onStale, attempt, repick, obs, admission)
}

// runAttempts is the loop proper, split out so runWithRetry stays inside the
// repo's handler-length convention.
func runAttempts(
	w http.ResponseWriter,
	r *http.Request,
	target Target,
	policy RetryPolicy,
	onStale func(Target),
	attempt retryAttempt,
	repick retryRepick,
	obs retryObserver,
	admission retryBudgetAdmission,
) {
	for i := 0; i < policy.MaxAttempts; i++ {
		if i > 0 && policy.Backoff > 0 {
			timer := time.NewTimer(policy.Backoff)
			select {
			case <-timer.C:
			case <-r.Context().Done():
				timer.Stop()
				return
			}
			timer.Stop()
		}
		req, err := replayRequest(r)
		if err != nil {
			if obs != nil {
				obs.IncRetryExhausted(RetrySkipBodyNotReplay)
			}
			return
		}
		failed := target
		signal := &staleTargetSignal{onStale: func() { onStale(failed) }}
		//nolint:contextcheck // the stale signal deliberately inherits the inbound request context.
		req = req.WithContext(withStaleTargetSignal(req.Context(), signal))

		buf := newRetryWriter(w)
		attempt(buf, req, target)
		if obs != nil {
			obs.IncRetryAttempt(attemptOutcome(i, signal.stale.Load()))
		}

		reason, ok := nextAttemptAllowed(req, buf, signal, policy, i)
		if !ok {
			if obs != nil && reason != "" {
				obs.IncRetryExhausted(reason)
			}
			buf.commit()
			return
		}
		next, found := repick()
		if !found {
			if obs != nil {
				obs.IncRetryExhausted(RetrySkipNoTarget)
			}
			buf.commit()
			return
		}
		percent := policy.BudgetPercent
		if percent <= 0 {
			percent = api.EdgeRuleRetryDefaultBudgetPercent
		}
		minRetries := policy.BudgetMinRetries
		if minRetries <= 0 {
			minRetries = api.EdgeRuleRetryDefaultBudgetMin
		}
		if admission.budget != nil && !admission.budget.AllowRetry(admission.scope, percent, minRetries) {
			if obs != nil {
				obs.IncRetryExhausted(RetrySkipAggregate)
			}
			buf.commit()
			return
		}
		buf.discard()
		target = next
	}
}

// nextAttemptAllowed applies the ADR-201 §1 safety rules in order. It returns
// the exhaustion reason when a replay is refused; an empty reason means the
// attempt simply succeeded and no metric should fire.
func nextAttemptAllowed(
	r *http.Request,
	buf *retryWriter,
	signal *staleTargetSignal,
	policy RetryPolicy,
	attempt int,
) (string, bool) {
	// Rule 2 first: an application answer is not retryable at any price, and
	// a success must not emit an exhaustion metric.
	if !signal.stale.Load() {
		return "", false
	}
	// Rule 1: a response that reached the client cannot be taken back.
	if buf.committed {
		return RetrySkipCommitted, false
	}
	// Rule 6.
	if attempt+1 >= policy.MaxAttempts {
		return RetrySkipAttempts, false
	}
	// Rule 5: a replay borrows from the customer's own deadline and may never
	// extend it. No budget attached means no budget rule matched, so there is
	// nothing to overrun.
	if budget, ok := reqbudget.FromContext(r.Context()); ok {
		if budget.Remaining(time.Now()) < policy.MinRemaining {
			return RetrySkipBudget, false
		}
	}
	return "", true
}

// replayRequest returns the request to send for this attempt, rewinding the
// admitted body when there is one. The returned request shares everything else
// with the original.
func replayRequest(r *http.Request) (*http.Request, error) {
	if r.Body == nil || r.Body == http.NoBody || r.GetBody == nil {
		return r, nil
	}
	body, err := r.GetBody()
	if err != nil {
		return nil, err
	}
	clone := r.Clone(r.Context())
	clone.Body = body
	return clone, nil
}

func attemptOutcome(index int, stale bool) string {
	switch {
	case stale && index == 0:
		return "original_failed"
	case stale:
		return "replay_failed"
	case index == 0:
		return "original_ok"
	default:
		return "replay_ok"
	}
}

// WithRetryEnabled sets the FAAS_GATEWAY_RETRY operator gate.
func (h *Handler) WithRetryEnabled(enabled bool) *Handler {
	h.retryEnabled = enabled
	return h
}

// WithRetryDefault sets the policy used when the gate is on and no kind=retry
// rule matched.
func (h *Handler) WithRetryDefault(p RetryPolicy) *Handler {
	h.retryDefault = p
	return h
}

// WithRetryMatcher installs the kind=retry rule resolver.
func (h *Handler) WithRetryMatcher(fn func(app App, r *http.Request) (RetryPolicy, bool)) *Handler {
	h.retryMatch = fn
	return h
}

// WithRetryObserver installs the retry metric sink.
func (h *Handler) WithRetryObserver(obs retryObserver) *Handler {
	h.retryObs = obs
	return h
}

// WithRetryBudget installs the aggregate retry-amplification limiter. The
// same instance can be shared with the internal service proxy.
func (h *Handler) WithRetryBudget(budget *RetryBudget) *Handler {
	h.retryBudget = budget
	return h
}

// retryPolicyFor resolves the effective policy for one request.
//
// Precedence: an injected test matcher, then a matched kind=retry edge rule,
// then the operator default. All three are inert until the gate is on, so
// FAAS_GATEWAY_RETRY remains a single kill switch regardless of what rules
// customers have stored.
func (h *Handler) retryPolicyFor(app App, r *http.Request) RetryPolicy {
	if !h.retryEnabled {
		return RetryPolicy{}
	}
	if h.retryMatch != nil {
		if policy, ok := h.retryMatch(app, r); ok {
			policy.Enabled = true
			return policy
		}
	}
	if h.edgeRules != nil {
		if rule := h.edgeRules.MatchRetry(r.Context(), hostname(r.Host), r.URL.Path, r.Method); rule != nil {
			return rule.Policy()
		}
	}
	policy := h.retryDefault
	policy.Enabled = policy.MaxAttempts >= 2
	return policy
}

// proxyAttempt runs the forwarder for one request, replaying against a fresh
// target when ADR-201 §1 permits.
//
// Streaming is excluded outright rather than left to rule 1. A streaming
// response commits on its first flush, so a replay is impossible by
// construction — but routing it through the retry writer would put a
// buffering ResponseWriter in front of the one path whose entire purpose is
// not to buffer, and the plan's per-flush write deadline is measured against
// that writer.
func (h *Handler) proxyAttempt(
	w http.ResponseWriter,
	r *http.Request,
	target Target,
	isStreaming bool,
	retire func(Target),
	forward retryAttempt,
	app App,
) {
	if isStreaming {
		forward(w, r, target)
		return
	}
	policy := h.retryPolicyFor(app, r)
	if !policy.Enabled {
		forward(w, r, target)
		return
	}
	repick := func() (Target, bool) {
		pick := h.backend.Pick(app.ID)
		if !pick.OK {
			return Target{}, false
		}
		// The failed instance was evicted synchronously by retire(), so the
		// picker cannot hand it back. Guard anyway: a backend that does not
		// implement EvictInstance would otherwise replay onto the same dead
		// target and burn the attempt budget for nothing.
		if pick.Target.InstanceID == target.InstanceID {
			return Target{}, false
		}
		return pick.Target, true
	}
	obs := h.retryObs
	if obs == nil && h.metrics != nil {
		// Default to the daemon's own registry rather than silently
		// dropping the counters. WithRetryObserver stays available for
		// tests that need to assert on the sequence.
		obs = h.metrics
	}
	runWithRetry(w, r, target, policy, retire, forward, repick, obs,
		retryBudgetAdmission{budget: h.retryBudget, scope: app.ID})
}
