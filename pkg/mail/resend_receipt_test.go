package mail_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/mail"
)

func TestResendSendWithReceipt(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			id := uuid.NewString()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Idempotency-Key") != "stable" {
					t.Error("receipt send changed provider key")
				}
				if valid {
					_, _ = fmt.Fprintf(w, `{"id":%q}`, id)
				} else {
					_, _ = w.Write([]byte(`{"accepted":true}`))
				}
			}))
			defer provider.Close()
			s, err := mail.NewResendSender(mail.ResendConfig{APIKey: "local-only", From: "fixture@example.test", BaseURL: provider.URL, HTTPClient: provider.Client(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				t.Fatal(err)
			}
			receiptSender := s.(interface {
				SendWithReceipt(context.Context, mail.Message) (string, error)
			})
			got, err := receiptSender.SendWithReceipt(t.Context(), mail.Message{To: []string{"fictional@example.test"}, MessageID: "stable"})
			if valid && (got != id || err != nil) {
				t.Fatal(got, err)
			}
			if !valid {
				var transient *mail.TransientError
				if !errors.As(err, &transient) || got != "" {
					t.Fatal("ambiguous acceptance was not retryable", got, err)
				}
			}
			// Existing platform mail retains its successful error-only send contract.
			if err = s.Send(t.Context(), mail.Message{MessageID: "stable"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
