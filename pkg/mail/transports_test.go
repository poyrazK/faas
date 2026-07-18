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

// TestSenderFromEnv_PicksLog confirms the default (no FAAS_MAIL_TRANSPORT)
// returns a LogSender that emits one record per Send.
func TestSenderFromEnv_PicksLog(t *testing.T) {
	getenv := func(string) string { return "" }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := mail.SenderFromEnv(getenv, log)
	if _, ok := s.(*mail.LogSender); !ok {
		t.Errorf("default transport = %T, want *mail.LogSender", s)
	}
}

// TestSenderFromEnv_PicksNoop confirms FAAS_MAIL_TRANSPORT=noop
// returns a NoopSender.
func TestSenderFromEnv_PicksNoop(t *testing.T) {
	getenv := func(k string) string {
		if k == "FAAS_MAIL_TRANSPORT" {
			return "noop"
		}
		return ""
	}
	s := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, ok := s.(mail.NoopSender); !ok {
		t.Errorf("transport = %T, want mail.NoopSender", s)
	}
}

// TestSenderFromEnv_ResendFallsBackOnMissingAPIKey confirms a
// misconfigured resend falls back to log + warning rather than
// booting a half-working mailer.
func TestSenderFromEnv_ResendFallsBackOnMissingAPIKey(t *testing.T) {
	getenv := func(k string) string {
		switch k {
		case "FAAS_MAIL_TRANSPORT":
			return "resend"
		case "FAAS_MAIL_FROM":
			return "ops@example.test"
		}
		return ""
	}
	s := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, ok := s.(*mail.LogSender); !ok {
		t.Errorf("misconfig transport = %T, want *mail.LogSender (fallback)", s)
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
	s := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, ok := s.(*mail.ResendSender); !ok {
		t.Errorf("transport = %T, want *mail.ResendSender", s)
	}
}

// TestSenderFromEnv_UnknownTransportFallsBack confirms an
// unrecognised transport name falls back to log + warning.
func TestSenderFromEnv_UnknownTransportFallsBack(t *testing.T) {
	getenv := func(k string) string {
		if k == "FAAS_MAIL_TRANSPORT" {
			return "carrier-pigeon"
		}
		return ""
	}
	s := mail.SenderFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, ok := s.(*mail.LogSender); !ok {
		t.Errorf("unknown transport = %T, want *mail.LogSender (fallback)", s)
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
