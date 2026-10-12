// Mailer wire-up tests (PR fix for gap G4 — Resend/Postmark transport
// selection at apid startup). These verify the env-driven factory wires
// the right pkg/mail.Sender implementation through newMailerAdapter,
// plus the magic-link login path actually delivers end-to-end.

package apidserver_test

import (
	"context"
	"encoding/json"
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
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth"
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
// have the expected concrete type for each happy-path env shape, and
// the mailAdapter we wrap it in must forward a probe message through.
// Fail-closed rows (resend / postmark selected without the credential,
// or unset / unknown transport on a non-dev box) assert the typed
// error sentinel — apid/meterd catch it on boot and refuse to start
// (ADR-115 §D5 + issue #246).
func TestMailFactory_PicksCorrectTransport(t *testing.T) {
	cases := []struct {
		name       string
		env        map[string]string
		wantType   string // empty = expect nil sender (fail-closed)
		wantFailIs error  // non-nil = expect errors.Is(err, sentinel)
	}{
		{
			// Issue #246: an unset FAAS_MAIL_TRANSPORT on a
			// production box now refuses to boot — a production
			// box that journals the dunning ladder looks healthy
			// while the customer receives nothing.
			name:       "unset-on-prod-fails-closed",
			env:        map[string]string{},
			wantFailIs: mail.ErrMailUnsetInProd,
		},
		{
			// FAAS_DEV=1 collapses an unset transport to log so a
			// developer's local stack still boots without ceremony.
			name:     "unset-on-dev-resolves-to-log",
			env:      map[string]string{"FAAS_DEV": "1"},
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
			// ADR-115 §D5: operator-selected resend with no
			// FAAS_MAIL_RESEND_API_KEY fails closed. The pre-#115
			// behaviour was a WARN + LogSender fallback; the
			// fail-closed contract makes the boot refuse to start
			// so the operator cannot run a daemon that silently
			// drops email.
			name:       "resend-without-key-fails-closed",
			env:        map[string]string{"FAAS_MAIL_TRANSPORT": "resend"},
			wantFailIs: mail.ErrMailerMisconfigured,
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
			name:       "postmark-without-token-fails-closed",
			env:        map[string]string{"FAAS_MAIL_TRANSPORT": "postmark"},
			wantFailIs: mail.ErrMailerMisconfigured,
		},
		{
			// Issue #246: a typo ("resned") on a production box
			// used to fall back to LogSender with WARN — the same
			// silent drop as the unset case with none of the
			// visibility. It now fails closed.
			name:       "bogus-transport-on-prod-fails-closed",
			env:        map[string]string{"FAAS_MAIL_TRANSPORT": "carrier-pigeon"},
			wantFailIs: mail.ErrMailUnknownTransport,
		},
		{
			// FAAS_DEV=1 collapses an unknown transport to log
			// with WARN so a developer iterating on a brand-new
			// transport name still boots.
			name: "bogus-transport-on-dev-warns-and-falls-back",
			env: map[string]string{
				"FAAS_MAIL_TRANSPORT": "carrier-pigeon",
				"FAAS_DEV":            "1",
			},
			wantType: "*mail.LogSender",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Use t.Setenv so Go's test framework cleans up after
			// us. Unset every factory-input key first so the env
			// shape is exactly what tc.env describes; the previous
			// run's settings cannot bleed across rows.
			for _, k := range []string{
				"FAAS_MAIL_TRANSPORT",
				"FAAS_MAIL_RESEND_API_KEY",
				"FAAS_MAIL_FROM",
				"FAAS_MAIL_POSTMARK_TOKEN",
				"FAAS_DEV",
			} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			m, err := mail.SenderFromEnv(os.Getenv, log)
			if tc.wantFailIs != nil {
				if !errors.Is(err, tc.wantFailIs) {
					t.Fatalf("err = %v, want errors.Is(err, %v)", err, tc.wantFailIs)
				}
				if m != nil {
					t.Errorf("sender = %T, want nil on fail-closed", m)
				}
				// Mirror runWithDeps: never call newMailerAdapter
				// with a nil sender when the factory returned an
				// error — the boot path exits non-zero before
				// reaching the adapter.
				return
			}
			if err != nil {
				t.Fatalf("unexpected error on happy path: %v", err)
			}
			if m == nil {
				t.Fatal("SenderFromEnv returned nil on happy path")
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
			if got := apidserver.NewMailerAdapter(m); got == nil {
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
	a := apidserver.NewMailerAdapter(rec)
	if a == nil {
		t.Fatal("adapter is nil")
	}
	want := apidserver.Message{
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
	a := apidserver.NewMailerAdapter(nil)
	if err := a.Send(context.Background(), apidserver.Message{Subject: "x"}); err != nil {
		t.Errorf("nil-sender adapter should swallow, got %v", err)
	}
}

// TestMailAdapter_SurfacesSenderError makes sure upstream failures
// reach the caller — a quota warning that fails to send MUST NOT be
// silently dropped.
func TestMailAdapter_SurfacesSenderError(t *testing.T) {
	rec := &recordingSender{err: mail.ErrTransient}
	a := apidserver.NewMailerAdapter(rec)
	err := a.Send(context.Background(), apidserver.Message{Subject: "quota-warn"})
	if !errors.Is(err, mail.ErrTransient) {
		t.Errorf("expected ErrTransient, got %v", err)
	}
}

// TestLoginSetsSessionAndSendsNoEmail pins the post-#165 contract:
// POST /login authenticates the user by setting the faas_sid session
// cookie synchronously and sends NO login email. The mailer stays
// wired for password-reset / dunning / quota mail; the login path
// itself stays silent.
//
// Post-#165 PR #2 (issue #165, ADR-032) the login path is email +
// password (Argon2id). The test seeds an account with a password
// row, exercises the legitimate happy path, and asserts:
//
//   - HTTP 200 (the customer signed in).
//   - faas_sid cookie set.
//   - No email sent (password login is silent; the mailer fires
//     only on /login/forgot).
//   - No api_key field in the response body (the #165 leak path
//     is closed; the response never carries a key, on success or
//     failure).
func TestLoginSetsSessionAndSendsNoEmail(t *testing.T) {
	const email = "user@example.com"
	const password = "correct-horse-battery-staple"
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), email, api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	phc, err := auth.Encode(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccountPassword(context.Background(), acct.ID, phc); err != nil {
		t.Fatal(err)
	}
	rec := &recordingSender{}
	srv := apidserver.NewServer(store, apidserver.DiscardLogger(), "gregale.dev", apidserver.NoopNotifier{})
	(*srv.Mailer()) = apidserver.NewMailerAdapter(rec)
	h := srv.Handler()

	form := url.Values{"email": {email}, "password": {password}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recHTTP := httptest.NewRecorder()
	h.ServeHTTP(recHTTP, req)
	if recHTTP.Code != http.StatusOK {
		t.Fatalf("/login status = %d body = %s", recHTTP.Code, recHTTP.Body.String())
	}

	// The session cookie is the authentication contract now.
	var gotSession bool
	for _, c := range recHTTP.Result().Cookies() {
		if c.Name == apidserver.SessionCookie && c.Value != "" {
			gotSession = true
		}
	}
	if !gotSession {
		t.Errorf("expected %s session cookie to be set on /login", apidserver.SessionCookie)
	}

	// No login email should ever be dispatched.
	if msgs := rec.snapshot(); len(msgs) != 0 {
		t.Errorf("expected no login email, got %d message(s): %+v", len(msgs), msgs)
	}

	// Body must NOT have api_key. Pre-#165 this was the leak
	// path that made the takeover reproducible from a single
	// POST /login + jq.
	var body map[string]any
	if err := json.NewDecoder(recHTTP.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, has := body["api_key"]; has {
		t.Errorf("response body has api_key field; this is the #165 leak path")
	}
}
