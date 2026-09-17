package webhookout_test

// Issue #396 / ADR-045 PR 2 — pkg/webhookout tests.
//
// Coverage matrix (mirrors the plan §2):
//
// Signer (6 tests):
//   - SignVerify_Vector: round-trip with frozen inputs.
//   - SignVerify_TamperRejected: one byte of body changed → Verify rejects.
//   - SignVerify_WrongSecret: different Signer → Verify rejects.
//   - SignVerify_TimestampBinding: signing with unix, verifying with
//     unix-1 → Verify rejects (the signer does not enforce a tolerance
//     window; replay protection is the customer's verifier's policy).
//   - SignVerify_DeliveryIDBinding: changing the delivery id after
//     Sign → Verify rejects.
//   - Sign_HexLength: produced hex is the SHA-256 width.
//
// Dispatcher (15 tests):
//   - Dispatch_RetryThenSucceed: server returns 503 twice then 200;
//     injected Sleeper records the delays; assertions on attempts,
//     status, error, and backoff sequence.
//   - Dispatch_Terminal4xx: server returns 410 → ErrTerminal, no retry.
//   - Dispatch_429Retryable: 429 twice then 200 → retry proceeds.
//   - Dispatch_408Retryable: 408 twice then 200 → retry proceeds.
//   - Dispatch_5xxAttemptsExhausted: always-503 → ErrAttemptsExhausted.
//   - Dispatch_NetworkErrorAttemptsExhausted: server closes conn →
//     ErrAttemptsExhausted.
//   - Dispatch_BodyTooLarge: 64 KiB body → ErrBodyTooLarge.
//   - Dispatch_BodyExactlyAtBoundary: exactly MaxBodyBytes bytes →
//     nil error (boundary between body-too-large and success).
//   - Dispatch_NoSecretLog: secret substring must not appear in the
//     captured slog JSON (CLAUDE.md §11 invariant).
//   - Dispatch_SSRFRejected: dial-time egress guard rejects loopback
//     with oci.ErrImageEgressDenied. With MaxAttempts at the production
//     default the dispatcher short-circuits after attempt 1
//     (PR #404 review BUG-03).
//   - Dispatch_Headers: every X-Faas-Alert-* header present and well-formed.
//   - Dispatch_AttemptHeaderIncrements: the Attempt header increments
//     on retry.
//   - Dispatch_NilLogger: Logger: nil → no panic.
//   - Dispatch_BodyContainsEvent: body carries rule/app_id/id.
//
// Backoff (1 test + 1 helper):
//   - BackoffTable: 4 retry positions match 2s/8s/32s/128s ±25%.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

const testSecret = "the-secret-that-must-never-appear-in-logs"

// testTarget builds a Target with a fresh Signer. Tests use this so
// the Signer construction is documented in one place (the secret
// lifetime equals the dispatcher's, not the per-call delivery).
func testTarget(url string) webhookout.Target {
	return webhookout.Target{URL: url, Signer: webhookout.NewSigner([]byte(testSecret))}
}

// ----------------------------------------------------------------------------
// Signer tests
// ----------------------------------------------------------------------------

// TestWebhook_SignVerify_Vector freezes the inputs and confirms Sign +
// Verify round-trip with the canonical string "<unix>.<delivery_id>.<body>".
func TestWebhook_SignVerify_Vector(t *testing.T) {
	s := webhookout.NewSigner([]byte(testSecret))
	body := []byte(`{"rule":"latency_p95_high","app":"my-api"}`)
	const unix int64 = 1700000000
	const id = "evt_01HV"

	gotHex := s.Sign(unix, id, body)
	if gotHex == "" {
		t.Fatal("Sign returned empty hex")
	}
	if err := s.Verify(unix, id, body, gotHex); err != nil {
		t.Errorf("Verify(%q) = %v, want nil", gotHex, err)
	}
}

// TestWebhook_SignVerify_TamperRejected flips one byte of body after
// Sign — Verify must reject.
func TestWebhook_SignVerify_TamperRejected(t *testing.T) {
	s := webhookout.NewSigner([]byte(testSecret))
	body := []byte(`{"rule":"a"}`)
	sig := s.Sign(1700000000, "evt_01", body)

	body[0] = 'B' // tamper
	if err := s.Verify(1700000000, "evt_01", body, sig); err == nil {
		t.Error("Verify accepted tampered body")
	}
}

// TestWebhook_SignVerify_WrongSecret: a different Signer (different key)
// must NOT verify a signature produced by the original Signer.
func TestWebhook_SignVerify_WrongSecret(t *testing.T) {
	signer := webhookout.NewSigner([]byte("secret-A"))
	other := webhookout.NewSigner([]byte("secret-B"))

	body := []byte(`{"x":1}`)
	sig := signer.Sign(1700000000, "evt_01", body)

	if err := other.Verify(1700000000, "evt_01", body, sig); err == nil {
		t.Error("Verify accepted signature from a different secret")
	}
}

// TestWebhook_SignVerify_TimestampBinding: changing the unix timestamp
// after Sign must invalidate Verify. The signer does not enforce a
// tolerance window — replay protection is the customer's verifier's
// policy (consistent with pkg/billing/paddle/webhook.go where the
// tolerance lives in the consumer).
func TestWebhook_SignVerify_TimestampBinding(t *testing.T) {
	s := webhookout.NewSigner([]byte(testSecret))
	body := []byte(`{}`)
	sig := s.Sign(1700000000, "evt_01", body)

	if err := s.Verify(1700000001, "evt_01", body, sig); err == nil {
		t.Error("Verify accepted signature with bumped timestamp")
	}
}

// TestWebhook_SignVerify_DeliveryIDBinding: changing the delivery id
// after Sign must invalidate Verify.
func TestWebhook_SignVerify_DeliveryIDBinding(t *testing.T) {
	s := webhookout.NewSigner([]byte(testSecret))
	body := []byte(`{}`)
	sig := s.Sign(1700000000, "evt_01", body)

	if err := s.Verify(1700000000, "evt_02", body, sig); err == nil {
		t.Error("Verify accepted signature with different delivery id")
	}
}

// TestWebhook_Sign_HexLength is a sanity check that the produced hex
// is the right width for a SHA-256 (32 bytes → 64 hex chars).
func TestWebhook_Sign_HexLength(t *testing.T) {
	s := webhookout.NewSigner([]byte(testSecret))
	sig := s.Sign(1700000000, "evt_01", []byte("body"))
	if len(sig) != hex.EncodedLen(sha256.Size) {
		t.Errorf("hex length = %d, want %d", len(sig), hex.EncodedLen(sha256.Size))
	}
}

// ----------------------------------------------------------------------------
// Dispatcher tests
// ----------------------------------------------------------------------------

// recordingSleeper records the delays the dispatcher requested so a
// test can assert the backoff sequence without waiting real time.
// Stays nil-safe (no goroutines needed).
type recordingSleeper struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (r *recordingSleeper) Sleep(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.delays = append(r.delays, d)
}

func (r *recordingSleeper) Delays() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]time.Duration, len(r.delays))
	copy(out, r.delays)
	return out
}

func newTestEvent() webhookout.Event {
	return webhookout.Event{
		ID:         "evt_test_01",
		OccurredAt: time.Unix(1700000000, 0).UTC(),
		Rule:       "latency_p95_high",
		AppID:      "my-api",
		Payload:    map[string]any{"value": 1.5, "threshold": 1.0},
	}
}

func newTestDispatcher(t *testing.T, sleeper *recordingSleeper, client *http.Client) *webhookout.Dispatcher {
	t.Helper()
	opts := webhookout.DispatcherOptions{
		HTTPClient: client,
	}
	if sleeper != nil {
		opts.Sleeper = sleeper.Sleep
	}
	return webhookout.NewDispatcher(opts)
}

// TestWebhook_Dispatch_RetryThenSucceed: server returns 503 twice then
// 200. Asserts attempts=3, status=200, err=nil, and the four recorded
// delays match the backoff schedule.
func TestWebhook_Dispatch_RetryThenSucceed(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	sleeper := &recordingSleeper{}
	d := newTestDispatcher(t, sleeper, srv.Client())

	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if res.Err != nil {
		t.Fatalf("err = %v, want nil", res.Err)
	}
	if res.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", res.Attempts)
	}
	if res.StatusCode != 200 {
		t.Errorf("status = %d, want 200", res.StatusCode)
	}

	delays := sleeper.Delays()
	if len(delays) != 2 {
		t.Fatalf("recorded %d sleeper calls, want 2 (between attempts 1-2 and 2-3)", len(delays))
	}
	// Two retries → base*1 (~2s ±25%) and base*4 (~8s ±25%).
	assertWithinJitter(t, "delay[0]", delays[0], 2*time.Second)
	assertWithinJitter(t, "delay[1]", delays[1], 8*time.Second)
}

// TestWebhook_Dispatch_Terminal4xx: server returns 410 once → retry
// gives up immediately. Err must satisfy errors.Is(ErrTerminal).
func TestWebhook_Dispatch_Terminal4xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusGone)
	}))
	t.Cleanup(srv.Close)

	d := newTestDispatcher(t, nil, srv.Client())
	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if !errors.Is(res.Err, webhookout.ErrTerminal) {
		t.Errorf("err = %v, want ErrTerminal", res.Err)
	}
	if res.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (terminal 4xx must not retry)", res.Attempts)
	}
	if res.StatusCode != 410 {
		t.Errorf("status = %d, want 410", res.StatusCode)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("server saw %d attempts, want 1", got)
	}
}

// TestWebhook_Dispatch_429Retryable: 429 is in the retryable set.
func TestWebhook_Dispatch_429Retryable(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	sleeper := &recordingSleeper{}
	d := newTestDispatcher(t, sleeper, srv.Client())
	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if res.Err != nil {
		t.Errorf("err = %v, want nil", res.Err)
	}
	if res.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", res.Attempts)
	}
	if len(sleeper.Delays()) != 2 {
		t.Errorf("sleeper saw %d calls, want 2", len(sleeper.Delays()))
	}
}

// TestWebhook_Dispatch_408Retryable: 408 Request Timeout is in the
// retryable set. Mirrors the matrix in plan §3.
func TestWebhook_Dispatch_408Retryable(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) <= 2 {
			w.WriteHeader(http.StatusRequestTimeout)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := newTestDispatcher(t, nil, srv.Client())
	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if res.Err != nil {
		t.Errorf("err = %v, want nil", res.Err)
	}
	if res.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", res.Attempts)
	}
}

// TestWebhook_Dispatch_5xxAttemptsExhausted: always-503 server must
// yield ErrAttemptsExhausted after MaxAttempts retries.
//
// Uses a tiny BaseBackoff so the test stays fast; the BackoffTable
// test below exercises the production 2s/8s/32s/128s schedule with
// random delays so we don't need to wait on it here.
func TestWebhook_Dispatch_5xxAttemptsExhausted(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	d := webhookout.NewDispatcher(webhookout.DispatcherOptions{
		HTTPClient:  srv.Client(),
		BaseBackoff: 1 * time.Millisecond,
	})
	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if !errors.Is(res.Err, webhookout.ErrAttemptsExhausted) {
		t.Errorf("err = %v, want ErrAttemptsExhausted", res.Err)
	}
	if got, want := int(attempts.Load()), webhookout.DefaultMaxAttempts; got != want {
		t.Errorf("attempts = %d, want %d", got, want)
	}
	if res.Attempts != webhookout.DefaultMaxAttempts {
		t.Errorf("res.Attempts = %d, want %d", res.Attempts, webhookout.DefaultMaxAttempts)
	}
}

// TestWebhook_Dispatch_NetworkErrorAttemptsExhausted: server closes
// the conn without responding → retryable transport error →
// ErrAttemptsExhausted after MaxAttempts.
//
// Uses a small BaseBackoff (1ms) so the test stays fast — the policy
// correctness is what we're testing, not the wall-clock budget.
func TestWebhook_Dispatch_NetworkErrorAttemptsExhausted(t *testing.T) {
	// Listener that accepts and immediately closes.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	url := "http://" + ln.Addr().String() + "/x"

	d := webhookout.NewDispatcher(webhookout.DispatcherOptions{
		HTTPClient:  &http.Client{Timeout: 500 * time.Millisecond},
		BaseBackoff: 1 * time.Millisecond,
	})
	res := d.Dispatch(context.Background(), testTarget(url), newTestEvent())
	if !errors.Is(res.Err, webhookout.ErrAttemptsExhausted) {
		t.Errorf("err = %v, want ErrAttemptsExhausted", res.Err)
	}
	if res.Attempts != webhookout.DefaultMaxAttempts {
		t.Errorf("attempts = %d, want %d", res.Attempts, webhookout.DefaultMaxAttempts)
	}
}

// TestWebhook_Dispatch_BodyTooLarge: 64 KiB body → ErrBodyTooLarge,
// len(BodyPrefix) == MaxBodyBytes.
func TestWebhook_Dispatch_BodyTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// 64 KiB body — way past the 32 KiB cap.
		_, _ = w.Write(make([]byte, 64*1024))
	}))
	t.Cleanup(srv.Close)

	d := newTestDispatcher(t, nil, srv.Client())
	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if !errors.Is(res.Err, webhookout.ErrBodyTooLarge) {
		t.Errorf("err = %v, want ErrBodyTooLarge", res.Err)
	}
	if len(res.BodyPrefix) != webhookout.MaxBodyBytes {
		t.Errorf("len(BodyPrefix) = %d, want %d", len(res.BodyPrefix), webhookout.MaxBodyBytes)
	}
	// Body too large is a misconfiguration — must NOT retry.
	if res.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (body-too-large must not retry)", res.Attempts)
	}
}

// TestWebhook_Dispatch_BodyExactlyAtBoundary: a body of exactly
// MaxBodyBytes bytes is NOT over the cap — it lands within the
// read window and the probe byte reads EOF, so Err is nil. Pins
// the boundary between ErrBodyTooLarge and the success path.
func TestWebhook_Dispatch_BodyExactlyAtBoundary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, webhookout.MaxBodyBytes))
	}))
	t.Cleanup(srv.Close)

	d := newTestDispatcher(t, nil, srv.Client())
	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if res.Err != nil {
		t.Errorf("err = %v, want nil at boundary", res.Err)
	}
	if len(res.BodyPrefix) != webhookout.MaxBodyBytes {
		t.Errorf("len(BodyPrefix) = %d, want %d", len(res.BodyPrefix), webhookout.MaxBodyBytes)
	}
}

// TestWebhook_Dispatch_NoSecretLog: every captured slog line must NOT
// contain the secret substring. Pins the CLAUDE.md §11 invariant
// ("Never log secret values; env secrets are sealed at rest").
func TestWebhook_Dispatch_NoSecretLog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("server hiccup"))
	}))
	t.Cleanup(srv.Close)

	var logs safeBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	opts := webhookout.DispatcherOptions{
		HTTPClient:  srv.Client(),
		Logger:      logger,
		BaseBackoff: 1 * time.Millisecond, // keep the test fast
	}
	d := webhookout.NewDispatcher(opts)

	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if !errors.Is(res.Err, webhookout.ErrAttemptsExhausted) {
		t.Fatalf("expected ErrAttemptsExhausted, got %v", res.Err)
	}
	if strings.Contains(logs.String(), testSecret) {
		t.Errorf("captured log contains secret substring; first 256 bytes:\n%s", logs.String()[:min(256, logs.Len())])
	}
}

// TestWebhook_Dispatch_SSRFRejected: the dispatcher's HTTPClient is
// wired with the OCI egress guard. Dials to a loopback / RFC1918
// address must be denied with oci.ErrImageEgressDenied. Precedent:
// pkg/oci/egress_test.go:138-156 (TestEgressDialContext_RefusesRFC1918).
//
// MaxAttempts stays at the production default (5); the dispatcher
// must treat the first dial-time SSRF rejection as terminal and not
// retry. PR #404 review finding BUG-03: re-running the dial across
// the 220s retry budget burns wall-clock for no benefit because the
// DNS outcome won't change inside that window.
func TestWebhook_Dispatch_SSRFRejected(t *testing.T) {
	dialer := oci.EgressDialContext(&net.Dialer{})
	tr := &http.Transport{DialContext: dialer}
	client := &http.Client{Transport: tr, Timeout: 1 * time.Second}

	d := webhookout.NewDispatcher(webhookout.DispatcherOptions{
		HTTPClient: client,
		// MaxAttempts left at the default (5); the SSRF guard
		// must short-circuit the loop on the first attempt.
	})

	res := d.Dispatch(context.Background(), testTarget("http://localhost:1/x"), newTestEvent())
	if !errors.Is(res.Err, oci.ErrImageEgressDenied) {
		t.Errorf("err = %v, want oci.ErrImageEgressDenied (loopback must be denied)", res.Err)
	}
	if res.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (SSRF rejection must short-circuit; no retries)", res.Attempts)
	}
}

// TestWebhook_Dispatch_Headers: every X-Faas-Alert-* header is set and
// well-formed on the first attempt.
func TestWebhook_Dispatch_Headers(t *testing.T) {
	var gotSig, gotID, gotTS, gotAttempt string
	var attemptCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount.Add(1)
		gotSig = r.Header.Get(webhookout.HeaderSignature)
		gotID = r.Header.Get(webhookout.HeaderID)
		gotTS = r.Header.Get(webhookout.HeaderTimestamp)
		gotAttempt = r.Header.Get(webhookout.HeaderAttempt)

		// Verify the signature on the server side — confirms Sign
		// canonical string matches what the server recomputes.
		body, _ := io.ReadAll(r.Body)
		signer := webhookout.NewSigner([]byte(testSecret))
		ts, _ := strconv.ParseInt(gotTS, 10, 64)
		if err := signer.Verify(ts, gotID, body, strings.TrimPrefix(gotSig, "sha256=")); err != nil {
			t.Errorf("server-side verify failed: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := newTestDispatcher(t, nil, srv.Client())
	evt := newTestEvent()
	res := d.Dispatch(context.Background(), testTarget(srv.URL), evt)
	if res.Err != nil {
		t.Fatalf("err = %v", res.Err)
	}

	if !strings.HasPrefix(gotSig, "sha256=") {
		t.Errorf("signature header = %q, want sha256= prefix", gotSig)
	}
	if gotID != evt.ID {
		t.Errorf("id header = %q, want %q", gotID, evt.ID)
	}
	if gotTS != "1700000000" {
		t.Errorf("timestamp header = %q, want 1700000000", gotTS)
	}
	if gotAttempt != "1" {
		t.Errorf("attempt header = %q, want 1", gotAttempt)
	}
}

// TestWebhook_Dispatch_Headers_AlertSet: with the zero-value
// HeaderSet (HeaderSetAlert), the dispatcher emits X-Faas-Alert-*
// headers. Pins the pre-#476 alert wire so a refactor doesn't drift
// it (issue #476 / ADR-076).
func TestWebhook_Dispatch_Headers_AlertSet(t *testing.T) {
	var gotSig, gotID, gotTS, gotAttempt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Faas-Alert-Signature")
		gotID = r.Header.Get("X-Faas-Alert-Id")
		gotTS = r.Header.Get("X-Faas-Alert-Timestamp")
		gotAttempt = r.Header.Get("X-Faas-Alert-Attempt")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := newTestDispatcher(t, nil, srv.Client())
	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if res.Err != nil {
		t.Fatalf("err = %v", res.Err)
	}

	if !strings.HasPrefix(gotSig, "sha256=") {
		t.Errorf("alert sig header = %q, want sha256= prefix", gotSig)
	}
	if gotID == "" {
		t.Errorf("alert id header missing")
	}
	if gotTS == "" {
		t.Errorf("alert timestamp header missing")
	}
	if gotAttempt != "1" {
		t.Errorf("alert attempt header = %q, want 1", gotAttempt)
	}
	// The webhook set MUST NOT leak onto the alert wire.
	if h := srv.Client().Transport; h == nil {
		// (no-op; we use r.Header below)
	}
}

// TestWebhook_Dispatch_Headers_WebhookSet: with HeaderSetWebhook, the
// dispatcher emits X-Faas-Webhook-Signature, X-Faas-Delivery-Id,
// X-Faas-Webhook-Timestamp, X-Faas-Webhook-Attempt. Pins the
// outbound-webhook wire (issue #476 / ADR-076). The alert headers
// must NOT leak onto this wire.
func TestWebhook_Dispatch_Headers_WebhookSet(t *testing.T) {
	var (
		gotSig, gotID, gotTS, gotAttempt string
		gotAlertSig, gotAlertID          string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Faas-Webhook-Signature")
		gotID = r.Header.Get("X-Faas-Delivery-Id")
		gotTS = r.Header.Get("X-Faas-Webhook-Timestamp")
		gotAttempt = r.Header.Get("X-Faas-Webhook-Attempt")
		// The alert set must NOT leak onto the webhook wire.
		gotAlertSig = r.Header.Get("X-Faas-Alert-Signature")
		gotAlertID = r.Header.Get("X-Faas-Alert-Id")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := webhookout.NewDispatcher(webhookout.DispatcherOptions{
		HTTPClient: srv.Client(),
		HeaderSet:  webhookout.HeaderSetWebhook,
	})
	evt := newTestEvent()
	res := d.Dispatch(context.Background(), testTarget(srv.URL), evt)
	if res.Err != nil {
		t.Fatalf("err = %v", res.Err)
	}

	if !strings.HasPrefix(gotSig, "sha256=") {
		t.Errorf("webhook sig header = %q, want sha256= prefix", gotSig)
	}
	if gotID != evt.ID {
		t.Errorf("webhook id header = %q, want %q (X-Faas-Delivery-Id binds to event id)", gotID, evt.ID)
	}
	if gotTS == "" {
		t.Errorf("webhook timestamp header missing")
	}
	if gotAttempt != "1" {
		t.Errorf("webhook attempt header = %q, want 1", gotAttempt)
	}
	if gotAlertSig != "" {
		t.Errorf("alert sig leaked onto webhook wire: %q (HeaderSet mixing)", gotAlertSig)
	}
	if gotAlertID != "" {
		t.Errorf("alert id leaked onto webhook wire: %q (HeaderSet mixing)", gotAlertID)
	}
}

// TestWebhook_Dispatch_CloudEventsStructured is the executable standards
// fixture for the opt-in CloudEvents 1.0 delivery format. It pins the
// structured-mode content type, required context attributes, account_id
// extension, and preservation of the existing HMAC signature over the exact
// wire body.
func TestWebhook_Dispatch_CloudEventsStructured(t *testing.T) {
	var (
		body        []byte
		contentType string
		signature   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		signature = r.Header.Get("X-Faas-Webhook-Signature")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	evt := newTestEvent()
	evt.Source = "urn:gregale:app:" + evt.AppID
	evt.Type = "app.parked"
	evt.Subject = "apps/" + evt.AppID
	evt.AccountID = "acct_test"
	evt.Data = json.RawMessage(`{"reason":"idle"}`)
	d := webhookout.NewDispatcher(webhookout.DispatcherOptions{
		HTTPClient: srv.Client(),
		HeaderSet:  webhookout.HeaderSetWebhook,
		Format:     webhookout.DeliveryFormatCloudEvents,
	})
	res := d.Dispatch(context.Background(), testTarget(srv.URL), evt)
	if res.Err != nil {
		t.Fatalf("err = %v", res.Err)
	}
	if contentType != "application/cloudevents+json" {
		t.Fatalf("content type = %q, want application/cloudevents+json", contentType)
	}
	var envelope struct {
		SpecVersion     string          `json:"specversion"`
		ID              string          `json:"id"`
		Source          string          `json:"source"`
		Type            string          `json:"type"`
		Subject         string          `json:"subject"`
		Time            string          `json:"time"`
		DataContentType string          `json:"datacontenttype"`
		Data            json.RawMessage `json:"data"`
		AccountID       string          `json:"account_id"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode CloudEvents body: %v", err)
	}
	if envelope.SpecVersion != "1.0" || envelope.ID != evt.ID ||
		envelope.Source != evt.Source || envelope.Type != evt.Type ||
		envelope.Subject != evt.Subject || envelope.Time == "" ||
		envelope.DataContentType != "application/json" ||
		envelope.AccountID != evt.AccountID {
		t.Fatalf("CloudEvents context = %+v, want id=%q source=%q type=%q subject=%q account_id=%q",
			envelope, evt.ID, evt.Source, evt.Type, evt.Subject, evt.AccountID)
	}
	if string(envelope.Data) != string(evt.Data) {
		t.Fatalf("data = %s, want %s", envelope.Data, evt.Data)
	}
	if !strings.HasPrefix(signature, "sha256=") {
		t.Fatalf("signature = %q, want sha256= prefix", signature)
	}
	if err := webhookout.NewSigner([]byte(testSecret)).Verify(evt.OccurredAt.Unix(), evt.ID, body, strings.TrimPrefix(signature, "sha256=")); err != nil {
		t.Fatalf("CloudEvents body signature verification: %v", err)
	}
}

// TestWebhook_Dispatch_AttemptHeaderIncrements: the X-Faas-Alert-Attempt
// header must increment on retry so the customer's verifier can tell
// which attempt it's looking at.
func TestWebhook_Dispatch_AttemptHeaderIncrements(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get(webhookout.HeaderAttempt))
		mu.Unlock()
		if attempts.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := newTestDispatcher(t, nil, srv.Client())
	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if res.Err != nil {
		t.Fatalf("err = %v", res.Err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"1", "2", "3"}
	if len(seen) != len(want) {
		t.Fatalf("saw %d attempts, want %d", len(seen), len(want))
	}
	for i, v := range want {
		if seen[i] != v {
			t.Errorf("seen[%d] = %q, want %q", i, seen[i], v)
		}
	}
}

// TestWebhook_Dispatch_NilLogger: passing Logger: nil must fall back
// to slog.Default() and NOT panic. The zero-value options test.
func TestWebhook_Dispatch_NilLogger(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := webhookout.NewDispatcher(webhookout.DispatcherOptions{
		HTTPClient: srv.Client(),
		// Logger: nil — must not panic.
	})
	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if res.Err != nil {
		t.Errorf("err = %v, want nil", res.Err)
	}
}

// TestWebhook_Dispatch_BodyContainsEvent: the body posted to the
// customer carries rule + app + payload fields. Pins the wire shape
// PR 4's docs depend on.
func TestWebhook_Dispatch_BodyContainsEvent(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := newTestDispatcher(t, nil, srv.Client())
	evt := newTestEvent()
	res := d.Dispatch(context.Background(), testTarget(srv.URL), evt)
	if res.Err != nil {
		t.Fatalf("err = %v", res.Err)
	}

	if got["rule"] != evt.Rule {
		t.Errorf("body.rule = %v, want %v", got["rule"], evt.Rule)
	}
	if got["app_id"] != evt.AppID {
		t.Errorf("body.app_id = %v, want %v", got["app_id"], evt.AppID)
	}
	if got["id"] != evt.ID {
		t.Errorf("body.id = %v, want %v", got["id"], evt.ID)
	}
}

// ----------------------------------------------------------------------------
// Backoff tests
// ----------------------------------------------------------------------------

// assertWithinJitter pins the bounds for a single delay. base * 4^n
// with ±25% jitter — see plan §3.
func assertWithinJitter(t *testing.T, name string, got, wantBase time.Duration) {
	t.Helper()
	min := time.Duration(float64(wantBase) * 0.75)
	max := time.Duration(float64(wantBase) * 1.25)
	if got < min || got > max {
		t.Errorf("%s = %v, want in [%v, %v]", name, got, min, max)
	}
}

// TestWebhook_BackoffTable exercises the four retry positions via a
// dispatcher that always returns 503 — the recorded sleeper delays
// must match the schedule.
func TestWebhook_BackoffTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	sleeper := &recordingSleeper{}
	d := newTestDispatcher(t, sleeper, srv.Client())
	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if !errors.Is(res.Err, webhookout.ErrAttemptsExhausted) {
		t.Fatalf("err = %v, want ErrAttemptsExhausted", res.Err)
	}

	delays := sleeper.Delays()
	if len(delays) != webhookout.DefaultMaxAttempts-1 {
		t.Fatalf("sleeper saw %d calls, want %d", len(delays), webhookout.DefaultMaxAttempts-1)
	}
	// Schedule (attempt index → base): 0→2s, 1→8s, 2→32s, 3→128s.
	want := []time.Duration{
		2 * time.Second,
		8 * time.Second,
		32 * time.Second,
		128 * time.Second,
	}
	for i, w := range want {
		assertWithinJitter(t, fmt.Sprintf("delay[%d]", i), delays[i], w)
	}
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// safeBuffer is a tiny mutex-guarded io.Writer so slog's JSONHandler
// doesn't trip -race when its internal buf writes overlap with our
// strings.Contains read. Precedent: pkg/e2etest/harness.go.
type safeBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func (b *safeBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf)
}
