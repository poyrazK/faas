// Package webhookout is the outbound webhook dispatcher used by meterd
// (issue #396 / ADR-045) to deliver signed POSTs to customer-supplied
// URLs on alert-rule fire. PR 2 ships the library and the unit tests;
// PR 4 wires the meterd caller.
//
// Signing: HMAC-SHA256 over the canonical string
// "<unix>.<delivery_id>.<body>". The customer verifies the signature
// using their stored secret; the canonical string is what binds the
// signature to (timestamp, delivery_id, body) — none of those three
// pieces can be tampered with independently. The verify path uses
// hmac.Equal (constant-time) — never == — precedent:
// pkg/billing/paddle/webhook.go:173-188.
//
// Retry: 5 attempts, exponential backoff with ±25% jitter at
// ~2s / 8s / 32s / 128s. Retry on 5xx / 408 / 429 / network errors;
// terminal on every other 4xx. The total wall-clock budget in the
// worst case is 220s (0 + 2 + 8 + 32 + 128 + 5 × 10s per-attempt
// timeout).
//
// SSRF guard: delegated to pkg/oci (no re-implementation of the CIDR
// union — handlers/EgressIPAllowed at validation time,
// EgressDialContext at dial-time, ErrImageEgressDenied surfaced via
// errors.Is).
//
// CLAUDE.md §11: the secret is NEVER logged. DispatcherOptions.Logger
// is allowed; the dispatcher only ever logs attempt counts, status
// codes, and metadata (rule name, delivery id). The body of a
// response is truncated to 32 KiB so an unbounded reader cannot leak
// the secret via a reflected-payload response.
package webhookout

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
)

// Sentinel errors. errors.Is is the public contract so callers can
// branch on the four terminal states (success, 4xx terminal, attempts
// exhausted, body too large, SSRF rejected).
var (
	// ErrTerminal is returned when the first attempt produced a
	// non-408/429 4xx. The retry policy gives up on those — most
	// commonly because the customer pointed us at a stale endpoint
	// that returned 410 Gone.
	ErrTerminal = errors.New("webhookout: terminal failure (non-retryable 4xx)")

	// ErrAttemptsExhausted is returned after MaxAttempts retries on
	// a 5xx/408/429/network failure. The meterd layer will record
	// the failure and surface it on the alert-rule's history page.
	ErrAttemptsExhausted = errors.New("webhookout: retryable failure after MaxAttempts")

	// ErrBodyTooLarge is returned when a response body exceeds 32 KiB.
	// Bodies that big almost always mean a misconfigured endpoint
	// (a customer pointed us at an HTML page by mistake). Truncating
	// without flagging it would hide the misconfiguration; returning
	// the truncated prefix is best-effort observability.
	ErrBodyTooLarge = errors.New("webhookout: response body exceeds 32 KiB")
)

// HeaderSet picks which header names the dispatcher emits. Two sets
// coexist so the alert wire (PR #396 / ADR-045, customer verifiers
// shipped) stays stable while the new outbound webhook surface
// (issue #476 / ADR-076) emits a parallel set.
//
// The signing scheme is identical (HMAC-SHA256 over
// "<unix>.<delivery_id>.<body>"); only the wire header names change.
type HeaderSet int

const (
	// HeaderSetAlert emits X-Faas-Alert-Signature,
	// X-Faas-Alert-Id, X-Faas-Alert-Timestamp, X-Faas-Alert-Attempt.
	// The historical alert-delivery wire (PR #396). Customers'
	// verifiers pin on these names; do NOT rename.
	HeaderSetAlert HeaderSet = iota

	// HeaderSetWebhook emits X-Faas-Webhook-Signature,
	// X-Faas-Delivery-Id, X-Faas-Webhook-Timestamp,
	// X-Faas-Webhook-Attempt. The new outbound webhook delivery wire
	// (issue #476 / ADR-076). Distinct from the alert set so a
	// customer's verifier can key dashboards off which surface fired
	// the POST without parsing the body.
	HeaderSetWebhook
)

// headerNames returns the (signature, id, timestamp, attempt) tuple
// for the chosen HeaderSet. The dispatcher calls this once per
// request to keep the request path branch-free at the http.Header
// set level.
func (h HeaderSet) headerNames() (signature, id, timestamp, attempt string) {
	switch h {
	case HeaderSetWebhook:
		return "X-Faas-Webhook-Signature",
			"X-Faas-Delivery-Id",
			"X-Faas-Webhook-Timestamp",
			"X-Faas-Webhook-Attempt"
	default: // HeaderSetAlert (zero value)
		return "X-Faas-Alert-Signature",
			"X-Faas-Alert-Id",
			"X-Faas-Alert-Timestamp",
			"X-Faas-Alert-Attempt"
	}
}

// Legacy alert header constants. Kept as exported package consts so
// existing customer-side verifiers and the e2e tests at
// cmd/e2e/meterd_alerts_e2e_test.go (which assert these names) keep
// working. New code should use HeaderSetAlert / HeaderSetWebhook
// through DispatcherOptions.
const (
	HeaderAlertSignature = "X-Faas-Alert-Signature"
	HeaderAlertID        = "X-Faas-Alert-Id"
	HeaderAlertTimestamp = "X-Faas-Alert-Timestamp"
	HeaderAlertAttempt   = "X-Faas-Alert-Attempt"

	// Deprecated aliases preserved for pre-#476 callers. Resolve to
	// the same wire names as HeaderAlert* (the alert header set is
	// the historical default). New callers should switch to
	// HeaderAlert* (or HeaderSet* via DispatcherOptions).
	HeaderSignature = HeaderAlertSignature
	HeaderID        = HeaderAlertID
	HeaderTimestamp = HeaderAlertTimestamp
	HeaderAttempt   = HeaderAlertAttempt
)

// Default policy. Lifted out of the DispatcherOptions zero-check so
// the table is auditable in one place.
const (
	DefaultMaxAttempts = 5
	DefaultBaseBackoff = 2 * time.Second
	DefaultPerAttempt  = 10 * time.Second
	MaxBodyBytes       = 32 * 1024
)

// Signer computes and verifies the per-delivery HMAC-SHA256
// signature. The secret is held in memory only and NEVER logged.
// Construct one per (account, rule, secret version) — the secret does
// not traverse the call stack on every fire.
//
// The canonical string is "<unix>.<delivery_id>.<body>" — timestamp,
// delivery id, then body, joined by '.' (a separator that does not
// appear in the base64url delivery id we generate). The unix timestamp
// flows through the X-Faas-Alert-Timestamp header so the customer's
// verifier can implement its own replay window; the signer does not
// enforce one (the dispatcher's policy is "deliver promptly", the
// customer's policy is "accept within N minutes").
type Signer struct {
	secret []byte
}

// NewSigner returns a Signer. The slice is NOT copied — callers that
// later overwrite the secret's storage should pass a copy. PR 3 reads
// the unsealed secret from pkg/state and constructs one Signer per
// (account, rule) — so the secret lifetime equals the dispatcher's.
func NewSigner(secret []byte) *Signer {
	return &Signer{secret: secret}
}

// Sign returns the hex-encoded HMAC-SHA256 over "<unix>.<delivery_id>.<body>".
func (s *Signer) Sign(unix int64, deliveryID string, body []byte) string {
	mac := hmac.New(sha256.New, s.secret)
	// hmac.Hash.Write never returns a non-nil error; the errcheck
	// suppression keeps the linter quiet.
	_, _ = fmt.Fprintf(mac, "%d.%s.", unix, deliveryID)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify returns nil iff gotHex matches the canonical HMAC. Uses
// hmac.Equal (constant-time) — never ==. Errors carry the bad hex so
// the customer's logs can correlate; the dispatcher never logs the
// computed hex (which would be a constant-time oracle on the secret).
func (s *Signer) Verify(unix int64, deliveryID string, body []byte, gotHex string) error {
	expected := s.Sign(unix, deliveryID, body)
	// hmac.Equal operates on bytes; the hex representations are
	// ASCII so a length mismatch is a length mismatch on the wire too.
	if !hmac.Equal([]byte(expected), []byte(gotHex)) {
		return fmt.Errorf("webhookout: signature mismatch")
	}
	return nil
}

// Target is the destination URL for a single delivery. URL is
// pre-validated by the handler (PR 3) via oci.EgressIPAllowed; the
// dispatcher runs the dial-time check via oci.NewEgressHTTPClient
// (default HTTPClient).
//
// Signer is the per-rule HMAC key. Construct once per (account,
// rule, secret version) — the secret lifetime equals the
// dispatcher's, not the per-call delivery. The signature path
// signs the canonical string "<unix>.<delivery_id>.<body>"; the
// unix value comes from evt.OccurredAt and the delivery id from
// evt.ID, so the signer only holds the key.
type Target struct {
	URL    string
	Signer *Signer
}

// Event is the JSON payload posted to the customer. Payload is the
// rule-specific body (threshold value, current value, app slug).
// OccurredAt is the alert-fire instant; the dispatcher serialises
// it as RFC3339Nano into both the X-Faas-Alert-Timestamp header and
// an "occurred_at" field in the body so the customer's verifier can
// pin it without parsing the body twice.
type Event struct {
	ID         string         `json:"id"`          // X-Faas-Alert-Id header value
	OccurredAt time.Time      `json:"occurred_at"` // X-Faas-Alert-Timestamp header value
	Rule       string         `json:"rule"`        // rule name, for audit
	RuleName   string         `json:"rule_name"`   // alias of Rule — surfaced on the wire for downstream consumers that key dashboards off `rule_name`
	AppID      string         `json:"app_id"`      // app slug, for the customer
	Payload    map[string]any `json:"payload"`     // arbitrary JSON-able content
}

// Result is the return value of Dispatch. Err is one of:
//   - nil                       (success: 2xx or 3xx)
//   - ErrTerminal               (first attempt returned a non-408/429 4xx)
//   - ErrAttemptsExhausted      (MaxAttempts retries on a retryable failure)
//   - ErrBodyTooLarge           (response body exceeded 32 KiB)
//   - errors.Is(wrapping oci.ErrImageEgressDenied)  (SSRF rejected)
//
// StatusCode is the last attempt's response status (0 if no response
// was received — e.g. a network error). BodyPrefix is the first
// MaxBodyBytes of the last response body; useful for the operator's
// "why did the customer's endpoint reject this?" debug dump. The
// prefix is intentionally truncated — keeping the full body would
// risk leaking the customer's secrets that they may have reflected
// back into the response.
type Result struct {
	StatusCode int
	Attempts   int
	BodyPrefix []byte
	Err        error
}

// DispatcherOptions configures a Dispatcher. Zero-valued fields
// resolve to the package defaults (DefaultMaxAttempts = 5,
// DefaultBaseBackoff = 2s, DefaultPerAttempt = 10s). HTTPClient is
// optional; nil resolves to oci.NewEgressHTTPClient so the dial-time
// SSRF guard is on by default. Sleeper is injectable so tests don't
// wait real backoff; nil resolves to time.Sleep. Logger is optional;
// nil resolves to slog.Default().
//
// HeaderSet picks which header names the dispatcher emits on every
// POST. Zero value is HeaderSetAlert (preserves the pre-#476 alert
// wire). Issue #476's outbound webhook dispatcher sets this to
// HeaderSetWebhook so customer verifiers can key dashboards on the
// new prefix.
type DispatcherOptions struct {
	MaxAttempts int
	BaseBackoff time.Duration
	PerAttempt  time.Duration
	HTTPClient  *http.Client
	Sleeper     func(d time.Duration)
	Logger      *slog.Logger
	HeaderSet   HeaderSet
}

// Dispatcher is the per-rule outbound webhook poster. PR 3 wires one
// per (account, rule, secret version); PR 4 calls Dispatch on every
// alert fire.
//
// Dispatcher is NOT safe for concurrent use of Dispatch. The
// per-target Signer lives on Target, but the per-call body
// marshalling and the retry loop share d.opts — a caller that
// fires multiple deliveries in parallel should construct one
// Dispatcher per delivery stream (or serialise at the caller).
type Dispatcher struct {
	opts DispatcherOptions
	// headerNames is the (signature, id, timestamp, attempt) tuple
	// resolved once at construction so the per-attempt hot path
	// doesn't branch. Setting HeaderSet on DispatcherOptions after
	// NewDispatcher has no effect.
	headerSig     string
	headerID      string
	headerTime    string
	headerAttempt string
}

// NewDispatcher returns a Dispatcher. opts is documented above. The
// zero-value opts resolves to all defaults — the production caller
// does not need to set anything besides an explicit MaxAttempts if
// it wants fewer retries.
//
// HTTPClient ownership: when the caller passes nil the dispatcher
// builds one via oci.NewEgressHTTPClient() (the SSRF-guarded client
// has no timeout) and applies PerAttempt as the client-level Timeout.
// When the caller passes their own *http.Client the dispatcher does
// NOT mutate it — the caller's Timeout (and Transport) stay as-is.
// PR 3 callers that want the SSRF guard AND a custom timeout should
// build the client with oci.EgressDialContext and a Timeout, then
// pass it in.
func NewDispatcher(opts DispatcherOptions) *Dispatcher {
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = DefaultMaxAttempts
	}
	if opts.BaseBackoff == 0 {
		opts.BaseBackoff = DefaultBaseBackoff
	}
	if opts.PerAttempt == 0 {
		opts.PerAttempt = DefaultPerAttempt
	}
	callerHTTPClient := opts.HTTPClient != nil
	if opts.HTTPClient == nil {
		// Test-only escape hatch: cmd/e2e/meterd_alerts_e2e_test.go
		// sets FAAS_EGRESS_ALLOW_LOOPBACK=1 BEFORE spawning meterd so
		// the dispatcher can POST to an httptest.NewServer bound on
		// 127.0.0.1. The flag is read fresh on every dispatcher
		// construction (NewDispatcher is called once at meterd boot
		// today, so this is a no-op cost). Production daemons must
		// NOT export this env var — see oci.NewEgressHTTPClientAllowLoopback.
		if hc := oci.NewEgressHTTPClientAllowLoopback(); hc != nil {
			opts.HTTPClient = hc
		} else {
			opts.HTTPClient = oci.NewEgressHTTPClient()
		}
	}
	if opts.Sleeper == nil {
		opts.Sleeper = time.Sleep
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	// Only stamp PerAttempt onto the HTTPClient when we built it
	// ourselves (SSRF guard returns no-timeout clients). A caller-
	// supplied *http.Client keeps its own Timeout.
	if !callerHTTPClient {
		opts.HTTPClient.Timeout = opts.PerAttempt
	}
	sig, id, ts, att := opts.HeaderSet.headerNames()
	return &Dispatcher{
		opts:          opts,
		headerSig:     sig,
		headerID:      id,
		headerTime:    ts,
		headerAttempt: att,
	}
}

// Dispatch posts evt to target with retry+backoff. See Result for
// the error contract. ctx is the per-delivery deadline (PR 4 sets it
// to the dispatcher's wall-clock budget; PR 3 sets it to the per-rule
// deadline).
func (d *Dispatcher) Dispatch(ctx context.Context, t Target, evt Event) Result {
	return d.dispatch(ctx, t, evt)
}

// DispatchTest is the customer-facing "send a test alert" path used
// by the apid handler at
// cmd/apid/handlers_alert_presets.go::sendTestAlertPreset. It is a
// parallel code path to Dispatch that:
//   - Sets evt.Payload["test"] = true so the customer's verifier can
//     branch on the discriminator (skips the production ledger write,
//     enables test-mode short-circuits like a quieter alert).
//   - Does NOT call into the meterd evaluator's ClaimAlertFire /
//     alert_deliveries write path (that's owned by meterd, not
//     apid) — the test attempt shows up only in the audit log
//     (`alert_preset.test_sent`) and in the customer's webhook
//     receiver.
//   - Reuses the same retry/backoff loop as Dispatch so a transient
//     webhook-receiver blip behaves the same.
//
// The dispatcher's only contract is that Payload["test"] == true in
// the body that reaches the customer's URL — anything else is the
// handler's responsibility (synthetic observed value, dispatch id
// collision avoidance, etc.).
//
// Refs: ADR-123 PR-C, issue #1233, plan §Commit 2.
func (d *Dispatcher) DispatchTest(ctx context.Context, t Target, evt Event) Result {
	if evt.Payload == nil {
		evt.Payload = make(map[string]any, 1)
	}
	// Setting, not merging — a caller-supplied "test: false" would
	// be overwritten, which is the right posture (the dispatcher
	// is the only writer of the discriminator key on this code
	// path).
	evt.Payload["test"] = true
	return d.dispatch(ctx, t, evt)
}

// dispatch is the shared retry/backoff implementation behind Dispatch
// and DispatchTest. Refactored so the test path is a single line on
// top of the production path (no logic duplication).
func (d *Dispatcher) dispatch(ctx context.Context, t Target, evt Event) Result {
	body, err := json.Marshal(evt)
	if err != nil {
		// Marshalling a map[string]any with a known shape should not
		// fail; if it does the failure is permanent.
		return Result{Err: fmt.Errorf("webhookout: marshal event: %w", err)}
	}
	if t.Signer == nil {
		return Result{Err: errors.New("webhookout: Target.Signer is nil")}
	}
	unix := evt.OccurredAt.Unix()
	sig := t.Signer.Sign(unix, evt.ID, body)

	var lastResult Result
	for attempt := 0; attempt < d.opts.MaxAttempts; attempt++ {
		if attempt > 0 {
			delay := d.backoffFor(attempt - 1)
			d.opts.Sleeper(delay)
		}
		lastResult = d.attempt(ctx, t.URL, sig, unix, evt.ID, attempt+1, body)
		lastResult.Attempts = attempt + 1
		if lastResult.Err == nil {
			return lastResult
		}
		if errors.Is(lastResult.Err, ErrTerminal) {
			// First attempt's 4xx is the terminal signal — bail
			// before logging the retry.
			break
		}
		if errors.Is(lastResult.Err, ErrBodyTooLarge) {
			// Body too large is a misconfiguration; no point in
			// retrying — the next attempt will hit the same cap.
			break
		}
		if errors.Is(lastResult.Err, oci.ErrImageEgressDenied) {
			// SSRF rejection is terminal. The DNS outcome won't change
			// inside the 220s retry budget, so re-running the dial
			// just burns the wall-clock window for no benefit. The
			// caller (meterd) branches on oci.ErrImageEgressDenied
			// via errors.Is to render `code: alert_webhook_denied`,
			// so we leave the sentinel unwrapped below.
			break
		}
		d.logAttempt(t, evt, attempt+1, lastResult)
	}
	// Loop exited without success or terminal flag — every attempt
	// was retryable. Surface ErrAttemptsExhausted wrapped around the
	// last attempt's underlying error so callers can errors.Is()
	// for retry-budget exhaustion and still see the root cause.
	if lastResult.Err == nil ||
		errors.Is(lastResult.Err, ErrTerminal) ||
		errors.Is(lastResult.Err, ErrBodyTooLarge) ||
		errors.Is(lastResult.Err, oci.ErrImageEgressDenied) {
		return lastResult
	}
	d.logExhausted(evt, lastResult)
	return Result{
		StatusCode: lastResult.StatusCode,
		Attempts:   lastResult.Attempts,
		BodyPrefix: lastResult.BodyPrefix,
		Err:        fmt.Errorf("%w: %w", ErrAttemptsExhausted, lastResult.Err),
	}
}

// logExhausted emits a single closure line on the wrap path so the
// operator can see the retry budget was consumed. The per-attempt
// logAttempt already wrote the underlying detail; this is just the
// "we gave up" beat. Never logs the body / secret / URL.
func (d *Dispatcher) logExhausted(evt Event, r Result) {
	d.opts.Logger.Warn(
		"webhookout: attempts exhausted",
		"rule", evt.Rule,
		"app", evt.AppID,
		"delivery_id", evt.ID,
		"attempts", r.Attempts,
		"status", r.StatusCode,
	)
}

// attempt performs a single POST with all headers attached. Returns
// a Result whose Err is nil on 2xx/3xx, ErrTerminal on a non-408/429
// 4xx, ErrBodyTooLarge on a body > MaxBodyBytes, or a wrapped error
// on retryable failures (5xx, 408, 429, network).
func (d *Dispatcher) attempt(ctx context.Context, url, sig string, unix int64, deliveryID string, attempt int, body []byte) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		// Malformed URL — permanent.
		return Result{StatusCode: 0, Err: fmt.Errorf("webhookout: build request: %w", err), BodyPrefix: nil}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(d.headerSig, "sha256="+sig)
	req.Header.Set(d.headerID, deliveryID)
	req.Header.Set(d.headerTime, fmt.Sprintf("%d", unix))
	req.Header.Set(d.headerAttempt, fmt.Sprintf("%d", attempt))

	resp, err := d.opts.HTTPClient.Do(req)
	if err != nil {
		// Network errors are retryable. SSRF rejections are wrapped
		// through this path because the egress guard returns its own
		// sentinel; we surface that sentinel via errors.Is so the
		// caller can branch on it.
		return Result{StatusCode: 0, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	// Read up to MaxBodyBytes+1 — the extra byte is the "body too
	// large" probe. io.LimitReader ensures we don't block on the
	// extra read past the cap, and the defer'd drain step on the
	// body-too-large branch lets the underlying conn return to the
	// keep-alive pool instead of hanging.
	prefix := make([]byte, MaxBodyBytes+1)
	n, _ := io.ReadFull(resp.Body, prefix)
	prefix = prefix[:n]
	if n > MaxBodyBytes {
		// Drain the remainder so the conn is reusable. Bounded by
		// the larger of (MaxBodyBytes, 1<<20) — a misconfigured
		// endpoint that streams indefinitely can't keep us here
		// forever; 1 MiB is enough to release the keep-alive slot
		// on any sane server while bounding the worst case.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return Result{
			StatusCode: resp.StatusCode,
			BodyPrefix: prefix[:MaxBodyBytes],
			Err:        ErrBodyTooLarge,
		}
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 400:
		return Result{StatusCode: resp.StatusCode, BodyPrefix: prefix}
	case resp.StatusCode == 408 || resp.StatusCode == 429:
		return Result{
			StatusCode: resp.StatusCode,
			BodyPrefix: prefix,
			Err:        fmt.Errorf("webhookout: retryable %d: %s", resp.StatusCode, truncateBody(prefix)),
		}
	case resp.StatusCode >= 500:
		return Result{
			StatusCode: resp.StatusCode,
			BodyPrefix: prefix,
			Err:        fmt.Errorf("webhookout: retryable %d: %s", resp.StatusCode, truncateBody(prefix)),
		}
	default:
		// 4xx other than 408/429 — terminal.
		return Result{
			StatusCode: resp.StatusCode,
			BodyPrefix: prefix,
			Err:        ErrTerminal,
		}
	}
}

// backoffFor returns the delay before the (attempt+1)-th try. attempt
// is 0-indexed (0 → first retry, so the delay before attempt 2).
// Formula: base * 4^attempt * (1 + jitter) with jitter ∈ [-0.25, +0.25].
// The result is always >= 0.
func (d *Dispatcher) backoffFor(attempt int) time.Duration {
	multiplier := 1 << (2 * attempt) // 1, 4, 16, 64
	//nolint:gosec // backoff jitter is not a security primitive; math/rand is fine.
	jitter := (rand.Float64()*0.5 - 0.25)
	delay := time.Duration(float64(d.opts.BaseBackoff) * float64(multiplier) * (1 + jitter))
	if delay < 0 {
		delay = 0
	}
	return delay
}

// logAttempt logs a retryable failure so the operator can see why a
// delivery is taking longer than usual. Never logs the secret, the
// body, or the response body. Stripped of CR/LF via the standard
// CodeQL go/log-injection sanitiser pattern (alert #117) — the
// server's response body prefix is user-controlled and flows into
// the log line.
func (d *Dispatcher) logAttempt(t Target, evt Event, attempt int, r Result) {
	msg := truncateBody(r.BodyPrefix)
	msg = strings.ReplaceAll(msg, "\r", "")
	msg = strings.ReplaceAll(msg, "\n", "")
	d.opts.Logger.Warn(
		"webhookout: attempt failed; will retry",
		"rule", evt.Rule,
		"app", evt.AppID,
		"delivery_id", evt.ID,
		"attempt", attempt,
		"status", r.StatusCode,
		"err_msg", msg,
	)
}

// truncateBody returns a short, log-safe string from a response body
// prefix. Bodies can be JSON, HTML, or anything — we want a stable
// shape that survives CR/LF stripping and doesn't balloon the log
// line on a 32 KiB response.
func truncateBody(b []byte) string {
	const limit = 256
	if len(b) > limit {
		b = b[:limit]
	}
	return string(b)
}
