package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOperatorBillingCatalogUsesOperatorSession(t *testing.T) {
	var gotCookie, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if cookie, err := r.Cookie("faas_sid"); err == nil {
			gotCookie = cookie.Value
		}
		writeTestJSON(w, http.StatusOK, api.BillingCatalogResponse{
			Provider: "paddle",
			SyncedAt: "2026-09-12T12:00:00Z",
			Entries: []api.BillingCatalogEntry{{
				Plan: "pro", Kind: api.BillingCatalogKindMonthly, Handle: "pri_test", SyncedAt: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
			}},
		})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "operator-cookie")
	out, stderr, restore := captureOperatorIO()
	defer restore()

	if code := run([]string{"billing", "price-catalog", "list"}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	if gotPath != "/v1/admin/billing-paddle-catalog" || gotCookie != "operator-cookie" {
		t.Fatalf("request path/cookie = %q/%q", gotPath, gotCookie)
	}
	if !strings.Contains(out.String(), "provider=paddle") || !strings.Contains(out.String(), "pri_test") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestOperatorBillingReconcileJSONAndIdempotency(t *testing.T) {
	const accountID = "11111111-1111-4111-8111-111111111111"
	var gotMethod, gotPath, gotIdempotency string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotIdempotency = r.Method, r.URL.Path, r.Header.Get("Idempotency-Key")
		writeTestJSON(w, http.StatusOK, api.BillingReconcileResponse{AccountID: accountID, MBSeconds: 42})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "operator-cookie")
	out, stderr, restore := captureOperatorIO()
	defer restore()
	jsonOutput = true

	if code := run([]string{"billing", "reconcile", accountID}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/admin/billing-reconcile/"+accountID || gotIdempotency == "" {
		t.Fatalf("request = %s %s idempotency=%q", gotMethod, gotPath, gotIdempotency)
	}
	var response api.BillingReconcileResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil || response.MBSeconds != 42 {
		t.Fatalf("json = %q, err=%v", out.String(), err)
	}
}

func TestOperatorWebhookTestSignsRequest(t *testing.T) {
	var signature string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signature = r.Header.Get("Stripe-Signature")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	secretPath := filepath.Join(t.TempDir(), "stripe.secret")
	if err := os.WriteFile(secretPath, []byte("whsec_test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, stderr, restore := captureOperatorIO()
	defer restore()

	if code := run([]string{"billing", "webhook-test", "stripe", "--url", server.URL, "--secret-file", secretPath}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	if signature == "" || !strings.Contains(signature, "v1=") {
		t.Fatalf("Stripe-Signature = %q", signature)
	}
	if !strings.Contains(out.String(), "204 No Content") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestOperatorBillingNestedHelpIsLocal(t *testing.T) {
	out, _, restore := captureOperatorIO()
	defer restore()
	if code := run([]string{"billing", "reconcile", "--help"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	if !strings.Contains(out.String(), "gregalectl billing") {
		t.Fatalf("stdout = %q", out.String())
	}
}
