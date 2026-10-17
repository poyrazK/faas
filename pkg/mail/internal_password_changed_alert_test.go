package mail_test

import (
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
)

func TestPasswordChangedAlertOutboxSenderMarksTemplateAndHonorsIndependentGate(t *testing.T) {
	var on atomic.Bool
	var requests atomic.Int32
	socket := filepath.Join(t.TempDir(), "mail.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get(internalmail.ProducerOutboxTemplateHeader) != internalmail.ProducerOutboxPasswordChangedAlertTemplate || r.Header.Get(internalmail.ProducerOutboxGenerationHeader) != uuid.Nil.String() || r.Header.Get("Idempotency-Key") != "password-changed:stable" {
			t.Error("password-changed transport lost its explicit template, generation or stable key")
		}
		w.WriteHeader(http.StatusAccepted)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	sender, err := mail.NewInternalProducerSender(socket, "auth", strings.Repeat("p", 32), "notify@example.test", func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	sender = sender.WithScheduling(func() bool { return true }).WithOutbox(func() bool { return true }).WithPasswordChangedAlertOutbox(on.Load)
	expires := time.Now().Add(time.Hour)
	message := mail.Message{To: []string{"one@example.net"}, Subject: "Verify", TextBody: "test", MessageID: "password-changed:stable"}
	if err := sender.SendTimedWithRecovery(t.Context(), message, internalmail.MessageTiming{ExpiresAt: &expires}, uuid.Nil.String()); !errors.Is(err, internalmail.ErrDisabled) || requests.Load() != 0 {
		t.Fatal("disabled password-changed sender performed I/O", err)
	}
	on.Store(true)
	if err := sender.SendTimedWithRecovery(t.Context(), message, internalmail.MessageTiming{ExpiresAt: &expires}, uuid.Nil.String()); err != nil || requests.Load() != 1 {
		t.Fatal("explicit password-changed handoff failed", err)
	}
	on.Store(false)
	if err := sender.SendTimedWithRecovery(t.Context(), message, internalmail.MessageTiming{ExpiresAt: &expires}, uuid.Nil.String()); !errors.Is(err, internalmail.ErrDisabled) || requests.Load() != 1 {
		t.Fatal("revocation did not stop subsequent handoff", err)
	}
}

func TestPasswordChangedAlertBodyKeepsOriginalEventTimeAndExcludesLinks(t *testing.T) {
	at := time.Date(2026, time.October, 17, 10, 5, 17, 0, time.FixedZone("offset", 3600))
	for _, method := range []string{"dashboard", "password_reset"} {
		subject, body := mail.PasswordChangedBodyV1("one@example.net", method, at)
		if subject != "Password updated on your Gregale account" || !strings.Contains(body, "2026-10-17 09:05:17 UTC") || !strings.Contains(body, "one@example.net") || strings.Contains(body, "?token=") {
			t.Fatal("password alert changed event time or included a link")
		}
		want := "from your account settings"
		if method == "password_reset" {
			want = "using a password reset link"
		}
		if !strings.Contains(body, want) {
			t.Fatal("alert lost event source")
		}
	}
}
