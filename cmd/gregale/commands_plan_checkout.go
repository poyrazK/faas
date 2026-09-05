package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/browser"
)

// renderPlanCheckoutHandoff is the `gregale plan <paid>` outcome when
// apid answers 402 with a hosted-checkout (or billing-portal) URL. The
// plan has not changed yet — the billing provider confirms it through
// its webhook once the customer pays — so the CLI's job is to get the
// customer to that page: print the URL, open the browser, and say what
// happens next.
//
// Exit code 0, deliberately: the command did what it could (same
// posture as the "scheduled; current plan remains" downgrade branch and
// cmdDashboard's browser fallback). JSON mode is unchanged — the full
// Problem (with checkout_url / billing_portal_url) is written to stderr
// and the 402 keeps its non-zero exit so scripts can branch on it.
func renderPlanCheckoutHandoff(ae *APIError, target api.Plan) int {
	if jsonOutput {
		_ = writeJSONProblem(ae.Problem)
		return exitCodeForStatus(ae.Problem.Status)
	}
	url, where := ae.Problem.CheckoutURL, "checkout"
	if url == "" {
		url, where = ae.Problem.BillingPortalURL, "billing portal"
	}
	PrintWarn(osStderr, "Upgrading to %s needs to be confirmed with the billing provider — finish it in the %s.", target, where)
	_, _ = fmt.Fprintf(osStdout, "Opening %s\n", url)
	if err := browser.Open(url); err != nil {
		PrintFail(os.Stderr, "Could not open browser: %v", err)
		_, _ = fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", url)
	}
	_, _ = fmt.Fprintf(osStdout, "Your plan switches to %s as soon as the provider confirms payment. Check with: gregale whoami\n", target)
	return 0
}
