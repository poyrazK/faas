// commands_billing_test.go — issue #253 + #242 CLI surface pin.
//
// Pins the four documented behaviours of `gregale billing portal`:
//   1. --print prints the URL and skips the browser
//   2. --no-open (default branch) opens via browser.Default (recorder)
//   3. empty URL → "portal not configured" friendly error, exit 1
//   4. no auth → exit 2 (handled by `requireNoAuth`)
//   5. unknown subcommand → usage error, exit 1
//
// Issue #242 adds retry / cancel / payment-method subcommands:
//   - retry: POST /v1/billing/retry → 200; prints attempt+provider IDs
//   - cancel: POST /v1/billing/cancel → 200; prints effective date
//   - payment-method: GET /v1/billing/portal → prints card-on-file
//
// The dispatcher (cmdBilling) is pinned by the subcommand routing:
// `gregale billing help` exits 0; `gregale billing bogus` exits 1.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/browser"
)

// billingPortalStub is the minimal apid stub the CLI talks to: it
// answers GET /v1/billing/portal with either a populated URL or the
// empty-URL "absent" sentinel, depending on the configure() return.
func billingPortalStub(t *testing.T, configure func() api.BillingPortalResponse) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/billing/portal", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(configure())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestCmdBillingPortal_PrintFlag(t *testing.T) {
	apiURL := billingPortalStub(t, func() api.BillingPortalResponse {
		return api.BillingPortalResponse{URL: "https://billing.example.com/portal?account=acct_42"}
	})
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	// Swap the browser opener so an accidental browser call fails
	// the test loudly. --print must NOT call browser.Open.
	rec := withRecorder(t)

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdBillingPortal([]string{"--print"}); code != 0 {
		t.Fatalf("cmdBillingPortal --print = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "https://billing.example.com/portal?account=acct_42") {
		t.Errorf("stdout missing URL; got: %q", stdout.String())
	}
	if len(rec.urls) != 0 {
		t.Errorf("--print opened browser %d times; want 0", len(rec.urls))
	}
}

func TestCmdBillingPortal_NoOpenAlias(t *testing.T) {
	apiURL := billingPortalStub(t, func() api.BillingPortalResponse {
		return api.BillingPortalResponse{URL: "https://billing.example.com/portal?account=acct_42"}
	})
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	rec := withRecorder(t)
	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdBillingPortal([]string{"--no-open"}); code != 0 {
		t.Fatalf("cmdBillingPortal --no-open = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "https://billing.example.com/portal?account=acct_42") {
		t.Errorf("stdout missing URL; got: %q", stdout.String())
	}
	if len(rec.urls) != 0 {
		t.Errorf("--no-open opened browser %d times; want 0", len(rec.urls))
	}
}

func TestCmdBillingPortal_OpensBrowser(t *testing.T) {
	apiURL := billingPortalStub(t, func() api.BillingPortalResponse {
		return api.BillingPortalResponse{URL: "https://billing.example.com/portal?account=acct_42"}
	})
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	rec := withRecorder(t)
	if code := cmdBillingPortal(nil); code != 0 {
		t.Fatalf("cmdBillingPortal (default) = %d, want 0", code)
	}
	if len(rec.urls) != 1 {
		t.Fatalf("recorder saw %d launches, want 1", len(rec.urls))
	}
	if rec.urls[0] != "https://billing.example.com/portal?account=acct_42" {
		t.Errorf("opened URL = %q, want the substituted portal link", rec.urls[0])
	}
}

func TestCmdBillingPortal_BrowserOpenFailureExitsZero(t *testing.T) {
	// Mirrors cmdDashboard: a failed browser launch is not a CLI
	// failure — the customer's intent ("get the portal URL") is
	// still satisfied via the stderr fallback. Exit 0.
	apiURL := billingPortalStub(t, func() api.BillingPortalResponse {
		return api.BillingPortalResponse{URL: "https://billing.example.com/portal?account=acct_42"}
	})
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	rec := withRecorder(t)
	rec.err = errBrowserStub
	stderr, restore := captureStderr(t)
	defer restore()
	if code := cmdBillingPortal(nil); code != 0 {
		t.Errorf("cmdBillingPortal on browser failure = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "Could not open browser") {
		t.Errorf("stderr missing browser-failure notice; got: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "https://billing.example.com/portal?account=acct_42") {
		t.Errorf("stderr missing URL fallback; got: %q", stderr.String())
	}
}

// errBrowserStub is the canned browser error used by the failure
// tests. We define it locally so the test file does not depend on
// internal pkg/browser symbols.
var errBrowserStub = errStub("browser.Open: stub failure")

type errStub string

func (e errStub) Error() string { return string(e) }

func TestCmdBillingPortal_EmptyURLReturnsFriendlyError(t *testing.T) {
	apiURL := billingPortalStub(t, func() api.BillingPortalResponse {
		return api.BillingPortalResponse{URL: ""}
	})
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stderr, restore := captureStderr(t)
	defer restore()
	if code := cmdBillingPortal(nil); code != 1 {
		t.Errorf("cmdBillingPortal on empty URL = %d, want 1 (user error)", code)
	}
	if !strings.Contains(stderr.String(), "Billing portal is not configured") {
		t.Errorf("stderr missing friendly hint; got: %q", stderr.String())
	}
}

func TestCmdBillingPortal_RequiresLogin(t *testing.T) {
	requireNoAuth(t)
	if code := cmdBillingPortal(nil); code != 2 {
		t.Errorf("cmdBillingPortal no-auth = %d, want 2", code)
	}
}

func TestCmdBillingPortal_RejectsExtraArgs(t *testing.T) {
	t.Setenv("FAAS_API", "http://localhost")
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdBillingPortal([]string{"--print", "junk"}); code != 1 {
		t.Errorf("cmdBillingPortal with extra args = %d, want 1", code)
	}
}

func TestCmdBilling_Dispatch(t *testing.T) {
	t.Setenv("FAAS_API", "http://localhost")
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	t.Run("bare usage error", func(t *testing.T) {
		stderr, restore := captureStderr(t)
		defer restore()
		if code := cmdBilling(nil); code != 1 {
			t.Errorf("cmdBilling (no sub) = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "usage: gregale billing") {
			t.Errorf("stderr missing usage; got: %q", stderr.String())
		}
	})
	t.Run("help exits zero", func(t *testing.T) {
		stdout, restore := captureStdout(t)
		defer restore()
		if code := cmdBilling([]string{"help"}); code != 0 {
			t.Errorf("cmdBilling help = %d, want 0", code)
		}
		if !strings.Contains(stdout.String(), "portal") {
			t.Errorf("help output missing 'portal' subcommand; got: %q", stdout.String())
		}
		// PR-P3 subcommands also appear in the help text so an
		// operator running `gregale billing help` discovers them.
		// Issue #242 adds retry / cancel / payment-method.
		for _, sub := range []string{"status", "price-catalog", "reconcile", "retry", "cancel", "payment-method"} {
			if !strings.Contains(stdout.String(), sub) {
				t.Errorf("help output missing %q subcommand; got: %q", sub, stdout.String())
			}
		}
	})
	t.Run("unknown subcommand exits 1", func(t *testing.T) {
		stderr, restore := captureStderr(t)
		defer restore()
		if code := cmdBilling([]string{"reticulate-splines"}); code != 1 {
			t.Errorf("cmdBilling unknown sub = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "unknown subcommand") {
			t.Errorf("stderr missing 'unknown subcommand'; got: %q", stderr.String())
		}
	})
}

// billingCatalogStub is the minimal apid stub for the PR-P3 catalog
// endpoints. It answers GET /v1/admin/billing-paddle-catalog with the
// configure() result. Tests that need to exercise sync / reset mount
// the same handler under their respective paths; the catalog GET
// covers the read-side assertions.
func billingCatalogStub(t *testing.T, configure func() api.BillingCatalogResponse) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/admin/billing-paddle-catalog", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(configure())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestCmdBillingStatus_RendersCatalog(t *testing.T) {
	apiURL := billingCatalogStub(t, func() api.BillingCatalogResponse {
		return api.BillingCatalogResponse{
			Provider: "paddle",
			SyncedAt: "2026-08-08T12:00:00Z",
			Entries: []api.BillingCatalogEntry{
				{Plan: "hobby", Kind: api.BillingCatalogKindMonthly, Handle: "pri_h_monthly", SyncedAt: parseTime(t, "2026-08-08T12:00:00Z")},
				{Plan: "hobby", Kind: api.BillingCatalogKindOverage, Handle: "pri_h_overage", SyncedAt: parseTime(t, "2026-08-08T12:00:00Z")},
				{Plan: "pro", Kind: api.BillingCatalogKindMonthly, Handle: "pri_p_monthly", SyncedAt: parseTime(t, "2026-08-08T12:00:00Z")},
			},
		}
	})
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdBillingStatus(nil); code != 0 {
		t.Fatalf("cmdBillingStatus = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{"Provider:", "paddle", "pri_h_monthly", "pri_p_monthly"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q; got: %q", want, out)
		}
	}
}

func TestCmdBillingStatus_EmptyCatalogHints(t *testing.T) {
	apiURL := billingCatalogStub(t, func() api.BillingCatalogResponse {
		return api.BillingCatalogResponse{Provider: "paddle", SyncedAt: ""}
	})
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdBillingStatus(nil); code != 0 {
		t.Fatalf("cmdBillingStatus = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "never synced") {
		t.Errorf("status output missing 'never synced'; got: %q", out)
	}
	if !strings.Contains(out, "gregale billing price-catalog sync") {
		t.Errorf("status output missing actionable hint; got: %q", out)
	}
}

func TestCmdBillingStatus_RejectsArgs(t *testing.T) {
	t.Setenv("FAAS_API", "http://localhost")
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stderr, restore := captureStderr(t)
	defer restore()
	if code := cmdBillingStatus([]string{"junk"}); code != 1 {
		t.Errorf("cmdBillingStatus with extra args = %d, want 1", code)
	}
	// PR-P4 changed the rejection message to enumerate the four
	// accepted flags; the test asserts the new shape so a future
	// regression that drops the message back to the old "unexpected
	// args" string (pre-PR-P4) is caught.
	if !strings.Contains(stderr.String(), "unexpected arg") {
		t.Errorf("stderr missing 'unexpected arg'; got: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "--watch") {
		t.Errorf("stderr missing '--watch' flag hint; got: %q", stderr.String())
	}
}

// TestParseBillingStatusFlags pins PR-P4's --watch / --json / --no-clear
// flag parser. Lives next to TestCmdBillingStatus_RejectsArgs so the
// full CLI-flag contract for `gregale billing status` is documented in
// one file.
func TestParseBillingStatusFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantWatch bool
		wantDur   time.Duration
		wantJSON  bool
		wantClear bool
		wantErr   bool
	}{
		{name: "empty", args: nil, wantDur: 0},
		{name: "watch no value", args: []string{"--watch"}, wantWatch: true, wantDur: 60 * time.Second},
		{name: "watch inline value", args: []string{"--watch=30"}, wantWatch: true, wantDur: 30 * time.Second},
		{name: "watch spaced value", args: []string{"--watch", "15"}, wantWatch: true, wantDur: 15 * time.Second},
		{name: "json", args: []string{"--json"}, wantJSON: true},
		{name: "no-clear", args: []string{"--no-clear"}, wantClear: true},
		{name: "combo", args: []string{"--watch=10", "--json", "--no-clear"}, wantWatch: true, wantDur: 10 * time.Second, wantJSON: true, wantClear: true},
		{name: "bad value", args: []string{"--watch=junk"}, wantErr: true},
		{name: "unknown flag", args: []string{"--bogus"}, wantErr: true},
		{name: "zero duration", args: []string{"--watch=0"}, wantWatch: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			watch, dur, asJSON, noClear, err := parseBillingStatusFlags(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if watch != tt.wantWatch {
				t.Errorf("watch = %v, want %v", watch, tt.wantWatch)
			}
			if tt.wantWatch && dur != tt.wantDur {
				t.Errorf("dur = %v, want %v", dur, tt.wantDur)
			}
			if asJSON != tt.wantJSON {
				t.Errorf("json = %v, want %v", asJSON, tt.wantJSON)
			}
			if noClear != tt.wantClear {
				t.Errorf("noClear = %v, want %v", noClear, tt.wantClear)
			}
		})
	}
}

// parseTime is a tiny RFC 3339 helper used by the catalog stub.
func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parseTime(%q): %v", s, err)
	}
	return tt
}

// captureStdout / captureStderr are declared in cli_login_test.go
// (commands2_test.go has the matching helpers). The browser package
// is referenced to ensure we exercise the seam that cmdBillingPortal
// touches.
var _ = browser.Default

// ─── Issue #242: retry / cancel / payment-method ────────────────────────
//
// These three subcommands slot into the dispatch help text added at
// commands_billing.go::printBillingUsage. The stubs mirror the
// existing billingPortalStub shape — minimal apid-side answer for
// each of the new endpoints.

// billingMutationsStub mounts both POST /v1/billing/retry and
// POST /v1/billing/cancel with the configure() responses. Tests
// that need only one of the two call the matching subcommand and
// the other route goes unhit. The mux 404s anything else so a
// misrouted request is visible in test output.
func billingMutationsStub(t *testing.T, retry func() api.BillingRetryResponse, cancel func() api.BillingCancelResponse) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/billing/retry", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(retry())
	})
	mux.HandleFunc("/v1/billing/cancel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cancel())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestCmdBillingRetry_Success pins the happy path: POST returns
// 200 with attempt + provider IDs, CLI prints both. --json emits
// the raw envelope.
func TestCmdBillingRetry_Success(t *testing.T) {
	apiURL := billingMutationsStub(t,
		func() api.BillingRetryResponse {
			return api.BillingRetryResponse{
				AttemptID:     "in_1abcd",
				ProviderRefID: "pi_1abcd",
				Status:        "pending_provider_confirmation",
			}
		},
		func() api.BillingCancelResponse {
			t.Fatal("cancel route should not be hit on retry")
			return api.BillingCancelResponse{}
		},
	)
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdBillingRetry(nil); code != 0 {
		t.Errorf("cmdBillingRetry = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{"in_1abcd", "pi_1abcd", "pending_provider_confirmation"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q; got: %q", want, out)
		}
	}
}

// TestCmdBillingRetry_NoOpenCharge prints the friendly hint when
// the server returns 404 + code=billing_no_open_charge (account in
// good standing; the dunning email was stale).
func TestCmdBillingRetry_NoOpenCharge(t *testing.T) {
	// Override the stub with a 404-only variant.
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/billing/retry", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(api.Problem{
			Status: http.StatusNotFound,
			Code:   "billing_no_open_charge",
			Title:  "No open charge to retry",
			Detail: "the account is in good standing; no open invoice or transaction to retry",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	apiURL := srv.URL

	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stderr, restore := captureStderr(t)
	defer restore()
	if code := cmdBillingRetry(nil); code != 1 {
		t.Errorf("cmdBillingRetry no-open-charge = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "good standing") {
		t.Errorf("stderr missing friendly hint; got: %q", stderr.String())
	}
}

// TestCmdBillingRetry_RequiresLogin pins the no-auth path.
func TestCmdBillingRetry_RequiresLogin(t *testing.T) {
	requireNoAuth(t)
	if code := cmdBillingRetry(nil); code != 2 {
		t.Errorf("cmdBillingRetry no-auth = %d, want 2", code)
	}
}

// TestCmdBillingCancel_Success pins the happy path. The y/N
// confirm is skipped via --yes (the typed-confirm path is the
// same code path; tested separately).
func TestCmdBillingCancel_Success(t *testing.T) {
	apiURL := billingMutationsStub(t,
		func() api.BillingRetryResponse { return api.BillingRetryResponse{} },
		func() api.BillingCancelResponse {
			return api.BillingCancelResponse{
				CancelScheduled: true,
				EffectiveAt:     parseTime(t, "2026-09-08T00:00:00Z"),
			}
		},
	)
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdBillingCancel([]string{"--yes"}); code != 0 {
		t.Errorf("cmdBillingCancel --yes = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "2026-09-08") {
		t.Errorf("stdout missing effective date; got: %q", stdout.String())
	}
}

// TestCmdBillingCancel_AlreadyCancelledFriendlyHint: server
// returns 409 + code=billing_already_cancelled; CLI renders the
// hint instead of the SDK error.
func TestCmdBillingCancel_AlreadyCancelledFriendlyHint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/billing/cancel", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(api.Problem{
			Status: http.StatusConflict,
			Code:   "billing_already_cancelled",
			Title:  "No active subscription to cancel",
			Detail: "this account has no active subscription to cancel",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stderr, restore := captureStderr(t)
	defer restore()
	if code := cmdBillingCancel([]string{"--yes"}); code != 1 {
		t.Errorf("cmdBillingCancel already-cancelled = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "No active subscription") {
		t.Errorf("stderr missing friendly hint; got: %q", stderr.String())
	}
}

// TestCmdBillingPaymentMethod_PrintsAndOpens pins the read+open
// flow: card-on-file summary rendered, then browser.Open called
// for the portal URL.
func TestCmdBillingPaymentMethod_PrintsAndOpens(t *testing.T) {
	apiURL := billingPortalStub(t, func() api.BillingPortalResponse {
		return api.BillingPortalResponse{
			URL: "https://billing.example.com/portal?account=acct_42",
			PaymentMethod: &api.PaymentMethodSummary{
				Brand:    "visa",
				Last4:    "4242",
				ExpMonth: 12,
				ExpYear:  2027,
			},
		}
	})
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdBillingPaymentMethod([]string{"--print"}); code != 0 {
		t.Errorf("cmdBillingPaymentMethod --print = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{"visa", "4242", "12/2027", "https://billing.example.com/portal"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q; got: %q", want, out)
		}
	}
}

// TestCmdBillingPaymentMethod_NoCardOnFile pins the no-card path:
// server returns URL but no PaymentMethod block; CLI renders the
// "no payment method on file" hint + opens the portal URL.
func TestCmdBillingPaymentMethod_NoCardOnFile(t *testing.T) {
	apiURL := billingPortalStub(t, func() api.BillingPortalResponse {
		return api.BillingPortalResponse{
			URL: "https://billing.example.com/portal?account=acct_42",
		}
	})
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stderr, restore := captureStderr(t)
	defer restore()
	if code := cmdBillingPaymentMethod([]string{"--print"}); code != 1 {
		t.Errorf("cmdBillingPaymentMethod no-card = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "No payment method") {
		t.Errorf("stderr missing friendly hint; got: %q", stderr.String())
	}
}
