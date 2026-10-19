package mail_test

import (
	"context"
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

	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
)

func TestInternalTransportIsExplicitlyGated(t *testing.T) {
	for _, dev := range []string{"", "1"} {
		env := map[string]string{"FAAS_MAIL_TRANSPORT": "internal", "FAAS_DEV": dev, "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_TOKEN": strings.Repeat("s", 32)}
		sender, err := mail.SenderFromEnv(func(key string) string { return env[key] }, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if sender != nil || !errors.Is(err, mail.ErrMailerMisconfigured) || !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal("internal transport escaped gate", sender, err)
		}
	}
}

func TestInternalTransportUsesOnlyUnixSocketAndHonorsKillSwitch(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	var requests atomic.Int32
	socket := filepath.Join(t.TempDir(), "mail.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("s", 32)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != internalmail.APIPath+"/messages" || r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Idempotency-Key") != "stable-message-id" {
			t.Error("invalid internal submission")
		}
		defer func() { _ = r.Body.Close() }()
		var payload internalmail.SendRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.From != "notify@example.test" || payload.Text != "body" || payload.Headers["Reply-To"] != "help@example.test" {
			t.Error("message fields lost")
		}
		w.WriteHeader(http.StatusAccepted)
	})}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	sender, err := mail.NewInternalSender(socket, token, "notify@example.test", enabled.Load)
	if err != nil {
		t.Fatal(err)
	}
	message := mail.Message{To: []string{"one@example.net"}, Subject: "hello", TextBody: "body", MessageID: "stable-message-id", Headers: map[string]string{"Reply-To": "help@example.test"}}
	if err := sender.Send(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	enabled.Store(false)
	if err := sender.Send(t.Context(), message); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("submission after disable")
	}
}

func TestInternalTransportDoesNotRetryAmbiguousSubmissionWithoutStableKey(t *testing.T) {
	sender, err := mail.NewInternalSender(filepath.Join(t.TempDir(), "missing.sock"), strings.Repeat("s", 32), "notify@example.test", func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	msg := mail.Message{To: []string{"one@example.net"}, Subject: "hello", TextBody: "body"}
	if err := sender.Send(context.Background(), msg); err == nil || errors.Is(err, mail.ErrTransient) {
		t.Fatal("ambiguous unkeyed submission became retryable", err)
	}
	msg.MessageID = "stable"
	if err := sender.Send(context.Background(), msg); !errors.Is(err, mail.ErrTransient) {
		t.Fatal("stable retry key was not retained", err)
	}
}
