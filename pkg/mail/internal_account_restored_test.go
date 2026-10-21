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

func TestAccountRestoredOutboxSenderMarksTemplateAndHonorsIndependentGate(t *testing.T) {
	var on atomic.Bool
	var requests atomic.Int32
	socket := filepath.Join(t.TempDir(), "mail.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get(internalmail.ProducerOutboxTemplateHeader) != internalmail.ProducerOutboxAccountRestoredTemplate || r.Header.Get(internalmail.ProducerOutboxGenerationHeader) != uuid.Nil.String() || r.Header.Get("Idempotency-Key") != "account-restored:stable" {
			t.Error("account-restored transport lost its explicit template, generation or stable key")
		}
		w.WriteHeader(http.StatusAccepted)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	sender, err := mail.NewInternalProducerSender(socket, "auth", strings.Repeat("p", 32), "notify@example.test", func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	sender = sender.WithScheduling(func() bool { return true }).WithOutbox(func() bool { return true }).WithAccountRestoredOutbox(on.Load)
	expires := time.Now().Add(time.Hour)
	message := mail.Message{To: []string{"one@example.net"}, Subject: "Verify", TextBody: "test", MessageID: "account-restored:stable"}
	if err := sender.SendTimedWithRecovery(t.Context(), message, internalmail.MessageTiming{ExpiresAt: &expires}, uuid.Nil.String()); !errors.Is(err, internalmail.ErrDisabled) || requests.Load() != 0 {
		t.Fatal("disabled account-restored sender performed I/O", err)
	}
	on.Store(true)
	if err := sender.SendTimedWithRecovery(t.Context(), message, internalmail.MessageTiming{ExpiresAt: &expires}, uuid.Nil.String()); err != nil || requests.Load() != 1 {
		t.Fatal("explicit account-restored handoff failed", err)
	}
	on.Store(false)
	if err := sender.SendTimedWithRecovery(t.Context(), message, internalmail.MessageTiming{ExpiresAt: &expires}, uuid.Nil.String()); !errors.Is(err, internalmail.ErrDisabled) || requests.Load() != 1 {
		t.Fatal("revocation did not stop subsequent handoff", err)
	}
}

func TestAccountRestoredBodyKeepsOriginalRecoveryTime(t *testing.T) {
	at := time.Date(2026, time.October, 17, 11, 1, 9, 0, time.FixedZone("offset", 3600))
	subject, body := mail.AccountRestoredBodyV1("one@example.net", at)
	if subject != "Your Gregale account is back in good standing" || !strings.Contains(body, "2026-10-17 10:01:09 UTC") || !strings.Contains(body, "one@example.net") || strings.Contains(body, "?token=") || strings.Contains(body, "meterd") {
		t.Fatal("recovery confirmation lost its original timestamp or exposed implementation details")
	}
}
