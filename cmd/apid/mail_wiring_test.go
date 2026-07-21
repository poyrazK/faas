// Mailer wire-up tests (PR fix for gap G4 — Resend/Postmark transport
// selection at apid startup). These verify the env-driven factory wires
// the right pkg/mail.Sender implementation through newMailerAdapter,
// plus the magic-link login path actually delivers end-to-end.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// recordingSender captures every outbound message so the test can
// assert on the magic-link payload. Matches the pkg/mail.Sender
// interface so it slots straight into newMailerAdapter.
type recordingSender struct {
	mu       sync.Mutex
	messages []mail.Message
	err      error // optional; non-nil makes every Send return it
}

func (r *recordingSender) Send(_ context.Context, m mail.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, m)
	return r.err
}

func (r *recordingSender) snapshot() []mail.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]mail.Message, len(r.messages))
	copy(out, r.messages)
	return out
}

// TestMailFactory_PicksCorrectTransport covers every FAAS_MAIL_TRANSPORT
// branch the factory exposes. The output of mail.SenderFromEnv must
// have the expected concrete type for each env shape, and the
// mailAdapter we wrap it in must forward a probe message through.
func TestMailFactory_PicksCorrectTransport(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		wantType string // fmt.Sprintf("%T", sender) for the chosen transport
	}{
		{
			name:     "unset-transport-defaults-to-log",
			env:      map[string]string{},
			wantType: "*mail.LogSender",
		},
		{
			name:     "explicit-log",
			env:      map[string]string{"FAAS_MAIL_TRANSPORT": "log"},
			wantType: "*mail.LogSender",
		},
		{
			name:     "noop",
			env:      map[string]string{"FAAS_MAIL_TRANSPORT": "noop"},
			wantType: "mail.NoopSender",
		},
		{
			name: "resend-with-key",
			env: map[string]string{
				"FAAS_MAIL_TRANSPORT":      "resend",
				"FAAS_MAIL_RESEND_API_KEY": "re_test_key",
				"FAAS_MAIL_FROM":           "noreply@example.com",
			},
			wantType: "*mail.ResendSender",
		},
		{
			name:     "resend-without-key-falls-back-to-log",
			env:      map[string]string{"FAAS_MAIL_TRANSPORT": "resend"},
			wantType: "*mail.LogSender",
		},
		{
			name: "postmark-with-token",
			env: map[string]string{
				"FAAS_MAIL_TRANSPORT":      "postmark",
				"FAAS_MAIL_POSTMARK_TOKEN": "pm_test_token",
				"FAAS_MAIL_FROM":           "noreply@example.com",
			},
			wantType: "*mail.PostmarkSender",
		},
		{
			name:     "postmark-without-token-falls-back-to-log",
			env:      map[string]string{"FAAS_MAIL_TRANSPORT": "postmark"},
			wantType: "*mail.LogSender",
		},
		{
			name:     "bogus-transport-falls-back-to-log",
			env:      map[string]string{"FAAS_MAIL_TRANSPORT": "carrier-pigeon"},
			wantType: "*mail.LogSender",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Use t.Setenv so Go's test framework cleans up after us;
			// t.Setenv on a non-existent key unsets it, matching the
			// factory's getenv behaviour for "FAAS_MAIL_TRANSPORT=" → log.
			for _, k := range []string{
				"FAAS_MAIL_TRANSPORT",
				"FAAS_MAIL_RESEND_API_KEY",
				"FAAS_MAIL_FROM",
				"FAAS_MAIL_POSTMARK_TOKEN",
			} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			m := mail.SenderFromEnv(os.Getenv, log)
			if m == nil {
				t.Fatal("SenderFromEnv returned nil")
			}
			gotType := fmt.Sprintf("%T", m)
			if gotType != tc.wantType {
				t.Errorf("transport = %s, want %s", gotType, tc.wantType)
			}

			// The mailAdapter path is what runWithDeps actually wires;
			// exercise the adapter's nil-collapse contract on every
			// branch (nil → noopMailer — non-nil → wraps the sender).
			// For live transports (Resend/Postmark) the sender itself
			// would dial out on a probe; we don't probe through those,
			// we only prove the adapter accepts the chosen transport
			// without panicking and returns a non-nil Mailer.
			if got := newMailerAdapter(m); got == nil {
				t.Errorf("newMailerAdapter(%s) returned nil", tc.wantType)
			}
		})
	}
}

// TestMailAdapter_ForwardsFields is the round-trip that proves a
// message body written through apid's Mailer ends up in the wrapped
// pkg/mail.Sender with every field intact.
func TestMailAdapter_ForwardsFields(t *testing.T) {
	rec := &recordingSender{}
	a := newMailerAdapter(rec)
	if a == nil {
		t.Fatal("adapter is nil")
	}
	want := Message{
		To:       []string{"alice@example.com", "bob@example.com"},
		Subject:  "magic link",
		TextBody: "Click here",
		HTMLBody: "<p>Click here</p>",
	}
	if err := a.Send(context.Background(), want); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := rec.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	g := got[0]
	if g.Subject != want.Subject || g.TextBody != want.TextBody || g.HTMLBody != want.HTMLBody {
		t.Errorf("forward lost fields: %+v", g)
	}
	if len(g.To) != 2 || g.To[0] != want.To[0] || g.To[1] != want.To[1] {
		t.Errorf("forward lost recipients: %+v", g.To)
	}
}

// TestMailAdapter_NilSenderIsNoop guards the newMailerAdapter contract:
// a nil underlying sender collapses to noopMailer{}, so callers that
// never nil-check (the rest of the apid codebase) stay safe.
func TestMailAdapter_NilSenderIsNoop(t *testing.T) {
	a := newMailerAdapter(nil)
	if err := a.Send(context.Background(), Message{Subject: "x"}); err != nil {
		t.Errorf("nil-sender adapter should swallow, got %v", err)
	}
}

// TestMailAdapter_SurfacesSenderError makes sure upstream failures
// reach the caller — a quota warning that fails to send MUST NOT be
// silently dropped.
func TestMailAdapter_SurfacesSenderError(t *testing.T) {
	rec := &recordingSender{err: mail.ErrTransient}
	a := newMailerAdapter(rec)
	err := a.Send(context.Background(), Message{Subject: "quota-warn"})
	if !errors.Is(err, mail.ErrTransient) {
		t.Errorf("expected ErrTransient, got %v", err)
	}
}

// TestMagicLinkDeliveredThroughMailer is the G4 closure end-to-end
// test: it boots a real handler stack (no listener), injects a
// recordingSender as srv.mailer via the adapter, fires POST /login,
// and asserts the magic-link email actually reached the wire. Without
// the newMailerAdapter path, the production code only ever hit
// newLogMailer (which calls slog) and this message would never appear.
func TestMagicLinkDeliveredThroughMailer(t *testing.T) {
	const email = "user@example.com"
	// The login handler only dispatches an email when AccountByEmail
	// resolves — the unknown-email branch is silent (handlers_auth.go:
	// postLogin). Seed the account so the happy path fires.
	store := state.NewMemStore()
	if _, err := store.CreateAccount(context.Background(), email, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	rec := &recordingSender{}
	srv := newServer(store, discardLogger(), "example.com", noopNotifier{})
	srv.mailer = newMailerAdapter(rec)
	h := srv.handler()

	// POST /login takes a form-encoded email (handlers_auth.go::postLogin).
	form := url.Values{"email": {email}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recHTTP := httptest.NewRecorder()
	h.ServeHTTP(recHTTP, req)
	// 200 (check-email page rendered) is the success contract.
	if recHTTP.Code != http.StatusOK {
		t.Fatalf("/login status = %d body = %s", recHTTP.Code, recHTTP.Body.String())
	}

	msgs := rec.snapshot()
	if len(msgs) == 0 {
		t.Fatal("no message recorded; magic link not delivered through mailAdapter")
	}
	m := msgs[0]
	if len(m.To) == 0 || m.To[0] != email {
		t.Errorf("recipient = %v, want %s", m.To, email)
	}
	// Pin the literal subject the handler writes (cmd/apid/handlers_auth.go:
	// postLogin). Marketing copy is deliberate; if it ever changes, this
	// test breaks loudly so the change shows up in review.
	if m.Subject != "Sign in to onebox faas" {
		t.Errorf("subject = %q, want %q", m.Subject, "Sign in to onebox faas")
	}
	if !strings.Contains(m.TextBody, "verify?token=") {
		t.Errorf("body missing token URL: %q", m.TextBody)
	}
}
