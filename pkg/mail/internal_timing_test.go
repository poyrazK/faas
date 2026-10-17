package mail_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
)

func TestTimedSendRequiresExplicitCapabilityAndGate(t *testing.T) {
	expires := time.Now().Add(time.Minute)
	options := internalmail.MessageTiming{ExpiresAt: &expires}
	message := mail.Message{To: []string{"one@example.net"}, Subject: "hello", TextBody: "body", MessageID: "stable-timed-key"}
	if err := mail.SendTimed(t.Context(), mail.NoopSender{}, message, options); !errors.Is(err, mail.ErrTimedTransportRequired) {
		t.Fatal("unsupported transport silently ignored timing", err)
	}
	sender, err := mail.NewInternalSender(filepath.Join(t.TempDir(), "missing.sock"), strings.Repeat("s", 32), "notify@example.test", func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if err := mail.SendTimed(t.Context(), sender, message, options); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("constructor implicitly enabled timing or opened a socket", err)
	}
	enabled := sender.WithScheduling(func() bool { return true })
	message.MessageID = ""
	if err := mail.SendTimed(t.Context(), enabled, message, options); err == nil || errors.Is(err, mail.ErrTransient) {
		t.Fatal("timed submission without stable identity was attempted", err)
	}
}

func TestTimedFactorySendUsesScopedRouteAndPreservesAbsoluteOptions(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	var requests atomic.Int32
	socket := filepath.Join(t.TempDir(), "mail.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	sendAt, expires := time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)
	token := strings.Repeat("p", 32)
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != internalmail.ProducersPath+"/auth/messages" || r.Header.Get("Idempotency-Key") != "stable-timed-key" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("timed send lost producer authority or replay key")
		}
		var input internalmail.SendRequest
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.SendAt == nil || input.ExpiresAt == nil || !input.SendAt.Equal(sendAt) || !input.ExpiresAt.Equal(expires) {
			t.Error("timed send changed absolute options")
		}
		w.WriteHeader(http.StatusAccepted)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	sender, err := mail.SenderFromEnv(func(key string) string {
		switch key {
		case "FAAS_MAIL_TRANSPORT":
			return mail.TransportInternalProducer
		case internalmail.EnableEnv, internalmail.ProducersEnableEnv:
			return "1"
		case internalmail.SchedulingEnableEnv:
			if enabled.Load() {
				return "1"
			}
			return "0"
		case "FAAS_INTERNAL_MAIL_SOCKET":
			return socket
		case "FAAS_INTERNAL_MAIL_PRODUCER_ID":
			return "auth"
		case "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN":
			return token
		case "FAAS_MAIL_FROM":
			return "notify@example.test"
		}
		t.Fatal("timed transport read unrelated configuration", key)
		return ""
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	message := mail.Message{To: []string{"one@example.net"}, Subject: "hello", TextBody: "body", MessageID: "stable-timed-key"}
	options := internalmail.MessageTiming{SendAt: &sendAt, ExpiresAt: &expires}
	if err := mail.SendTimed(t.Context(), sender, message, options); err != nil {
		t.Fatal(err)
	}
	enabled.Store(false)
	if err := mail.SendTimed(t.Context(), sender, message, options); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("timing kill switch was ignored", err)
	}
	if requests.Load() != 1 {
		t.Fatal("disabled timing submitted another request")
	}
}
