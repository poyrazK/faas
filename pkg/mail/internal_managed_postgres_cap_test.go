// adr: 20261025094915 — the compute cap warning has its own sender switch.
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

func TestManagedPostgresCapOutboxSenderMarksTemplateAndHonorsIndependentGate(t *testing.T) {
	var on atomic.Bool
	var requests atomic.Int32
	socket := filepath.Join(t.TempDir(), "mail.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get(internalmail.ProducerOutboxTemplateHeader) != internalmail.ProducerOutboxManagedPostgresCapTemplate || r.Header.Get(internalmail.ProducerOutboxGenerationHeader) != uuid.Nil.String() || r.Header.Get("Idempotency-Key") != "managed-postgres-cap:stable" {
			t.Error("managed-postgres-cap transport lost its explicit template, generation or stable key")
		}
		w.WriteHeader(http.StatusAccepted)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	sender, err := mail.NewInternalProducerSender(socket, "auth", strings.Repeat("p", 32), "notify@example.test", func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	sender = sender.WithScheduling(func() bool { return true }).WithOutbox(func() bool { return true }).WithManagedPostgresCapOutbox(on.Load)
	expires := time.Now().Add(time.Hour)
	message := mail.Message{To: []string{"one@example.net"}, Subject: "Verify", TextBody: "test", MessageID: "managed-postgres-cap:stable"}
	if err := sender.SendTimedWithRecovery(t.Context(), message, internalmail.MessageTiming{ExpiresAt: &expires}, uuid.Nil.String()); !errors.Is(err, internalmail.ErrDisabled) || requests.Load() != 0 {
		t.Fatal("disabled managed-postgres-cap sender performed I/O", err)
	}
	on.Store(true)
	if err := sender.SendTimedWithRecovery(t.Context(), message, internalmail.MessageTiming{ExpiresAt: &expires}, uuid.Nil.String()); err != nil || requests.Load() != 1 {
		t.Fatal("explicit managed-postgres-cap handoff failed", err)
	}
	on.Store(false)
	if err := sender.SendTimedWithRecovery(t.Context(), message, internalmail.MessageTiming{ExpiresAt: &expires}, uuid.Nil.String()); !errors.Is(err, internalmail.ErrDisabled) || requests.Load() != 1 {
		t.Fatal("revocation did not stop subsequent handoff", err)
	}
}
