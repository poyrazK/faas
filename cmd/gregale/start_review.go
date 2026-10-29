package main

import "fmt"

func (r *startRunner) renderAllowance(existing bool) {
	limits := r.account.Limits
	if limits.DeployedApps > 0 {
		remaining := max(0, limits.DeployedApps-r.account.AppCount)
		_, _ = fmt.Fprintf(osStdout, "  Apps     %d/%d in use · %d remaining", r.account.AppCount, limits.DeployedApps, remaining)
		if existing {
			_, _ = fmt.Fprintln(osStdout, " · updates this app")
		} else {
			_, _ = fmt.Fprintln(osStdout, " · this launch needs 1 app slot")
		}
	}
	if limits.RAMMB > 0 && limits.MaxConcurrency > 0 {
		_, _ = fmt.Fprintf(osStdout, "  Plan     %d MB default RAM · %d concurrent instances\n", limits.RAMMB, limits.MaxConcurrency)
	}
	if limits.IncludedGBHours > 0 {
		_, _ = fmt.Fprintf(osStdout, "  Compute  %.2f/%d included GB-h used\n", r.account.UsageGBHours, limits.IncludedGBHours)
	}
	_, _ = fmt.Fprintln(osStdout, "  Billing  Normal plan limits apply. Eligible idle apps park and wake on the next request.")
	_, _ = fmt.Fprintf(osStdout, "  Usage    gregale billing usage · %s/dashboard/usage\n", dashboardBaseURL(r.session.APIBase))
}
