package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeMailBounce records every HandleMailBounce call so tests
// can pin the normalized bounce shape. Implements the apid-side
// mailBounceHandler interface.
type fakeMailBounce struct {
	calls atomic.Int64
	last  meter.MailBounce
	err   error
}

func (f *fakeMailBounce) HandleMailBounce(_ context.Context, b meter.MailBounce) error {
	f.calls.Add(1)
	f.last = b
	return f.err
}

// resendServerForTest builds a minimal *server suitable for
// exercising the resendWebhook handler. The handler's only
// dependencies are the signed-secret field, durable replay store,
// mailBounce seam, and audit shim. Use the production constructor so
// replay coverage cannot silently drift from the handler's wiring.
func resendServerForTest(t *testing.T, secret string, bouncer *fakeMailBounce) *server {
	t.Helper()
	s := newServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "", noopNotifier{})
	s.resendWebhookSecret = secret
	s.mailBounce = bouncer
	return s
}

// signResendReq builds a POST /v1/webhooks/resend request with
// valid Svix headers signed with secret.
func signResendReq(t *testing.T, body []byte, secret, id string) *http.Request {
	t.Helper()
	if id == "" {
		id = "msg_test_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := mail.SignResendForTest(body, secret, id, ts)
	req := httptest.NewRequest("POST", "/v1/webhooks/resend", bytes.NewReader(body))
	req.Header.Set(mail.ResendSignatureHeader, sig)
	req.Header.Set(mail.ResendIDHeader, id)
	req.Header.Set(mail.ResendTimestampHeader, ts)
	return req
}

// TestResendWebhook_HappyPath pins the headline flow: a valid
// signature on a `email.bounced` envelope dispatches a
// hard_bounce MailBounce through the mailBounceHandler with
// the parsed email + delivery UUID.
func TestResendWebhook_HappyPath(t *testing.T) {
	secret := mail.RandomResendSecretForTest()
	body := []byte(`{"type":"email.bounced","data":{"email":"alice@example.com","created_at":"2026-08-29T10:00:00Z"}}`)
	bouncer := &fakeMailBounce{}
	s := resendServerForTest(t, secret, bouncer)

	rec := httptest.NewRecorder()
	s.resendWebhook(rec, signResendReq(t, body, secret, "msg_1"))

	if rec.Code != http.StatusOK {
		t.Fatalf("happy path: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if bouncer.calls.Load() != 1 {
		t.Fatalf("mailBounce invocations = %d, want 1", bouncer.calls.Load())
	}
	got := bouncer.last
	if got.Email != "alice@example.com" {
		t.Fatalf("bounce.Email = %q, want alice@example.com", got.Email)
	}
	if got.Reason != "hard_bounce" {
		t.Fatalf("bounce.Reason = %q, want hard_bounce", got.Reason)
	}
	if got.Source != "resend" {
		t.Fatalf("bounce.Source = %q, want resend", got.Source)
	}
	if got.ProviderEventID != "msg_1" {
		t.Fatalf("bounce.ProviderEventID = %q, want msg_1", got.ProviderEventID)
	}
}

// TestResendWebhook_BadSignature pins the 400 path. A tampered
// signature returns ErrBadSignature → 400 (NOT 503; that's the
// "secret unset" branch).
func TestResendWebhook_BadSignature(t *testing.T) {
	secret := mail.RandomResendSecretForTest()
	body := []byte(`{"type":"email.bounced","data":{"email":"a@b"}}`)
	bouncer := &fakeMailBounce{}
	s := resendServerForTest(t, secret, bouncer)

	// Sign with one secret, verify with another.
	other := mail.RandomResendSecretForTest()
	rec := httptest.NewRecorder()
	s.resendWebhook(rec, signResendReq(t, body, other, "msg_bad"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad signature: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if bouncer.calls.Load() != 0 {
		t.Fatal("bouncer invoked despite bad signature")
	}
}

// TestResendWebhook_UnsetSecret pins the fail-closed 503. A
// missing FAAS_MAIL_RESEND_WEBHOOK_SECRET cannot silently accept
// unsigned events — the route returns 503 so an operator
// notices.
func TestResendWebhook_UnsetSecret(t *testing.T) {
	bouncer := &fakeMailBounce{}
	s := resendServerForTest(t, "", bouncer)

	body := []byte(`{"type":"email.bounced","data":{"email":"a@b"}}`)
	rec := httptest.NewRecorder()
	s.resendWebhook(rec, signResendReq(t, body, mail.RandomResendSecretForTest(), "msg_unset"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unset secret: status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	if bouncer.calls.Load() != 0 {
		t.Fatal("bouncer invoked despite unset secret")
	}
}

// TestResendWebhook_ReplayIgnored pins the webhookdedupe branch.
// A redelivery within the 5-minute TTL is a no-op — the bouncer
// is NOT invoked a second time, but the response is 200 so
// Resend stops retrying.
func TestResendWebhook_ReplayIgnored(t *testing.T) {
	secret := mail.RandomResendSecretForTest()
	body := []byte(`{"type":"email.bounced","data":{"email":"a@b"}}`)
	bouncer := &fakeMailBounce{}
	s := resendServerForTest(t, secret, bouncer)

	// First delivery: success.
	rec1 := httptest.NewRecorder()
	s.resendWebhook(rec1, signResendReq(t, body, secret, "msg_replay"))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first delivery: status = %d, want 200", rec1.Code)
	}

	// Second delivery: same svix-id. The dedupe helper returns
	// ErrReplay; the handler emits audit + 200 and does NOT
	// re-invoke the bouncer.
	rec2 := httptest.NewRecorder()
	s.resendWebhook(rec2, signResendReq(t, body, secret, "msg_replay"))
	if rec2.Code != http.StatusOK {
		t.Fatalf("replay: status = %d, want 200 (idempotent ack)", rec2.Code)
	}
	if bouncer.calls.Load() != 1 {
		t.Fatalf("bouncer invocations = %d, want 1 (replay must not re-invoke)", bouncer.calls.Load())
	}
}

// TestResendWebhook_UnknownEventType pins the observability
// branch: Resend sends `email.delivered` / `email.opened` /
// `email.clicked` too. None of those should reach the bounce
// handler — they get 200 immediately.
func TestResendWebhook_UnknownEventType(t *testing.T) {
	secret := mail.RandomResendSecretForTest()
	body := []byte(`{"type":"email.delivered","data":{"email":"a@b"}}`)
	bouncer := &fakeMailBounce{}
	s := resendServerForTest(t, secret, bouncer)

	rec := httptest.NewRecorder()
	s.resendWebhook(rec, signResendReq(t, body, secret, "msg_delivered"))

	if rec.Code != http.StatusOK {
		t.Fatalf("delivered event: status = %d, want 200", rec.Code)
	}
	if bouncer.calls.Load() != 0 {
		t.Fatal("delivered event should not reach the bounce handler")
	}
}

// TestResendWebhook_ComplaintRecognised pins that complaint
// events round-trip into the bounce handler with reason=complaint.
func TestResendWebhook_ComplaintRecognised(t *testing.T) {
	secret := mail.RandomResendSecretForTest()
	body := []byte(`{"type":"email.complained","data":{"email":"alice@example.com"}}`)
	bouncer := &fakeMailBounce{}
	s := resendServerForTest(t, secret, bouncer)

	rec := httptest.NewRecorder()
	s.resendWebhook(rec, signResendReq(t, body, secret, "msg_complaint"))

	if rec.Code != http.StatusOK {
		t.Fatalf("complaint: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := bouncer.last.Reason; got != "complaint" {
		t.Fatalf("complaint bounced as %q, want complaint", got)
	}
}

// TestResendWebhook_StaleTimestamp pins the replay-window guard.
// A 10-minute-old delivery fails the tolerance check → 400.
func TestResendWebhook_StaleTimestamp(t *testing.T) {
	secret := mail.RandomResendSecretForTest()
	body := []byte(`{"type":"email.bounced","data":{"email":"a@b"}}`)
	bouncer := &fakeMailBounce{}
	s := resendServerForTest(t, secret, bouncer)

	old := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	id := "msg_stale"
	sig := mail.SignResendForTest(body, secret, id, old)
	req := httptest.NewRequest("POST", "/v1/webhooks/resend", bytes.NewReader(body))
	req.Header.Set(mail.ResendSignatureHeader, sig)
	req.Header.Set(mail.ResendIDHeader, id)
	req.Header.Set(mail.ResendTimestampHeader, old)

	rec := httptest.NewRecorder()
	s.resendWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("stale ts: status = %d, want 400", rec.Code)
	}
	if bouncer.calls.Load() != 0 {
		t.Fatal("stale delivery should not reach the bounce handler")
	}
}

// TestResendWebhook_BadJSON pins the JSON-parse path. A valid
// signature on a non-JSON body returns 400 — the verifier is
// the only thing that matters for auth, the parser is a
// downstream guard.
func TestResendWebhook_BadJSON(t *testing.T) {
	secret := mail.RandomResendSecretForTest()
	body := []byte(`{not json`)
	bouncer := &fakeMailBounce{}
	s := resendServerForTest(t, secret, bouncer)

	rec := httptest.NewRecorder()
	s.resendWebhook(rec, signResendReq(t, body, secret, "msg_bad_json"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON: status = %d, want 400", rec.Code)
	}
}

// TestResendWebhook_BouncerErrorIs500 pins the contract that a
// bounce-handler failure surfaces as 500 + audit row (NOT 200).
// The handler must not silently swallow a DB outage.
func TestResendWebhook_BouncerErrorIs500(t *testing.T) {
	secret := mail.RandomResendSecretForTest()
	body := []byte(`{"type":"email.bounced","data":{"email":"a@b"}}`)
	bouncer := &fakeMailBounce{err: io.ErrUnexpectedEOF}
	s := resendServerForTest(t, secret, bouncer)

	rec := httptest.NewRecorder()
	s.resendWebhook(rec, signResendReq(t, body, secret, "msg_err"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("bouncer error: status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}

// TestResendWebhook_NilBouncerIs500 pins that a misconfigured
// wiring (mailBounce not set) is loud rather than silent —
// the route must NOT no-op.
func TestResendWebhook_NilBouncerIs500(t *testing.T) {
	secret := mail.RandomResendSecretForTest()
	body := []byte(`{"type":"email.bounced","data":{"email":"a@b"}}`)
	s := resendServerForTest(t, secret, nil)
	s.mailBounce = nil // explicit nil for clarity

	rec := httptest.NewRecorder()
	s.resendWebhook(rec, signResendReq(t, body, secret, "msg_nil"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("nil bouncer: status = %d, want 500", rec.Code)
	}
}

// TestResendWebhook_SoftBounceIgnoredIs200 pins that
// ErrMailBounceIgnored (returned by the bounce handler for
// soft_bounce + unknown reasons) maps to 200 so the provider
// stops retrying.
func TestResendWebhook_SoftBounceIgnoredIs200(t *testing.T) {
	secret := mail.RandomResendSecretForTest()
	body := []byte(`{"type":"email.bounced","data":{"email":"a@b"}}`)
	bouncer := &fakeMailBounce{err: meter.ErrMailBounceIgnored}
	s := resendServerForTest(t, secret, bouncer)

	rec := httptest.NewRecorder()
	s.resendWebhook(rec, signResendReq(t, body, secret, "msg_soft"))

	if rec.Code != http.StatusOK {
		t.Fatalf("soft-bounce: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestResendReason_Mapping pins the closed-set mapping table.
// A typo here would silently misclassify complaints as
// hard_bounces (or vice versa), advancing dunning for a spam
// report — the kind of bug that costs customers.
func TestResendReason_Mapping(t *testing.T) {
	cases := map[string]string{
		"email.bounced":    "hard_bounce",
		"email.complained": "complaint",
		"email.delivered":  "",
		"email.opened":     "",
		"email.clicked":    "",
		"email.unknown":    "",
	}
	for in, want := range cases {
		if got := resendReason(in); got != want {
			t.Fatalf("resendReason(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestResendWebhook_PayloadShape pins the Resend envelope the
// handler parses. A future Resend schema change that breaks
// this contract surfaces here — the audit row + bouncer call
// would otherwise go silently wrong.
func TestResendWebhook_PayloadShape(t *testing.T) {
	body := []byte(`{"type":"email.bounced","data":{"email":"x@y","created_at":"2026-08-29T10:00:00Z"}}`)
	var ev resendEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Type != "email.bounced" {
		t.Fatalf("ev.Type = %q, want email.bounced", ev.Type)
	}
	if ev.Data.Email != "x@y" {
		t.Fatalf("ev.Data.Email = %q, want x@y", ev.Data.Email)
	}
	if ev.Data.CreatedAt == "" {
		t.Fatal("ev.Data.CreatedAt not captured")
	}
}
