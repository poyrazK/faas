package product

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/productemail"
)

func (f *emailFixture) feedback(t *testing.T, body []byte, tamper string, want int) {
	t.Helper()
	r := httptest.NewRequest("POST", "https://portal.example/v1/product-control/email/resend-events", bytes.NewReader(body))
	id, at := "msg_fixture", strconv.FormatInt(time.Now().Unix(), 10)
	if tamper == "stale" {
		at = strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
	}
	sig := mail.SignResendForTest(body, f.service.EmailWebhookSecret, id, at)
	if tamper == "signature" {
		sig += "bad"
	}
	if tamper == "body" {
		r.Body = http.NoBody
	}
	r.Header.Set(mail.ResendIDHeader, id)
	r.Header.Set(mail.ResendTimestampHeader, at)
	r.Header.Set(mail.ResendSignatureHeader, sig)
	w := httptest.NewRecorder()
	f.service.Gate(f.service.EmailFeedback)(w, r)
	if w.Code != want {
		t.Fatalf("feedback %s: %d want %d %s", tamper, w.Code, want, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal("webhook reflected provider content")
	}
}
func feedbackBody(provider, kind, bounce string) []byte {
	body, _ := json.Marshal(map[string]any{"type": "email." + kind, "created_at": time.Now().UTC(), "data": map[string]any{"email_id": provider, "to": []string{"PRIVATE_ADDRESS@example.test"}, "subject": "PRIVATE_SUBJECT", "bounce": map[string]string{"type": bounce, "message": "PRIVATE_BOUNCE"}}})
	return body
}
func TestProductEmailFeedbackBeforeAcceptanceAndSuppression(t *testing.T) {
	f := newEmailFixture(t)
	f.service.EmailWebhookSecret = mail.RandomResendSecretForTest()
	f.subscribe(t)
	f.reply(t)
	f.step(t)
	providerID := uuid.NewString()
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		f.feedback(t, feedbackBody(providerID, "bounced", "Permanent"), "", 200)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": providerID})
	}))
	defer provider.Close()
	var err error
	f.service.EmailTransport, err = productemail.NewResend(mail.ResendConfig{APIKey: "local-only", From: "fixture@example.test", BaseURL: provider.URL, HTTPClient: provider.Client()})
	if err != nil {
		t.Fatal(err)
	}
	f.step(t)
	rows, err := f.store.ProductEmailRepository().Recent(t.Context(), f.key)
	if err != nil || len(rows) != 1 || rows[0].Status != "bounced" || rows[0].ReportedAt == nil {
		t.Fatal(rows, err)
	}
	f.feedback(t, feedbackBody(providerID, "complained", ""), "", 200)
	f.feedback(t, feedbackBody(providerID, "delivered", ""), "", 200)
	w := f.call(t, "GET", "/email-preferences", nil, 200)
	if !strings.Contains(w.Body.String(), `"pause_reason":"complaint"`) || strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal(w.Body.String())
	}
	f.call(t, "PUT", "/email-preferences", map[string]any{"work_outcomes": true, "help_replies": true, "expected_version": 1}, 200)
	f.reply(t)
	f.step(t)
	f.step(t)
	if calls != 1 {
		t.Fatal("paused recipient sent another message", calls)
	}
	w = f.call(t, "GET", "/email-deliveries", nil, 200)
	if !strings.Contains(w.Body.String(), `"status":"complained"`) || strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal(w.Body.String())
	}
	f.ownerCall(t, "GET", "?status=complained", nil, 200)
}
func TestProductEmailFeedbackRejectsUntrustedEvents(t *testing.T) {
	f := newEmailFixture(t)
	f.service.EmailWebhookSecret = mail.RandomResendSecretForTest()
	body := feedbackBody(uuid.NewString(), "bounced", "Permanent")
	for _, tamper := range []string{"signature", "stale", "body"} {
		f.feedback(t, body, tamper, 400)
	}
	f.feedback(t, []byte(`{"type":"email.bounced","data":{"email_id":"not-a-receipt"}}`), "", 400)
	f.feedback(t, []byte(`{"type":"email.opened","data":{"private":"PRIVATE"}}`), "", 200)
	f.feedback(t, bytes.Repeat([]byte("x"), api.ProductControlEmailFeedbackBodyMaxBytes+1), "", 400)
	f.service.Enabled = false
	f.feedback(t, body, "", 404)
}
