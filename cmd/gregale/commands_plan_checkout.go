package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
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
	if ae.Problem.CheckoutURL == "" {
		// An existing subscription's upgrade was not charged (declined, or
		// the bank asked to confirm it): the portal fixes the card, and the
		// upgrade is then retried. The detail says which.
		url := ae.Problem.BillingPortalURL
		PrintWarn(osStderr, "Upgrade to %s not charged: %s", target, ae.Problem.Detail)
		_, _ = fmt.Fprintf(osStdout, "Opening %s\n", url)
		if err := openBrowser(url); err != nil {
			PrintFail(os.Stderr, "Could not open browser: %v", err)
			_, _ = fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", url)
		}
		_, _ = fmt.Fprintf(osStdout, "Then retry: %s\n", billingPlanCommand(string(target)))
		return 0
	}
	url := ae.Problem.CheckoutURL
	PrintWarn(osStderr, "Upgrading to %s needs to be confirmed with the billing provider — finish it in the checkout.", target)
	_, _ = fmt.Fprintf(osStdout, "Opening %s\n", url)
	if err := openBrowser(url); err != nil {
		PrintFail(os.Stderr, "Could not open browser: %v", err)
		_, _ = fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", url)
	}
	_, _ = fmt.Fprintf(osStdout, "Your plan switches to %s as soon as the provider confirms payment. Check with: %s\n", target, planCheckCommand())
	return 0
}

// billingPlanCommand is the `billing plan` command to retry or keep a plan.
// It keeps the global --org so a pasted hint changes the same organization's
// plan, not the personal account's (ADR-20261127200000).
func billingPlanCommand(plan string) string {
	if projectOrganization != "" {
		return "gregale --org " + projectOrganization + " billing plan " + plan
	}
	return "gregale billing plan " + plan
}

// planCheckCommand is the command that shows whether the plan switched.
func planCheckCommand() string {
	if projectOrganization != "" {
		return "gregale org info " + projectOrganization
	}
	return "gregale whoami"
}
