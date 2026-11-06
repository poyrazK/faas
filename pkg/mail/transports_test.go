// Tests for the Resend + Postmark transports and the factory. Use
// httptest.NewServer to simulate the upstream API — no real network.
package mail_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mail"
)

// TestResendSender_Success confirms a 200 from the upstream yields no
// error and the request body has the expected fields.
func TestResendSender_Success(t *testing.T) {
	var gotBody mail.ResendRequest
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emails" {
			t.Errorf("path = %q, want /emails", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc-123"}`))
	}))
	t.Cleanup(srv.Close)

	s, err := mail.NewResendSender(mail.ResendConfig{
		APIKey:  "re_test_xxx",
		From:    "ops@example.test",
		BaseURL: srv.URL,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewResendSender: %v", err)
	}
	if err := s.Send(context.Background(), mail.Message{
		To:       []string{"jane@example.test"},
		Subject:  "Hello",
		TextBody: "world",
		HTMLBody: "<p>world</p>",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotAuth != "Bearer re_test_xxx" {
		t.Errorf("Authorization = %q, want Bearer re_test_xxx", gotAuth)
	}
	if gotBody.From != "ops@example.test" || gotBody.Subject != "Hello" {
		t.Errorf("body = %+v", gotBody)
	}
	if len(gotBody.To) != 1 || gotBody.To[0] != "jane@example.test" {
		t.Errorf("to = %v", gotBody.To)
	}
	if gotBody.Text != "world" {
		t.Errorf("text = %q", gotBody.Text)
	}
	if gotBody.HTML != "<p>world</p>" {
		t.Errorf("html = %q", gotBody.HTML)
	}
}

// TestResendSender_4xxPropagates confirms a 422 from upstream
// surfaces a useful error message.
func TestResendSender_4xxPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"name":"validation_error","message":"to field is invalid"}`))
	}))
	t.Cleanup(srv.Close)

	s, _ := mail.NewResendSender(mail.ResendConfig{
		APIKey: "re_test", From: "ops@example.test", BaseURL: srv.URL,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	err := s.Send(context.Background(), mail.Message{To: []string{"bad"}, Subject: "x", TextBody: "y"})
	if err == nil {
		t.Fatal("expected error on 422")
	}
	if !strings.Contains(err.Error(), "validation_error") {
		t.Errorf("err = %v, want validation_error in message", err)
	}
}

// TestResendSender_MissingAPIKey confirms NewResendSender fails
// closed when APIKey is empty.
func TestResendSender_MissingAPIKey(t *testing.T) {
	if _, err := mail.NewResendSender(mail.ResendConfig{From: "ops@example.test"}); err == nil {
		t.Fatal("expected error for missing APIKey")
	}
}

// TestPostmarkSender_Success mirrors TestResendSender_Success for
// the Postmark transport.
func TestPostmarkSender_Success(t *testing.T) {
	var gotBody mail.PostmarkRequest
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/email" {
			t.Errorf("path = %q, want /email", r.URL.Path)
		}
		gotToken = r.Header.Get("X-Postmark-Server-Token")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"To":"jane@example.test","SubmittedAt":"2026-01-01T00:00:00Z","MessageID":"abc","ErrorCode":0,"Message":"OK"}`))
	}))
	t.Cleanup(srv.Close)

	s, err := mail.NewPostmarkSender(mail.PostmarkConfig{
		ServerToken: "pm_test_xxx",
		From:        "ops@example.test",
		BaseURL:     srv.URL,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewPostmarkSender: %v", err)
	}
	if err := s.Send(context.Background(), mail.Message{
		To:       []string{"jane@example.test"},
		Subject:  "Hello",
		TextBody: "world",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotToken != "pm_test_xxx" {
		t.Errorf("X-Postmark-Server-Token = %q", gotToken)
	}
	if gotBody.To != "jane@example.test" {
		t.Errorf("to = %q", gotBody.To)
	}
	if gotBody.From != "ops@example.test" {
		t.Errorf("from = %q", gotBody.From)
	}
}

// TestPostmarkSender_MissingToken confirms the config validator fails
// closed.
func TestPostmarkSender_MissingToken(t *testing.T) {
	if _, err := mail.NewPostmarkSender(mail.PostmarkConfig{From: "ops@example.test"}); err == nil {
		t.Fatal("expected error for missing ServerToken")
	}
}

// TestSenderFromEnv_UnsetOnProdFailsClosed pins issue #246's headline
// contract: an unset FAAS_MAIL_TRANSPORT on a production box refuses to
// boot so the operator cannot accidentally run a daemon that silently
// drops email into slog. The pre-#246 behaviour was a quiet LogSender
// fallback that hid a 4-step dunning ladder inside the journal.
func TestSenderFromEnv_UnsetOnProdFailsClosed(t *testing.T) {
	getenv := func(string) string { return "" }
	s, err := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !errors.Is(err, mail.ErrMailUnsetInProd) {
		t.Fatalf("err = %v, want errors.Is(err, mail.ErrMailUnsetInProd)", err)
	}
	if s != nil {
		t.Errorf("sender = %T, want nil on fail-closed", s)
	}
}

// TestSenderFromEnv_UnsetOnDevResolvesToLog pins the dev-box escape:
// FAAS_DEV=1 keeps the developer-friendly behaviour where an unset
// transport resolves to LogSender without ceremony. This row is what
// `make test` exercises on a developer's laptop.
func TestSenderFromEnv_UnsetOnDevResolvesToLog(t *testing.T) {
	getenv := func(k string) string {
		if k == "FAAS_DEV" {
			return "1"
		}
		return ""
	}
	s, err := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("SenderFromEnv: %v", err)
	}
	if _, ok := s.(*mail.LogSender); !ok {
		t.Errorf("unset on dev = %T, want *mail.LogSender", s)
	}
}

// TestSenderFromEnv_UnsetOnProdWithFAAS_DEVZeroStillFailsClosed is the
// regression row for the strict-default rule: only an explicitly
// truthy FAAS_DEV value marks a box as dev. "0", empty, "false" or
// any unrecognised value all collapse to "production" because the
// safe default for an ambiguous value is the strict one.
func TestSenderFromEnv_UnsetOnProdWithFAAS_DEVZeroStillFailsClosed(t *testing.T) {
	for _, literal := range []string{"0", "", "false", "no", "off", "maybe"} {
		literal := literal
		t.Run("FAAS_DEV="+literal, func(t *testing.T) {
			getenv := func(k string) string {
				if k == "FAAS_DEV" {
					return literal
				}
				return ""
			}
			_, err := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if !errors.Is(err, mail.ErrMailUnsetInProd) {
				t.Errorf("FAAS_DEV=%q: err = %v, want errors.Is(err, mail.ErrMailUnsetInProd)", literal, err)
			}
		})
	}
}

// TestSenderFromEnv_PicksNoop confirms FAAS_MAIL_TRANSPORT=noop on a
// FAAS_DEV box returns the silent NoopSender.
func TestSenderFromEnv_PicksNoop(t *testing.T) {
	getenv := func(k string) string {
		switch k {
		case "FAAS_MAIL_TRANSPORT":
			return "noop"
		case "FAAS_DEV":
			return "1"
		}
		return ""
	}
	s, err := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("SenderFromEnv: %v", err)
	}
	if _, ok := s.(mail.NoopSender); !ok {
		t.Errorf("transport = %T, want mail.NoopSender", s)
	}
}

// TestSenderFromEnv_ResendFailsClosedOnMissingAPIKey confirms the
// fail-closed contract: an operator-selected resend transport with
// FAAS_MAIL_RESEND_API_KEY empty returns ErrMailerMisconfigured so
// apid/meterd refuse to boot (G4-closure ADR-115 §D5). The pre-#115
// behaviour was a WARN + LogSender fallback.
func TestSenderFromEnv_ResendFailsClosedOnMissingAPIKey(t *testing.T) {
	getenv := func(k string) string {
		switch k {
		case "FAAS_MAIL_TRANSPORT":
			return "resend"
		case "FAAS_MAIL_FROM":
			return "ops@example.test"
		}
		return ""
	}
	s, err := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !errors.Is(err, mail.ErrMailerMisconfigured) {
		t.Fatalf("err = %v, want errors.Is(err, mail.ErrMailerMisconfigured)", err)
	}
	if s != nil {
		t.Errorf("sender = %T, want nil on fail-closed", s)
	}
	if !errors.Is(err, mail.ErrResendMissingAPIKey) {
		t.Errorf("err = %v, want wrapped mail.ErrResendMissingAPIKey", err)
	}
}

// TestSenderFromEnv_PicksResendLive confirms a fully-configured
// resend transport is picked.
func TestSenderFromEnv_PicksResendLive(t *testing.T) {
	getenv := func(k string) string {
		switch k {
		case "FAAS_MAIL_TRANSPORT":
			return "resend"
		case "FAAS_MAIL_RESEND_API_KEY":
			return "re_test"
		case "FAAS_MAIL_FROM":
			return "ops@example.test"
		}
		return ""
	}
	s, err := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("SenderFromEnv: %v", err)
	}
	if _, ok := s.(*mail.ResendSender); !ok {
		t.Errorf("transport = %T, want *mail.ResendSender", s)
	}
}

// TestSenderFromEnv_PostmarkFailsClosedOnMissingToken mirrors
// TestSenderFromEnv_ResendFailsClosedOnMissingAPIKey for the
// Postmark sibling (ADR-115 §D5).
func TestSenderFromEnv_PostmarkFailsClosedOnMissingToken(t *testing.T) {
	getenv := func(k string) string {
		switch k {
		case "FAAS_MAIL_TRANSPORT":
			return "postmark"
		case "FAAS_MAIL_FROM":
			return "ops@example.test"
		}
		return ""
	}
	s, err := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !errors.Is(err, mail.ErrMailerMisconfigured) {
		t.Fatalf("err = %v, want errors.Is(err, mail.ErrMailerMisconfigured)", err)
	}
	if s != nil {
		t.Errorf("sender = %T, want nil on fail-closed", s)
	}
	if !errors.Is(err, mail.ErrPostmarkMissingToken) {
		t.Errorf("err = %v, want wrapped mail.ErrPostmarkMissingToken", err)
	}
}

// TestSenderFromEnv_UnknownTransportOnProdFailsClosed pins issue
// #246's other headline contract: a typo in FAAS_MAIL_TRANSPORT
// ("resned") used to fall back to LogSender with WARN, which is the
// same silent drop as the unset case with none of the visibility.
// It now refuses to boot, mirroring the unset case.
func TestSenderFromEnv_UnknownTransportOnProdFailsClosed(t *testing.T) {
	getenv := func(k string) string {
		if k == "FAAS_MAIL_TRANSPORT" {
			return "carrier-pigeon"
		}
		return ""
	}
	s, err := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !errors.Is(err, mail.ErrMailUnknownTransport) {
		t.Fatalf("err = %v, want errors.Is(err, mail.ErrMailUnknownTransport)", err)
	}
	if s != nil {
		t.Errorf("sender = %T, want nil on fail-closed", s)
	}
}

// TestSenderFromEnv_UnknownTransportOnDevWarnsAndFallsBack is the
// dev-box escape for the unknown-transport branch: a developer
// iterating on a brand-new transport name still boots, with a WARN
// so the typo is visible in the journal. The strict row above pins
// the on-prod contract.
func TestSenderFromEnv_UnknownTransportOnDevWarnsAndFallsBack(t *testing.T) {
	getenv := func(k string) string {
		switch k {
		case "FAAS_MAIL_TRANSPORT":
			return "carrier-pigeon"
		case "FAAS_DEV":
			return "1"
		}
		return ""
	}
	s, err := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("SenderFromEnv: %v", err)
	}
	if _, ok := s.(*mail.LogSender); !ok {
		t.Errorf("unknown on dev = %T, want *mail.LogSender", s)
	}
}

// TestResendSender_5xxWrapsErrTransient confirms a 503 from the
// upstream yields an error that errors.Is(..., mail.ErrTransient)
// returns true — the contract callers retry on.
func TestResendSender_5xxWrapsErrTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"name":"server_error","message":"try later"}`))
	}))
	t.Cleanup(srv.Close)

	s, err := mail.NewResendSender(mail.ResendConfig{
		APIKey:  "re_test",
		From:    "ops@example.test",
		BaseURL: srv.URL,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewResendSender: %v", err)
	}
	err = s.Send(context.Background(), mail.Message{To: []string{"x@y.test"}, Subject: "x"})
	if err == nil {
		t.Fatal("expected error on 503")
	}
	if !errors.Is(err, mail.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(err, mail.ErrTransient)", err)
	}
}

// TestResendSender_4xxIsNotTransient confirms a 4xx is a permanent
// error (no ErrTransient wrap). The contract is: only retry on
// network failures + 5xx.
func TestResendSender_4xxIsNotTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"name":"validation_error"}`))
	}))
	t.Cleanup(srv.Close)

	s, _ := mail.NewResendSender(mail.ResendConfig{
		APIKey:  "re_test",
		From:    "ops@example.test",
		BaseURL: srv.URL,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	err := s.Send(context.Background(), mail.Message{To: []string{"x@y.test"}, Subject: "x"})
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if errors.Is(err, mail.ErrTransient) {
		t.Errorf("err = %v, did not expect errors.Is(err, mail.ErrTransient)", err)
	}
}

// TestPostmarkSender_5xxWrapsErrTransient mirrors the Resend test
// for the Postmark sibling.
func TestPostmarkSender_5xxWrapsErrTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"ErrorCode":0,"Message":"down"}`))
	}))
	t.Cleanup(srv.Close)

	s, err := mail.NewPostmarkSender(mail.PostmarkConfig{
		ServerToken: "pm_test",
		From:        "ops@example.test",
		BaseURL:     srv.URL,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewPostmarkSender: %v", err)
	}
	err = s.Send(context.Background(), mail.Message{To: []string{"x@y.test"}, Subject: "x"})
	if !errors.Is(err, mail.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(err, mail.ErrTransient)", err)
	}
}

// --- C3: 429 / Idempotency-Key / Retry-After -----------------------------
// Issue #246 acceptance item 3. Resend's free tier is 100/day, so
// 429 is an operational certainty and was previously classified as
// permanent — every customer on a free-tier quota warning silently
// dropped. The fix has three parts:
//
//  1. Treat 429 as transient (errors.Is(err, ErrTransient) returns
//     true) so the retry decorator (pkg/mail/retry.go) picks it up.
//  2. Parse the upstream Retry-After header (RFC 7231 §7.1.3) and
//     expose it via *TransientError.RetryAfter so the decorator can
//     honour the provider's back-off instead of guessing.
//  3. Send an Idempotency-Key header (Resend) / X-Idempotency-Key
//     (Postmark) when Message.MessageID is set so a network-level
//     retry that the upstream already accepted is deduplicated.

// TestResendSender_429WrapsErrTransient confirms 429 is now a
// transient error — pre-#246 it was classified permanent and the
// retry decorator (pkg/mail/retry.go) would never have picked it up.
func TestResendSender_429WrapsErrTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"name":"rate_limit","message":"slow down"}`))
	}))
	t.Cleanup(srv.Close)

	s, err := mail.NewResendSender(mail.ResendConfig{
		APIKey: "re_test", From: "ops@example.test", BaseURL: srv.URL,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewResendSender: %v", err)
	}
	err = s.Send(context.Background(), mail.Message{To: []string{"x@y.test"}, Subject: "x"})
	if err == nil {
		t.Fatal("expected error on 429")
	}
	if !errors.Is(err, mail.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(err, mail.ErrTransient)", err)
	}
	var te *mail.TransientError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want errors.As(*TransientError)", err)
	}
	if te.Status != http.StatusTooManyRequests {
		t.Errorf("TransientError.Status = %d, want %d", te.Status, http.StatusTooManyRequests)
	}
}

// TestResendSender_429CarriesRetryAfter confirms the upstream
// Retry-After header is parsed into TransientError.RetryAfter. The
// retry decorator (C4) consumes this field; without it the decorator
// falls back to its base delay and may hit the rate limit again.
func TestResendSender_429CarriesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"name":"rate_limit"}`))
	}))
	t.Cleanup(srv.Close)

	s, err := mail.NewResendSender(mail.ResendConfig{
		APIKey: "re_test", From: "ops@example.test", BaseURL: srv.URL,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewResendSender: %v", err)
	}
	err = s.Send(context.Background(), mail.Message{To: []string{"x@y.test"}, Subject: "x"})
	var te *mail.TransientError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want errors.As(*TransientError)", err)
	}
	if te.RetryAfter != 2*time.Second {
		t.Errorf("TransientError.RetryAfter = %s, want 2s", te.RetryAfter)
	}
}

// TestResendSender_5xxCarriesRetryAfter mirrors the 429 row for the
// 5xx branch. Some upstream proxies attach Retry-After to 503 as
// well; the parser must handle both.
func TestResendSender_5xxCarriesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	s, _ := mail.NewResendSender(mail.ResendConfig{
		APIKey: "re_test", From: "ops@example.test", BaseURL: srv.URL,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	err := s.Send(context.Background(), mail.Message{To: []string{"x@y.test"}, Subject: "x"})
	var te *mail.TransientError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want errors.As(*TransientError)", err)
	}
	if te.RetryAfter != 5*time.Second {
		t.Errorf("TransientError.RetryAfter = %s, want 5s", te.RetryAfter)
	}
	if te.Status != http.StatusServiceUnavailable {
		t.Errorf("TransientError.Status = %d, want %d", te.Status, http.StatusServiceUnavailable)
	}
}

// TestResendSender_SendsIdempotencyKey confirms a non-empty
// Message.MessageID is sent as the Idempotency-Key header so a
// network-level retry that the upstream already accepted is
// deduplicated inside Resend's 24h replay window.
func TestResendSender_SendsIdempotencyKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc-123"}`))
	}))
	t.Cleanup(srv.Close)

	s, _ := mail.NewResendSender(mail.ResendConfig{
		APIKey: "re_test", From: "ops@example.test", BaseURL: srv.URL,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := s.Send(context.Background(), mail.Message{
		To:        []string{"jane@example.test"},
		Subject:   "Hello",
		TextBody:  "world",
		MessageID: "dunning:acct-123:2026-08-29",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotKey != "dunning:acct-123:2026-08-29" {
		t.Errorf("Idempotency-Key = %q, want dunning:acct-123:2026-08-29", gotKey)
	}
}

// TestResendSender_NoIdempotencyKeyWhenEmpty confirms an empty
// Message.MessageID does NOT add the header — backwards-compat with
// call sites that haven't yet adopted the field.
func TestResendSender_NoIdempotencyKeyWhenEmpty(t *testing.T) {
	var gotKey string
	var hasKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Idempotency-Key")
		_, hasKey = r.Header["Idempotency-Key"]
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc-123"}`))
	}))
	t.Cleanup(srv.Close)

	s, _ := mail.NewResendSender(mail.ResendConfig{
		APIKey: "re_test", From: "ops@example.test", BaseURL: srv.URL,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := s.Send(context.Background(), mail.Message{
		To: []string{"jane@example.test"}, Subject: "x", TextBody: "y",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if hasKey {
		t.Errorf("Idempotency-Key header present (value=%q), want absent when MessageID empty", gotKey)
	}
}

// TestResendSender_PassesCustomHeaders confirms Message.Headers flows
// through to the wire so the quota-warning template can carry
// List-Unsubscribe (issue #246 acceptance item 4, RFC 8058).
// pkg/mail/headers.go builds the header set; this row proves the
// transport doesn't drop them.
//
// Resend's API treats custom HTTP request headers as unrelated to
// the email payload and drops them — so the right channel is the
// `headers` field on the JSON body (ResendRequest.Headers). The
// pre-PR transport set them via req.Header.Set, which looked
// correct in unit tests against the stub but was silently lost
// against the real Resend API. This test pins the JSON-body path
// so a future "simplification" can't regress the wire payload.
func TestResendSender_PassesCustomHeaders(t *testing.T) {
	var gotBody mail.ResendRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc-123"}`))
	}))
	t.Cleanup(srv.Close)

	s, _ := mail.NewResendSender(mail.ResendConfig{
		APIKey: "re_test", From: "ops@example.test", BaseURL: srv.URL,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := s.Send(context.Background(), mail.Message{
		To: []string{"jane@example.test"}, Subject: "x", TextBody: "y",
		Headers: map[string]string{
			"List-Unsubscribe": "<mailto:unsub@example.test>",
		},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got, ok := gotBody.Headers["List-Unsubscribe"]; !ok || got != "<mailto:unsub@example.test>" {
		t.Errorf("ResendRequest.Headers[List-Unsubscribe] = %q (present=%v); want <mailto:unsub@example.test>",
			got, ok)
	}
}

// TestPostmarkSender_429WrapsErrTransient mirrors the Resend 429
// row for Postmark.
func TestPostmarkSender_429WrapsErrTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ErrorCode":401,"Message":"rate limited"}`))
	}))
	t.Cleanup(srv.Close)

	s, _ := mail.NewPostmarkSender(mail.PostmarkConfig{
		ServerToken: "pm_test", From: "ops@example.test", BaseURL: srv.URL,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	err := s.Send(context.Background(), mail.Message{To: []string{"x@y.test"}, Subject: "x"})
	var te *mail.TransientError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want errors.As(*TransientError)", err)
	}
	if te.Status != http.StatusTooManyRequests {
		t.Errorf("TransientError.Status = %d, want %d", te.Status, http.StatusTooManyRequests)
	}
	if te.RetryAfter != 3*time.Second {
		t.Errorf("TransientError.RetryAfter = %s, want 3s", te.RetryAfter)
	}
}

// TestPostmarkSender_NoIdempotencyKey pins the PR #1191 fixup:
// Postmark's HTTP API does NOT support an X-Idempotency-Key
// header — the earlier draft of this PR set the header and the
// upstream silently dropped it, so a network-level retry that
// Postmark already accepted double-charged the customer. MessageID
// is still accepted as input (callers from dunning + quota-warning
// derive a stable id) but the transport MUST NOT send the header.
// Resend keeps honouring MessageID via its own Idempotency-Key
// header (TestResendSender_SendsIdempotencyKey).
func TestPostmarkSender_NoIdempotencyKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Idempotency-Key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"MessageID":"abc","ErrorCode":0,"Message":"OK"}`))
	}))
	t.Cleanup(srv.Close)

	s, _ := mail.NewPostmarkSender(mail.PostmarkConfig{
		ServerToken: "pm_test", From: "ops@example.test", BaseURL: srv.URL,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := s.Send(context.Background(), mail.Message{
		To:        []string{"jane@example.test"},
		Subject:   "x",
		TextBody:  "y",
		MessageID: "quota:acct-7:2026-08-29",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotKey != "" {
		t.Errorf("X-Idempotency-Key = %q, want empty (Postmark API has no idempotency-key feature; PR #1191 fixup removed the misleading header)", gotKey)
	}
}

// TestPostmarkSender_PassesCustomHeadersAsJSONArray pins the
// PR #1191 fixup: Postmark's API surface takes RFC 8058 /
// List-Unsubscribe headers via the JSON body's `Headers` array,
// NOT via HTTP request headers (Resend has parity on HTTP request
// headers, Postmark does not). The transport marshals
// msg.Headers → []PostmarkHeader{Name, Value} so the recipient
// MTA renders them as RFC 5322 headers.
func TestPostmarkSender_PassesCustomHeadersAsJSONArray(t *testing.T) {
	var gotHeaders []mail.PostmarkHeader
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body mail.PostmarkRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		gotHeaders = body.Headers
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"MessageID":"abc","ErrorCode":0,"Message":"OK"}`))
	}))
	t.Cleanup(srv.Close)

	s, _ := mail.NewPostmarkSender(mail.PostmarkConfig{
		ServerToken: "pm_test", From: "ops@example.test", BaseURL: srv.URL,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := s.Send(context.Background(), mail.Message{
		To: []string{"jane@example.test"}, Subject: "x", TextBody: "y",
		Headers: map[string]string{
			"List-Unsubscribe":      "<mailto:unsub@example.test>",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// JSON object order is non-deterministic — match by name.
	got := map[string]string{}
	for _, h := range gotHeaders {
		got[h.Name] = h.Value
	}
	if got["List-Unsubscribe"] != "<mailto:unsub@example.test>" {
		t.Errorf("Headers[List-Unsubscribe] = %q, want <mailto:unsub@example.test>", got["List-Unsubscribe"])
	}
	if got["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Errorf("Headers[List-Unsubscribe-Post] = %q, want List-Unsubscribe=One-Click", got["List-Unsubscribe-Post"])
	}
}

// TestPostmarkSender_NoHeadersOmitsField pins the omitempty on
// PostmarkRequest.Headers: an empty msg.Headers map must NOT add
// a noise `Headers: []` field to the JSON body. The transport
// returns nil from postmarkHeadersFromMap for empty input so
// json.Marshal skips the field.
func TestPostmarkSender_NoHeadersOmitsField(t *testing.T) {
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"MessageID":"abc","ErrorCode":0,"Message":"OK"}`))
	}))
	t.Cleanup(srv.Close)

	s, _ := mail.NewPostmarkSender(mail.PostmarkConfig{
		ServerToken: "pm_test", From: "ops@example.test", BaseURL: srv.URL,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := s.Send(context.Background(), mail.Message{
		To: []string{"jane@example.test"}, Subject: "x", TextBody: "y",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if strings.Contains(string(rawBody), `"Headers":`) {
		t.Errorf("body should not contain Headers field when msg.Headers is empty, got %s", rawBody)
	}
}

// TestTransientError_IsAndAs pins the type contract: a *TransientError
// returned from either transport must satisfy both errors.Is(err,
// ErrTransient) AND errors.As(err, *TransientError). The first is the
// gate the retry decorator uses; the second lets the decorator reach
// RetryAfter. Both are required.
//
// The PR #1191 /code-review surfaced the Err-set path (network
// errors wrapped with %w): the typed Is method must still resolve
// errors.Is(err, ErrTransient) for a TransientError whose Err field
// is non-nil, because Unwrap returns the inner Err first and the
// caller would otherwise walk past ErrTransient without matching.
// errors.As is the only path that worked pre-fix; both must work
// post-fix.
func TestTransientError_IsAndAs(t *testing.T) {
	// Err-nil path (4xx / 5xx from the transport).
	te := &mail.TransientError{Status: 429, RetryAfter: 7 * time.Second}
	if !errors.Is(te, mail.ErrTransient) {
		t.Errorf("TransientError (Err=nil) does not satisfy errors.Is(ErrTransient)")
	}
	var got *mail.TransientError
	if !errors.As(te, &got) {
		t.Errorf("TransientError does not satisfy errors.As(*TransientError)")
	}
	if got.RetryAfter != 7*time.Second {
		t.Errorf("got.RetryAfter = %s, want 7s", got.RetryAfter)
	}
	msg := te.Error()
	if !strings.Contains(msg, "status=429") || !strings.Contains(msg, "retry_after=7s") {
		t.Errorf("Error() = %q, want both status=429 and retry_after=7s", msg)
	}

	// Err-set path (network error wrapped with %w). This is the
	// common transient failure mode (Resend/Postmark HTTP client
	// surfaces a *url.Error). Without the typed Is method,
	// errors.Is walks Unwrap → e.Err and never reaches
	// ErrTransient.
	networkErr := errors.New("dial tcp: i/o timeout")
	teWithErr := &mail.TransientError{Status: 0, Err: networkErr}
	if !errors.Is(teWithErr, mail.ErrTransient) {
		t.Errorf("TransientError (Err set) does not satisfy errors.Is(ErrTransient); the typed Is method must catch ErrTransient before Unwrap walks past it")
	}
	// errors.As must still reach the typed struct (retry decorator
	// reads RetryAfter via the struct, not the sentinel).
	var got2 *mail.TransientError
	if !errors.As(teWithErr, &got2) {
		t.Errorf("TransientError (Err set) does not satisfy errors.As(*TransientError)")
	}
	// errors.Is is the right way to compare error values
	// (errorlint golangci-lint rule) — pointer equality is
	// fragile because TransientError wraps the network error
	// via fmt.Errorf-style chaining rather than storing it
	// directly.
	if !errors.Is(got2.Err, networkErr) {
		t.Errorf("got2.Err = %v, want errors.Is(got2.Err, %v)", got2.Err, networkErr)
	}
	// errors.Is also reaches the wrapped inner error for callers
	// that want the root cause (this path has always worked — the
	// typed Is method is additive, not a replacement for the
	// Unwrap chain).
	if !errors.Is(teWithErr, networkErr) {
		t.Errorf("TransientError (Err set) should still reach Err via the Unwrap chain")
	}
}
