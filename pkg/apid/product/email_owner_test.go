package product

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/productemail"
)

func (f *emailFixture) ownerCall(t *testing.T, method, query string, body any, status int) *httptest.ResponseRecorder {
	t.Helper()
	var data bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&data).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, "https://portal.example/v1/apps/"+f.app.Slug+"/product/email-deliveries"+query, &data)
	r.SetPathValue("slug", f.app.Slug)
	if strings.HasPrefix(query, "/") {
		r.SetPathValue("delivery", strings.Split(query, "/")[1])
	}
	w := httptest.NewRecorder()
	f.service.Gate(func(w http.ResponseWriter, r *http.Request) {
		if method == "GET" {
			f.service.EmailDeliveries(w, r, f.account)
		} else {
			f.service.EmailRetry(w, r, f.account)
		}
	})(w, r)
	if w.Code != status {
		t.Fatalf("owner %s %s: %d want %d %s", method, query, w.Code, status, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable owner email response")
	}
	return w
}
func (f *emailFixture) rejected(t *testing.T) productemail.Delivery {
	t.Helper()
	f.subscribe(t)
	f.reply(t)
	f.step(t)
	repo := f.store.ProductEmailRepository()
	jobs, err := repo.Claim(t.Context(), time.Now().UTC())
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	now := time.Now().UTC()
	if err = repo.Finish(t.Context(), jobs[0], "failed", "provider_rejected", now, now); err != nil {
		t.Fatal(err)
	}
	return jobs[0]
}
func TestProductEmailOwnerDiagnosticsAndRetryReceipt(t *testing.T) {
	f := newEmailFixture(t)
	d := f.rejected(t)
	w := f.ownerCall(t, "GET", "?tenant="+f.key.TenantID+"&status=failed", nil, 200)
	var page api.ProductEmailOwnerPage
	if json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Counts.Failed != 1 || len(page.Deliveries) != 1 || !page.Deliveries[0].RetryEligible || page.Mode != "local" {
		t.Fatal(w.Body.String())
	}
	if err := productemail.ValidateOwnerPage(page, f.key.TenantID, "failed", ""); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"verified-fixture@example.test", "PRIVATE_HELP_RESPONSE", "PRIVATE_CUSTOMER_QUESTION", "customer.access.jwt", strings.Repeat("A", 43), "grant", "actor_id"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("private owner projection", private)
		}
	}
	req := api.ProductEmailRetryRequest{TenantID: f.key.TenantID, ExpectedAttempts: &d.Attempts}
	first := f.ownerCall(t, "POST", "/"+d.ID+"/retry", req, 200).Body.String()
	files, _ := os.ReadDir(f.sink)
	if len(files) != 0 {
		t.Fatal("owner request sent inline")
	}
	f.step(t)
	second := f.ownerCall(t, "POST", "/"+d.ID+"/retry", req, 200).Body.String()
	if first != second {
		t.Fatal("lost-response retry changed receipt", first, second)
	}
	f.step(t)
	files, _ = os.ReadDir(f.sink)
	if len(files) != 1 {
		t.Fatal("repeated request duplicated message", len(files))
	}
	rows, _ := f.store.ProductEmailRepository().Recent(t.Context(), f.key)
	if len(rows) != 1 || rows[0].Status != "captured" || rows[0].Attempts != 2 {
		t.Fatal(rows)
	}
	newAttempt := 2
	f.ownerCall(t, "POST", "/"+d.ID+"/retry", api.ProductEmailRetryRequest{TenantID: f.key.TenantID, ExpectedAttempts: &newAttempt}, 409)
}
func TestProductEmailOwnerRetryRechecksCurrentAuthority(t *testing.T) {
	for _, change := range []string{"membership", "role", "realm", "mapping", "optout", "source", "transport", "auth outage"} {
		t.Run(change, func(t *testing.T) {
			f := newEmailFixture(t)
			d := f.rejected(t)
			switch change {
			case "membership":
				f.status.Store(403)
			case "role":
				f.role.Store("unknown")
			case "auth outage":
				f.status.Store(503)
			case "transport":
				f.service.EmailTransport = nil
			case "realm":
				f.spec.AuthApp = "other-auth"
				if _, err := f.store.PutProductCatalog(t.Context(), f.account.ID, f.app.ID, f.spec); err != nil {
					t.Fatal(err)
				}
			case "mapping":
				customer, err := f.store.GetProductCustomer(t.Context(), f.account.ID, f.app.ID, f.key.TenantID)
				if err != nil {
					t.Fatal(err)
				}
				customer.AuthOrganizationID = uuid.NewString()
				if _, err = f.store.SetProductCustomer(t.Context(), f.account.ID, f.app.ID, customer); err != nil {
					t.Fatal(err)
				}
			case "optout":
				f.call(t, "PUT", "/email-preferences", map[string]any{"work_outcomes": false, "help_replies": false, "expected_version": 1}, 200)
			case "source":
				help, err := f.store.GetProductOwnerHelp(t.Context(), f.account.ID, f.app.ID, d.Notification.ReferenceID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.store.UpdateProductHelp(t.Context(), f.account.ID, f.app.ID, help.Request.ID, api.ProductHelpUpdate{ExpectedVersion: help.Version, Status: "resolved", Response: "NEW_PRIVATE_RESPONSE"}); err != nil {
					t.Fatal(err)
				}
			}
			f.ownerCall(t, "POST", "/"+d.ID+"/retry", api.ProductEmailRetryRequest{TenantID: f.key.TenantID, ExpectedAttempts: &d.Attempts}, 409)
			rows, _ := f.store.ProductEmailRepository().Recent(t.Context(), f.key)
			if len(rows) != 1 || rows[0].Status != "failed" || rows[0].Attempts != 1 {
				t.Fatal("denied retry mutated queue", rows)
			}
		})
	}
}
func TestProductEmailOwnerSelectorsAndFeatureGate(t *testing.T) {
	f := newEmailFixture(t)
	d := f.rejected(t)
	for _, query := range []string{"?status=bad", "?tenant=bad", "?status=all&status=failed", "?actor_id=" + f.key.ActorID, "?cursor=invalid"} {
		f.ownerCall(t, "GET", query, nil, 400)
	}
	f.ownerCall(t, "GET", "?tenant="+uuid.NewString(), nil, 404)
	for _, body := range []any{map[string]any{"tenant_id": f.key.TenantID}, map[string]any{"tenant_id": f.key.TenantID, "expected_attempts": 0}, map[string]any{"tenant_id": f.key.TenantID, "expected_attempts": 1, "email": "bad@example.test"}} {
		f.ownerCall(t, "POST", "/"+d.ID+"/retry", body, 400)
	}
	f.ownerCall(t, "POST", "/"+uuid.NewString()+"/retry", api.ProductEmailRetryRequest{TenantID: f.key.TenantID, ExpectedAttempts: &d.Attempts}, 404)
	f.service.Enabled = false
	f.ownerCall(t, "GET", "", nil, 404)
	f.ownerCall(t, "POST", "/"+d.ID+"/retry", api.ProductEmailRetryRequest{TenantID: f.key.TenantID, ExpectedAttempts: &d.Attempts}, 404)
}
func TestProductEmailOwnerProviderRecoveryKeepsOriginalKey(t *testing.T) {
	f := newEmailFixture(t)
	f.subscribe(t)
	f.reply(t)
	f.step(t)
	calls, key := 0, ""
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if key == "" {
			key = r.Header.Get("Idempotency-Key")
		}
		if key == "" || key != r.Header.Get("Idempotency-Key") {
			t.Error("manual retry changed provider key")
		}
		if calls == 1 {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"id":"ca098456-6358-4018-b047-f7000be7fb66"}`))
	}))
	defer provider.Close()
	var err error
	f.service.EmailTransport, err = productemail.NewResend(mail.ResendConfig{APIKey: "local-only", From: "fixture@example.test", BaseURL: provider.URL, HTTPClient: provider.Client()})
	if err != nil {
		t.Fatal(err)
	}
	f.step(t)
	rows, _ := f.store.ProductEmailRepository().Recent(t.Context(), f.key)
	if len(rows) != 1 || rows[0].Status != "failed" || rows[0].Reason != "provider_rejected" || calls != 1 {
		t.Fatal(rows, calls)
	}
	attempt := 1
	f.ownerCall(t, "POST", "/"+rows[0].ID+"/retry", api.ProductEmailRetryRequest{TenantID: f.key.TenantID, ExpectedAttempts: &attempt}, 200)
	f.step(t)
	f.ownerCall(t, "POST", "/"+rows[0].ID+"/retry", api.ProductEmailRetryRequest{TenantID: f.key.TenantID, ExpectedAttempts: &attempt}, 200)
	f.step(t)
	rows, _ = f.store.ProductEmailRepository().Recent(t.Context(), f.key)
	if len(rows) != 1 || rows[0].Status != "provider_accepted" || rows[0].Attempts != 2 || calls != 2 {
		t.Fatal(rows, calls)
	}
}

func TestProductEmailOwnerCannotShortenAutomaticBackoff(t *testing.T) {
	f := newEmailFixture(t)
	f.subscribe(t)
	f.reply(t)
	f.step(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	}))
	defer provider.Close()
	var err error
	f.service.EmailTransport, err = productemail.NewResend(mail.ResendConfig{APIKey: "local-only", From: "fixture@example.test", BaseURL: provider.URL, HTTPClient: provider.Client()})
	if err != nil {
		t.Fatal(err)
	}
	f.step(t)
	scope := productemail.OwnerScope{AccountID: f.key.AccountID, AppID: f.key.AppID, TenantID: f.key.TenantID}
	page, err := f.store.ProductEmailRepository().OwnerPage(t.Context(), scope, "queued", "", time.Now().UTC())
	if err != nil || len(page.Deliveries) != 1 {
		t.Fatal(page, err)
	}
	d := page.Deliveries[0]
	if d.Reason != "transport_unavailable" || d.RetryEligible || d.NextAttemptAt.Before(time.Now().Add(59*time.Minute)) {
		t.Fatal("provider delay lost", d)
	}
	f.ownerCall(t, "POST", "/"+d.ID+"/retry", api.ProductEmailRetryRequest{TenantID: f.key.TenantID, ExpectedAttempts: &d.Attempts}, 409)
	_, fresh, err := f.store.ProductEmailRepository().OwnerGet(t.Context(), scope, d.ID, time.Now().UTC())
	if err != nil || !fresh.NextAt.Equal(d.NextAttemptAt) || fresh.Attempts != d.Attempts {
		t.Fatal("owner shortened retry-after", fresh, err)
	}
}
