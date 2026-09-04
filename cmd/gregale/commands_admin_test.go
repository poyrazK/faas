// Issue #279 PR A — `gregale admin credit <account> <cents> --reason`
// CLI smoke tests. Pins the three contracts:
//
//  1. Argument validation: bad UUID, bad cents, missing reason each
//     print a usage line and exit 2 (the CLI convention for
//     operator-error inputs).
//  2. Happy path: a single POST /v1/admin/accounts/{id}/credits hits
//     the API with the explicit Idempotency-Key header and the
//     {cents, reason} JSON body.
//  3. Auth gate: no token → exit 2 (the documented errAuth path).
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestAdminCredit_BadUUIDExitsTwo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected on bad UUID")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	code := cmdAdmin([]string{"credit", "not-a-uuid", "500", "--reason", "goodwill for outage"})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestAdminCredit_BadCentsExitsTwo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected on bad cents")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	code := cmdAdmin([]string{"credit", uuid.NewString(), "not-an-int", "--reason", "goodwill for outage"})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestAdminCredit_MissingReasonExitsTwo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected on missing --reason")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	// Flag-before-positional; --reason absent → the empty-string
	// validator fires after fs.Parse.
	code := cmdAdmin([]string{"credit", uuid.NewString(), "500"})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestAdminCredit_NoTokenExitsTwo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected without a token")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	code := cmdAdmin([]string{"credit", uuid.NewString(), "500", "--reason", "goodwill for outage"})
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (auth)", code)
	}
}

func TestAdminCredit_HappyPathHitsAPID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	targetID := uuid.NewString()
	const wantReason = "goodwill for outage"

	var hits [3]string // [path, idem key, body]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credits"):
			hits[0] = r.URL.Path
			hits[1] = r.Header.Get("Idempotency-Key")
			var body struct {
				Cents  int64  `json:"cents"`
				Reason string `json:"reason"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			hits[2] = body.Reason
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.AccountCreditResponse{
				ID:             uuid.NewString(),
				AccountID:      targetID,
				CentsRemaining: body.Cents,
				Reason:         body.Reason,
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	// Go's flag package stops parsing flags once a positional arg is
	// seen; the documented convention is flags-before-positional.
	// cmdAccountExport's `--no-secrets` test pins the same pattern.
	code := cmdAdmin([]string{"credit", "--reason", wantReason, targetID, "500"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.HasSuffix(hits[0], "/"+targetID+"/credits") {
		t.Errorf("path = %q, want suffix /%s/credits", hits[0], targetID)
	}
	if !strings.HasPrefix(hits[1], "cli-admin-credit-") {
		t.Errorf("Idempotency-Key = %q, want cli-admin-credit-*", hits[1])
	}
	if hits[2] != wantReason {
		t.Errorf("body reason = %q, want %q", hits[2], wantReason)
	}
}

// TestAdminCredit_IdempotentOnRetry pins the regression fix for the
// bug called out in PR #337's review: the Idempotency-Key MUST be
// stable across invocations of the same operator intent so a
// flaky-network retry returns the same credit_id (the server's
// idempotency middleware dedupes on (caller, key) for 24h). Before
// the fix the key carried a fresh crypto/rand nonce per invocation,
// defeating the dedupe and landing duplicate account_credits rows.
//
// Hash inputs are (account_uuid, cents, reason); the test varies
// each axis to prove the hash mixes all three.
func TestAdminCredit_IdempotentOnRetry(t *testing.T) {
	setupAdmin := func() {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("FAAS_TOKEN", "test")
	}

	target := uuid.NewString()
	const reason = "goodwill for outage"

	// Capture every Idempotency-Key the CLI sends during one test run.
	var keys []string
	recordSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credits") {
			keys = append(keys, r.Header.Get("Idempotency-Key"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.AccountCreditResponse{
			ID:             uuid.NewString(),
			AccountID:      target,
			CentsRemaining: 500,
			Reason:         reason,
		})
	}))

	setupAdmin()
	t.Setenv("FAAS_API", recordSrv.URL)

	// Two invocations of the *same* operator intent → same key.
	for i := 0; i < 2; i++ {
		if code := cmdAdmin([]string{"credit", "--reason", reason, target, "500"}); code != 0 {
			t.Fatalf("invocation %d: exit %d, want 0", i, code)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("captured %d requests, want 2", len(keys))
	}
	if keys[0] != keys[1] {
		t.Errorf("Idempotency-Key unstable across retries: %q != %q", keys[0], keys[1])
	}
	if !strings.HasPrefix(keys[0], "cli-admin-credit-") {
		t.Errorf("Idempotency-Key = %q, want cli-admin-credit-*", keys[0])
	}

	// Different cents → different key (the hash mixes all three inputs).
	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credits") {
			keys = append(keys, r.Header.Get("Idempotency-Key"))
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer otherSrv.Close()
	setupAdmin()
	t.Setenv("FAAS_API", otherSrv.URL)
	if code := cmdAdmin([]string{"credit", "--reason", reason, target, "501"}); code != 0 {
		t.Fatalf("different-cents invocation: exit %d, want 0", code)
	}
	if keys[2] == keys[0] {
		t.Errorf("different cents produced the same key %q; hash is not input-sensitive", keys[2])
	}

	// Different reason → different key.
	r3Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credits") {
			keys = append(keys, r.Header.Get("Idempotency-Key"))
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer r3Srv.Close()
	setupAdmin()
	t.Setenv("FAAS_API", r3Srv.URL)
	if code := cmdAdmin([]string{"credit", "--reason", "different reason here", target, "500"}); code != 0 {
		t.Fatalf("different-reason invocation: exit %d, want 0", code)
	}
	if keys[3] == keys[0] {
		t.Errorf("different reason produced the same key %q; hash is not input-sensitive", keys[3])
	}
}

// TestAdminUsageAdvertisesRefund pins the operator surface and keeps the
// dispatcher/help contract together with the server-side refund endpoint.
func TestAdminUsageAdvertisesRefund(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_API", "")

	stderr, restore := captureStderr(t)
	defer restore()
	if code := cmdAdmin([]string{}); code != 2 {
		t.Errorf("cmdAdmin([]) exit = %d, want 2 (operator error)", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "refund") {
		t.Fatalf("cmdAdmin usage missing refund subcommand: %s", out)
	}
	if !strings.Contains(out, "credit") {
		t.Fatalf("cmdAdmin usage missing `credit` entry — sanity check, the only known subcommand:\n  %s", out)
	}
}

func TestAdminRefund_BadInvoiceExitsTwo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected on bad invoice UUID")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	code := cmdAdmin([]string{"refund", "--reason", "customer request", uuid.NewString(), "not-a-uuid", "500"})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestAdminRefund_HappyPathHitsAPID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	targetID := uuid.NewString()
	invoiceID := uuid.NewString()
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/refunds") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotPath = r.URL.Path
		gotKey = r.Header.Get("Idempotency-Key")
		var body struct {
			InvoiceID   string `json:"invoice_id"`
			AmountCents int64  `json:"amount_cents"`
			Reason      string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.InvoiceID != invoiceID || body.AmountCents != 500 || body.Reason != "customer request" {
			t.Errorf("request body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.AdminRefundResponse{
			AccountID: targetID, InvoiceID: invoiceID, Provider: "polar",
			ProviderRefundID: "refund-1", ChargeID: "order-1", AmountCents: 500,
			Currency: "EUR", Status: "succeeded",
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdAdmin([]string{"refund", "--reason", "customer request", targetID, invoiceID, "500"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if gotPath != "/v1/admin/accounts/"+targetID+"/refunds" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.HasPrefix(gotKey, "cli-admin-refund-") {
		t.Errorf("Idempotency-Key = %q, want cli-admin-refund-*", gotKey)
	}
}
