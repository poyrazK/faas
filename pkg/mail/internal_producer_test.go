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

func TestInternalProducerTransportDefaultOffDoesNotReadSecretsOrFallback(t *testing.T) {
	for _, flags := range []struct{ master, producers string }{{"", "1"}, {"true", "1"}, {"1", ""}, {"1", "0"}, {"1", "true"}, {"1", "1 "}} {
		sender, err := mail.SenderFromEnv(func(key string) string {
			switch key {
			case "FAAS_MAIL_TRANSPORT":
				return mail.TransportInternalProducer
			case internalmail.EnableEnv:
				return flags.master
			case internalmail.ProducersEnableEnv:
				return flags.producers
			}
			t.Fatal("disabled producer transport read configuration or tried another transport", key)
			return ""
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if sender != nil || !errors.Is(err, mail.ErrMailerMisconfigured) || !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal("disabled producer transport escaped gate", err)
		}
	}
}

func TestInternalProducerTransportUsesScopedUnixRouteAndStableKey(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	var requests atomic.Int32
	socket := filepath.Join(t.TempDir(), "mail.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("p", 32)
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != internalmail.ProducersPath+"/auth/messages" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Idempotency-Key") != "stable-producer-key" {
			t.Error("producer transport lost its scoped route, credential or replay key")
		}
		var input internalmail.SendRequest
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.From != "notify@example.test" || input.Text != "body" {
			t.Error("producer transport changed message fields")
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
		t.Fatal("producer transport requested a broad or unrelated credential", key)
		return ""
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	message := mail.Message{To: []string{"one@example.net"}, Subject: "hello", TextBody: "body", MessageID: "stable-producer-key"}
	if err := sender.Send(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	enabled.Store(false)
	if err := sender.Send(t.Context(), message); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("producer kill switch was not rechecked", err)
	}
	if requests.Load() != 1 {
		t.Fatal("disabled producer transport opened its socket")
	}
}

func TestInternalProducerTransportRejectsPathOrRemoteTargets(t *testing.T) {
	for _, id := range []string{"", "../auth", "auth/other", "Auth", strings.Repeat("a", 33)} {
		if _, err := mail.NewInternalProducerSender("/run/faas/maild.sock", id, strings.Repeat("p", 32), "notify@example.test", func() bool { return true }); err == nil {
			t.Fatal("invalid producer ID accepted")
		}
	}
	if _, err := mail.NewInternalProducerSender("https://remote.test", "auth", strings.Repeat("p", 32), "notify@example.test", func() bool { return true }); err == nil {
		t.Fatal("remote producer transport accepted")
	}
}
