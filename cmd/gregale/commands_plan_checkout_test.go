package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// planCheckoutSink answers /v1/account with a Free account and
// PATCH /v1/account/plan with a 402 carrying the given Problem.
func planCheckoutSink(prob api.Problem) *multiSink {
	return &multiSink{
		onAccount: func(string) (int, any) {
			return http.StatusOK, api.AccountResponse{Email: "a@b.c", Plan: "free"}
		},
		onPlan: func([]byte) (int, any) {
			return http.StatusPaymentRequired, prob
		},
	}
}

// TestCmdPlan_CheckoutRequired_OpensBrowser pins the gap that blocked
// every Free → paid upgrade from the terminal: the 402 checkout_url is
// now printed and opened, and the command exits 0 because the hand-off
// is the successful outcome (the plan flips on the provider webhook).
func TestCmdPlan_CheckoutRequired_OpensBrowser(t *testing.T) {
	const checkoutURL = "https://polar.example/checkout/abc"
	srv := httptest.NewServer(planCheckoutSink(api.Problem{
		Status: http.StatusPaymentRequired, Code: api.CodePayment,
		Title: "Billing subscription required", Detail: "plan upgrades to pro require billing confirmation",
		CheckoutURL: checkoutURL,
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	rec := withRecorder(t)
	stdout, restore := captureStdout(t)
	defer restore()
	_, restoreErr := captureStderr(t)
	defer restoreErr()

	if code := cmdPlan([]string{"pro"}); code != 0 {
		t.Errorf("cmdPlan(pro) = %d, want 0 on checkout hand-off", code)
	}
	if len(rec.urls) != 1 || rec.urls[0] != checkoutURL {
		t.Errorf("browser urls = %v, want [%s]", rec.urls, checkoutURL)
	}
	if !strings.Contains(stdout.String(), checkoutURL) {
		t.Errorf("stdout should print the checkout URL: %q", stdout.String())
	}
}

// TestCmdPlan_PortalFallback_OpensPortal: a 402 without checkout_url
// but with billing_portal_url (existing subscription, or a provider
// without hosted checkout) hands off to the portal instead.
func TestCmdPlan_PortalFallback_OpensPortal(t *testing.T) {
	const portalURL = "https://polar.example/portal/session-1"
	srv := httptest.NewServer(planCheckoutSink(api.Problem{
		Status: http.StatusPaymentRequired, Code: api.CodePayment,
		Title: "Billing subscription required", Detail: "update the payment method in the billing portal",
		BillingPortalURL: portalURL,
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	rec := withRecorder(t)
	stdout, restore := captureStdout(t)
	defer restore()
	_, restoreErr := captureStderr(t)
	defer restoreErr()

	if code := cmdPlan([]string{"scale"}); code != 0 {
		t.Errorf("cmdPlan(scale) = %d, want 0 on portal hand-off", code)
	}
	if len(rec.urls) != 1 || rec.urls[0] != portalURL {
		t.Errorf("browser urls = %v, want [%s]", rec.urls, portalURL)
	}
	if !strings.Contains(stdout.String(), portalURL) {
		t.Errorf("stdout should print the portal URL: %q", stdout.String())
	}
}

// TestCmdPlan_CheckoutRequired_JSONModeKeepsProblem: --json callers
// keep the RFC 7807 envelope and the non-zero exit; no browser opens.
func TestCmdPlan_CheckoutRequired_JSONModeKeepsProblem(t *testing.T) {
	srv := httptest.NewServer(planCheckoutSink(api.Problem{
		Status: http.StatusPaymentRequired, Code: api.CodePayment,
		Title: "Billing subscription required", Detail: "checkout required",
		CheckoutURL: "https://polar.example/checkout/abc",
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	rec := withRecorder(t)
	_, restoreErr := captureStderr(t)
	defer restoreErr()
	jsonOutput = true
	defer resetJSONOutput()

	if code := cmdPlan([]string{"pro"}); code != 1 {
		t.Errorf("cmdPlan(pro) --json = %d, want 1 (402 keeps its exit code)", code)
	}
	if len(rec.urls) != 0 {
		t.Errorf("--json must not open a browser, got %v", rec.urls)
	}
}

// TestCmdPlan_PlainPaymentRequiredStillFails: a 402 without any URL
// (no provider on the box) keeps the error path.
func TestCmdPlan_PlainPaymentRequiredStillFails(t *testing.T) {
	srv := httptest.NewServer(planCheckoutSink(api.Problem{
		Status: http.StatusPaymentRequired, Code: api.CodePayment,
		Title: "Billing subscription required", Detail: "no checkout available",
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	rec := withRecorder(t)
	_, restoreErr := captureStderr(t)
	defer restoreErr()

	if code := cmdPlan([]string{"pro"}); code != 1 {
		t.Errorf("cmdPlan(pro) = %d, want 1 without a hand-off URL", code)
	}
	if len(rec.urls) != 0 {
		t.Errorf("no URL → no browser, got %v", rec.urls)
	}
}

// TestRenderAPIError_PrintsBillingHandoffURLs pins the generic 402
// rendering: any command that trips a payment gate now shows the
// checkout URL (preferred) or the portal URL under the detail line.
func TestRenderAPIError_PrintsBillingHandoffURLs(t *testing.T) {
	cases := []struct {
		name, raw, want, forbid string
	}{
		{
			name:   "checkout wins",
			raw:    `{"Problem":{"title":"Billing subscription required","code":"payment_required","status":402,"checkout_url":"https://polar.example/checkout/abc","billing_portal_url":"https://polar.example/portal"}}`,
			want:   "Checkout: https://polar.example/checkout/abc",
			forbid: "Billing portal:",
		},
		{
			name: "portal fallback",
			raw:  `{"Problem":{"title":"Billing subscription required","code":"payment_required","status":402,"billing_portal_url":"https://polar.example/portal"}}`,
			want: "Billing portal: https://polar.example/portal",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ae APIError
			if err := json.Unmarshal([]byte(tc.raw), &ae); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			var buf bytes.Buffer
			renderAPIError(&buf, &ae)
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("output missing %q:\n%s", tc.want, buf.String())
			}
			if tc.forbid != "" && strings.Contains(buf.String(), tc.forbid) {
				t.Errorf("output must not contain %q:\n%s", tc.forbid, buf.String())
			}
		})
	}
}
