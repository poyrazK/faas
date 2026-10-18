package product

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/productemail"
)

func capturedUnsubscribe(t *testing.T, f *emailFixture) string {
	t.Helper()
	f.subscribe(t)
	f.reply(t)
	f.step(t)
	f.step(t)
	files, err := os.ReadDir(f.sink)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	raw, err := os.ReadFile(filepath.Join(f.sink, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var message mail.Message
	if err = json.Unmarshal(raw, &message); err != nil {
		t.Fatal(err)
	}
	link := strings.Trim(message.Headers["List-Unsubscribe"], "<>")
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host != "portal.example" || u.Path != "/v1/product-control/email/unsubscribe" || message.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatal("invalid one-click headers", err)
	}
	token := u.Query().Get("token")
	if _, err = productemail.UnsubscribeHash(token); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{message.TextBody, message.HTMLBody} {
		if !strings.Contains(body, "#token="+token) || strings.Contains(body, "?token="+token) {
			t.Fatal("unsafe or missing human confirmation link")
		}
	}
	return token
}
func unsubscribeHTTP(t *testing.T, f *emailFixture, method, query, contentType, body string, origins []string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "https://portal.example/v1/product-control/email/unsubscribe"+query, strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	for _, origin := range origins {
		r.Header.Add("Origin", origin)
	}
	w := httptest.NewRecorder()
	h := f.service.EmailUnsubscribe
	if method == "GET" || method == "HEAD" {
		h = f.service.EmailUnsubscribePage
	}
	f.service.Gate(h)(w, r)
	if w.Code != status {
		t.Fatalf("unsubscribe %s: %d want %d %s", method, w.Code, status, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable opt-out")
	}
	return w
}
func TestProductEmailUnsubscribeWithoutSession(t *testing.T) {
	f := newEmailFixture(t)
	token := capturedUnsubscribe(t, f)
	repo := f.store.ProductEmailRepository()
	before, _ := repo.Get(t.Context(), f.key)
	for _, method := range []string{"GET", "HEAD"} {
		w := unsubscribeHTTP(t, f, method, "?token="+token, "", "", nil, 200)
		if !strings.Contains(w.Body.String(), "Opening this page does not change") || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), f.key.TenantID) || w.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatal("unsafe confirmation page")
		}
	}
	after, _ := repo.Get(t.Context(), f.key)
	if after.Version != before.Version || !after.Work || !after.Help {
		t.Fatal("GET mutated consent")
	}
	// Opt-out keeps working while the transport, encryption and Auth realm are unavailable.
	f.service.EmailTransport, f.service.EmailOpen, f.service.EmailSeal = nil, nil, nil
	f.status.Store(403)
	body := `{"token":"` + token + `"}`
	w := unsubscribeHTTP(t, f, "POST", "", "application/json", body, []string{"https://portal.example"}, 200)
	var result api.ProductEmailUnsubscribeResult
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Current {
		t.Fatal(w.Body.String())
	}
	for _, secret := range []string{token, f.key.ActorID, f.key.TenantID, f.key.ID(), "@example.test", "grant_sealed", "unsubscribe_hash"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("bearer response exposed private metadata")
		}
	}
	repeat := unsubscribeHTTP(t, f, "POST", "?token="+token, "application/x-www-form-urlencoded", "List-Unsubscribe=One-Click", nil, 200)
	if !bytes.Equal(w.Body.Bytes(), repeat.Body.Bytes()) {
		t.Fatal("unstable acknowledgement")
	}
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	if err := writer.WriteField("List-Unsubscribe", "One-Click"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	multipartRepeat := unsubscribeHTTP(t, f, "POST", "?token="+token, writer.FormDataContentType(), form.String(), nil, 200)
	if !bytes.Equal(w.Body.Bytes(), multipartRepeat.Body.Bytes()) {
		t.Fatal("multipart mailbox POST changed acknowledgement")
	}
	p := f.call(t, "GET", "/email-preferences", nil, 200)
	if strings.Contains(p.Body.String(), `"work_outcomes":true`) || strings.Contains(p.Body.String(), `"help_replies":true`) {
		t.Fatal(p.Body.String())
	}
	history := f.call(t, "GET", "/email-opt-outs", nil, 200)
	if !strings.Contains(history.Body.String(), result.ReceiptID) || strings.Contains(history.Body.String(), token) {
		t.Fatal(history.Body.String())
	}
	f.actor.Store(uuid.NewString())
	history = f.call(t, "GET", "/email-opt-outs", nil, 200)
	if strings.Contains(history.Body.String(), result.ReceiptID) {
		t.Fatal("another actor read opt-out history")
	}
	f.service.Enabled = false
	unsubscribeHTTP(t, f, "POST", "", "application/json", body, nil, 404)
}
func TestProductEmailUnsubscribeRequestValidation(t *testing.T) {
	f := newEmailFixture(t)
	token := capturedUnsubscribe(t, f)
	for _, tc := range []struct {
		query, kind, body string
		origins           []string
		code              int
	}{
		{"", "application/json", `{"token":"` + token + `"}`, []string{"https://evil.example"}, 403},
		{"", "application/json", `{"token":"` + token + `"}`, []string{"https://portal.example", "https://portal.example"}, 403},
		{"", "application/json", `{"token":"` + token + `","tenant_id":"injected"}`, nil, 400},
		{"", "application/json", `{"token":"` + token + `"} {}`, nil, 400},
		{"", "application/json", `{"token":"` + strings.Repeat("A", 43) + `"}`, nil, 410},
		{"", "application/json", `{"token":"` + token[:42] + `B"}`, nil, 410},
		{"?token=" + token, "application/json", `{"token":"` + token + `"}`, nil, 400},
		{"?token=" + token + "&token=" + token, "application/x-www-form-urlencoded", "List-Unsubscribe=One-Click", nil, 400},
		{"?token=" + token + "&tenant=other", "application/x-www-form-urlencoded", "List-Unsubscribe=One-Click", nil, 400},
		{"?token=" + token, "application/x-www-form-urlencoded", "List-Unsubscribe=One-Click&List-Unsubscribe=One-Click", nil, 400},
		{"?token=" + token, "application/x-www-form-urlencoded", "List-Unsubscribe=One-Click&actor=other", nil, 400},
		{"?token=" + token, "text/plain", "List-Unsubscribe=One-Click", nil, 400},
		{"?token=" + token, "application/x-www-form-urlencoded", strings.Repeat("x", api.ProductControlEmailUnsubscribeBodyMaxBytes+1), nil, 400},
	} {
		unsubscribeHTTP(t, f, "POST", tc.query, tc.kind, tc.body, tc.origins, tc.code)
	}
	sub, _ := f.store.ProductEmailRepository().Get(t.Context(), f.key)
	if !sub.Work || sub.Version != 1 {
		t.Fatal("invalid request changed consent")
	}
	f.call(t, "PUT", "/email-preferences", map[string]any{"work_outcomes": true, "help_replies": true, "expected_version": 1}, 200)
	unsubscribeHTTP(t, f, "POST", "", "application/json", `{"token":"`+token+`"}`, nil, 410)
}
func TestProductEmailUnsubscribeFinalCheckAndInFlightReceipt(t *testing.T) {
	for _, during := range []string{"final_check", "in_flight"} {
		t.Run(during, func(t *testing.T) {
			f := newEmailFixture(t)
			f.subscribe(t)
			providerID, token, sends := uuid.NewString(), "", 0
			repo := f.store.ProductEmailRepository()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends++
				var msg mail.ResendRequest
				if json.NewDecoder(r.Body).Decode(&msg) != nil {
					t.Error("invalid provider message")
				}
				link, _ := url.Parse(strings.Trim(msg.Headers["List-Unsubscribe"], "<>"))
				token = link.Query().Get("token")
				if msg.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
					t.Error("headers did not reach provider")
				}
				if during == "in_flight" {
					hash, _ := productemail.UnsubscribeHash(token)
					if _, err := repo.Unsubscribe(r.Context(), hash, time.Now().UTC()); err != nil {
						t.Error(err)
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": providerID})
			}))
			defer provider.Close()
			var err error
			f.service.EmailTransport, err = productemail.NewResend(mail.ResendConfig{APIKey: "loopback-only", From: "fixture@example.test", BaseURL: provider.URL, HTTPClient: provider.Client()})
			if err != nil {
				t.Fatal(err)
			}
			f.reply(t)
			f.step(t)
			f.step(t)
			if sends != 1 || token == "" {
				t.Fatal("first notification missing")
			}
			if during == "final_check" {
				hash, _ := productemail.UnsubscribeHash(token)
				seal := f.service.EmailSeal
				f.service.EmailSeal = func(id, value string) ([]byte, error) {
					if id == productemail.RecipientEvidenceID(f.key) {
						if _, e := repo.Unsubscribe(t.Context(), hash, time.Now().UTC()); e != nil {
							t.Fatal(e)
						}
					}
					return seal(id, value)
				}
				f.reply(t)
				f.step(t)
				f.step(t)
				if sends != 1 {
					t.Fatal("sent after unsubscribe at final check")
				}
			}
			rows, err := repo.Recent(t.Context(), f.key)
			accepted := 0
			for _, row := range rows {
				if row.Status == "provider_accepted" {
					accepted++
				}
			}
			if err != nil || accepted != 1 {
				t.Fatal("lost in-flight provider receipt", rows, err)
			}
			sub, _ := repo.Get(t.Context(), f.key)
			if sub.Work || sub.Help || sub.Version != 2 {
				t.Fatal(sub)
			}
		})
	}
}
