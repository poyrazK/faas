package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	stripewebhook "github.com/onebox-faas/faas/pkg/billing/stripe"
)

const testInvoiceID = "22222222-2222-4222-8222-222222222222"

type recordedRequest struct {
	method, path, query, cookie, idempotency string
	body                                     map[string]any
}

// operatorAPIServer records every request and answers with response.
func operatorAPIServer(t *testing.T, response any) *[]recordedRequest {
	t.Helper()
	var seen []recordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := recordedRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, idempotency: r.Header.Get("Idempotency-Key")}
		if cookie, err := r.Cookie("faas_sid"); err == nil {
			got.cookie = cookie.Value
		}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &got.body)
		}
		seen = append(seen, got)
		writeTestJSON(w, http.StatusOK, response)
	}))
	t.Cleanup(server.Close)
	installTestOperatorSession(t, server.URL, "operator-cookie")
	return &seen
}

func TestOperatorCreditUsesSessionAndStableIdempotencyKey(t *testing.T) {
	seen := operatorAPIServer(t, api.AccountCreditResponse{ID: "credit-1", AccountID: testAccountID, CentsRemaining: 500})
	out, stderr, restore := captureOperatorIO()
	defer restore()
	args := []string{"billing", "credit", "--account-id", testAccountID, "--cents", "500", "--reason", "outage goodwill", "--yes"}
	for range 2 {
		if code := run(args); code != 0 {
			t.Fatalf("run = %d, stderr=%s", code, stderr.String())
		}
	}
	first, second := (*seen)[0], (*seen)[1]
	if first.method != http.MethodPost || first.path != "/v1/admin/accounts/"+testAccountID+"/credits" || first.cookie != "operator-cookie" {
		t.Fatalf("request = %+v", first)
	}
	if first.idempotency == "" || first.idempotency != second.idempotency {
		t.Fatalf("idempotency keys %q / %q must be equal and non-empty", first.idempotency, second.idempotency)
	}
	if first.body["cents"] != float64(500) || first.body["reason"] != "outage goodwill" {
		t.Fatalf("body = %v", first.body)
	}
	if !strings.Contains(out.String(), "credit=credit-1") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestOperatorMoneyCommandsRequireYes(t *testing.T) {
	seen := operatorAPIServer(t, map[string]any{})
	_, _, restore := captureOperatorIO()
	defer restore()
	for _, args := range [][]string{
		{"billing", "credit", "--account-id", testAccountID, "--cents", "500", "--reason", "goodwill"},
		{"billing", "refund", "--account-id", testAccountID, "--invoice-id", testInvoiceID, "--cents", "5", "--reason", "duplicate"},
		{"billing", "consume-credits", "--invoice-id", testInvoiceID},
		{"accounts", "abuse-hold", "place", "--account-id", testAccountID, "--note", "mining"},
		{"github", "webhook-secret", "set", "--installation-id", "42"},
	} {
		if code := run(args); code != 2 {
			t.Errorf("%v = %d, want 2", args, code)
		}
	}
	if len(*seen) != 0 {
		t.Fatalf("validation failures sent %d requests", len(*seen))
	}
}

func TestOperatorRefundSendsInvoiceAndAmount(t *testing.T) {
	seen := operatorAPIServer(t, api.AdminRefundResponse{InvoiceID: testInvoiceID, AmountCents: 250, Status: "pending"})
	_, stderr, restore := captureOperatorIO()
	defer restore()
	if code := run([]string{"billing", "refund", "--account-id", testAccountID, "--invoice-id", testInvoiceID, "--cents", "250", "--reason", "double charge", "--idempotency-key", "refund-1", "--yes"}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	got := (*seen)[0]
	if got.path != "/v1/admin/accounts/"+testAccountID+"/refunds" || got.idempotency != "refund-1" {
		t.Fatalf("request = %+v", got)
	}
	if got.body["invoice_id"] != testInvoiceID || got.body["amount_cents"] != float64(250) {
		t.Fatalf("body = %v", got.body)
	}
}

func TestOperatorAbuseHoldReleaseUsesDelete(t *testing.T) {
	seen := operatorAPIServer(t, api.AccountAbuseHoldActionResponse{AccountID: testAccountID, Changed: true})
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := run([]string{"accounts", "abuse-hold", "release", "--account-id", testAccountID, "--note", "reviewed, false positive", "--yes"}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	got := (*seen)[0]
	if got.method != http.MethodDelete || got.path != "/v1/admin/accounts/"+testAccountID+"/abuse-hold" || got.idempotency == "" {
		t.Fatalf("request = %+v", got)
	}
	if !strings.Contains(out.String(), "held=false changed=true") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestOperatorEgressFlowsQuery(t *testing.T) {
	seen := operatorAPIServer(t, api.EgressFlowLogResponse{})
	_, stderr, restore := captureOperatorIO()
	defer restore()
	if code := run([]string{"accounts", "egress-flows", "--remote", "203.0.113.0/24", "--from", "2026-11-14T00:00:00Z", "--limit", "10"}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	got := (*seen)[0]
	if got.path != "/v1/admin/egress-flows" || got.query != "from=2026-11-14T00%3A00%3A00Z&limit=10&remote=203.0.113.0%2F24" {
		t.Fatalf("request = %+v", got)
	}
}

func TestOperatorEgressFlowsBlocked(t *testing.T) {
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	seen := operatorAPIServer(t, api.EgressFlowLogResponse{Flows: []api.EgressFlowLogEntry{
		{ObservedAt: at, Node: "bhs-2", AccountID: testAccountID, AppID: "app-1", InstanceID: "i-1",
			RemoteIP: "2001:db8::25", RemotePort: 587, Protocol: "tcp", Blocked: true},
	}})
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := run([]string{"accounts", "egress-flows", "--account-id", testAccountID, "--verdict", "blocked"}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	if got := (*seen)[0]; got.query != "account_id="+testAccountID+"&verdict=blocked" {
		t.Fatalf("request = %+v", got)
	}
	want := "observed_at=2026-11-28T12:00:00Z verdict=blocked node=bhs-2 account_id=" + testAccountID +
		" app_id=app-1 instance_id=i-1 remote=tcp [2001:db8::25]:587\n"
	if out.String() != want {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
	if code := run([]string{"accounts", "egress-flows", "--verdict", "dropped"}); code != 2 {
		t.Fatalf("bad verdict run = %d, want 2", code)
	}
}

func TestOperatorPostgresUsageImportPreviewThenApply(t *testing.T) {
	seen := operatorAPIServer(t, api.ManagedPostgresUsageImportResult{Revision: "rev-1"})
	_, stderr, restore := captureOperatorIO()
	defer restore()
	dir := t.TempDir()
	preview := filepath.Join(dir, "preview.json")
	apply := filepath.Join(dir, "apply.json")
	if err := os.WriteFile(preview, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(apply, []byte(`{"expected_revision":"rev-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"billing", "postgres", "usage-import", "--account-id", testAccountID}
	if code := run(append(base, "--file", preview)); code != 0 {
		t.Fatalf("preview = %d, stderr=%s", code, stderr.String())
	}
	if code := run(append(base, "--file", apply, "--apply")); code != 0 {
		t.Fatalf("apply = %d, stderr=%s", code, stderr.String())
	}
	if code := run(append(base, "--file", preview, "--apply")); code != 2 {
		t.Fatalf("apply without revision = %d, want 2", code)
	}
	prefix := "/v1/admin/managed-postgres/accounting/" + testAccountID + "/usage-imports"
	if len(*seen) != 2 || (*seen)[0].path != prefix+"/preview" || (*seen)[1].path != prefix || (*seen)[1].idempotency == "" {
		t.Fatalf("requests = %+v", *seen)
	}
}

func TestOperatorPostgresDiagnostics(t *testing.T) {
	seen := operatorAPIServer(t, api.ManagedPostgresAccountingDiagnosticsResponse{AccountID: testAccountID})
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := run([]string{"billing", "postgres", "diagnostics", "--account-id", testAccountID, "--limit", "5"}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	if got := (*seen)[0]; got.method != http.MethodGet || got.path != "/v1/admin/managed-postgres/accounting/"+testAccountID || got.query != "limit=5" {
		t.Fatalf("request = %+v", got)
	}
	if !strings.Contains(out.String(), testAccountID) {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestOperatorGithubWebhookSecretReadsStdin(t *testing.T) {
	seen := operatorAPIServer(t, api.AdminSetGithubWebhookSecretResponse{UpgradedBy: "op", UpgradedAt: time.Now()})
	_, stderr, restore := captureOperatorIO()
	defer restore()
	secret := strings.Repeat("ab", 16)
	old := operatorStdin
	operatorStdin = strings.NewReader("# rotated\n" + strings.ToUpper(secret) + "\n")
	defer func() { operatorStdin = old }()
	if code := run([]string{"github", "webhook-secret", "set", "--installation-id", "42", "--yes"}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	got := (*seen)[0]
	if got.path != "/v1/admin/github-webhook-secrets" || got.body["installation_id"] != float64(42) || got.body["secret_hex"] != secret {
		t.Fatalf("request = %+v", got)
	}
}

func TestOperatorSecretsRekeyProgress(t *testing.T) {
	seen := operatorAPIServer(t, api.RekeyProgress{Total: 10, Rekeyed: 7})
	out, stderr, restore := captureOperatorIO()
	defer restore()
	jsonOutput = true
	if code := run([]string{"secrets", "rekey-progress"}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	var progress api.RekeyProgress
	if err := json.Unmarshal(out.Bytes(), &progress); err != nil || (*seen)[0].path != "/v1/admin/secrets/rekey-progress" || progress.Rekeyed != 7 {
		t.Fatalf("request = %+v stdout = %q", (*seen)[0], out.String())
	}
}

// The signer and the server's verifier must agree (this test moved here
// with `billing webhook-test` from the customer CLI).
func TestOperatorWebhookTestStripeSignatureVerifies(t *testing.T) {
	body := []byte(`{"id":"evt_cli","type":"invoice.paid"}`)
	dir := t.TempDir()
	payload, secretFile := filepath.Join(dir, "event.json"), filepath.Join(dir, "secret")
	if err := os.WriteFile(payload, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretFile, []byte("whsec_cli\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	verdict := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		verdict <- stripewebhook.VerifySignature(got, r.Header.Get("Stripe-Signature"), "whsec_cli", time.Minute)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	_, stderr, restore := captureOperatorIO()
	defer restore()
	if code := run([]string{"billing", "webhook-test", "stripe", "--url", server.URL, "--secret-file", secretFile, "--payload", payload}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	if err := <-verdict; err != nil {
		t.Fatalf("verifier rejected the signature: %v", err)
	}
}
