package apidserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlEmailFeedbackRoutes(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	s := apidserver.NewServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{})
	path := "https://example.test/v1/product-control/email/resend-events"
	call := func(method string, body []byte, signed bool, want int) {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer owner-does-not-authorize-provider-events")
		if signed {
			id, at := "msg_fixture", strconv.FormatInt(time.Now().Unix(), 10)
			r.Header.Set(mail.ResendIDHeader, id)
			r.Header.Set(mail.ResendTimestampHeader, at)
			r.Header.Set(mail.ResendSignatureHeader, mail.SignResendForTest(body, s.ProductControl().EmailWebhookSecret, id, at))
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s feedback: %d want %d %s", method, w.Code, want, w.Body.String())
		}
	}
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
		call(method, nil, false, 404)
	}
	s.WithProductControlEnabled(true)
	call("POST", nil, false, 503)
	call("GET", nil, false, 405)
	s.ProductControl().EmailWebhookSecret = mail.RandomResendSecretForTest()
	body, _ := json.Marshal(map[string]any{"type": "email.delivered", "created_at": time.Now().UTC(), "data": map[string]string{"email_id": uuid.NewString()}})
	call("POST", body, false, 400)
	call("POST", body, true, 200)
	call("POST", body, true, 200)
	call("POST", []byte(`{"type":"email.bounced","created_at":"2026-10-17T10:00:00Z","data":{"email":"private@example.test"}}`), true, 400)
	s.WithProductControlEnabled(false)
	call("POST", body, true, 404)
}
func TestProductControlEmailFeedbackConfiguration(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	for _, secret := range []string{"", mail.RandomResendSecretForTest(), "not-a-signing-secret", "whsec_YQ=="} {
		s := apidserver.NewServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{}).WithProductControlEnabled(true)
		err := s.ConfigureProductEmail(func(key string) string {
			if key == "FAAS_PRODUCT_CONTROL_EMAIL_WEBHOOK_SECRET" {
				return secret
			}
			return ""
		})
		wantError := secret == "not-a-signing-secret" || secret == "whsec_YQ=="
		if (err != nil) != wantError {
			t.Fatal("unexpected signing-secret configuration result", err)
		}
		if s.ProductControl().EmailTransport != nil {
			t.Fatal("feedback configuration enabled real sending")
		}
	}
}
