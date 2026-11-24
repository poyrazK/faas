package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// Summarize only the returned (possibly filtered) rows. A delivered status
// counts as confirmed only when it refers to the current nonzero version.
func printSecretDeliveryOverview(app, scope string, resp *api.AppSecretListResponse) {
	delivered, pending, failed, unknown, failedNoInstance := 0, 0, 0, 0, 0
	add := func(status string, current, observed int64, targetsComplete bool, targets []api.SecretRuntimeReloadObservation) {
		switch secretDeliveryState(status, current, observed) {
		case "delivered":
			delivered++
		case "pending":
			pending++
		case "failed":
			failed++
			if secretFailedWithoutInstance(status, targetsComplete, targets) {
				failedNoInstance++
			}
		default:
			unknown++
		}
	}
	if len(resp.SecretsByScope) > 0 {
		for _, rows := range resp.SecretsByScope {
			for _, row := range rows {
				add(row.DeliveryStatus, row.DeliveryVersion, row.DeliveredVersion, row.RuntimeReloadTargetsComplete, row.RuntimeReloadObservations)
			}
		}
	} else {
		for _, row := range resp.Secrets {
			add(row.DeliveryStatus, row.DeliveryVersion, row.DeliveredVersion, row.RuntimeReloadTargetsComplete, row.RuntimeReloadObservations)
		}
	}
	// One line: the counts and what to run. The caveat that delivery records
	// runtime-start delivery, not application adoption, lives in the help
	// notes of `env list`; repeating it on every listing was most of the
	// output agents read in the 2026-11-23 eval.
	line := fmt.Sprintf("Delivery (app=%s; scope=%s): %d delivered at current version, %d pending, %d failed, %d unknown",
		compactInspectFailureText(app), compactInspectFailureText(scopeOrDefault(scope)), delivered, pending, failed, unknown)
	switch {
	case failed > 0 && failed == failedNoInstance:
		_, _ = fmt.Fprintln(osStdout, "The failures were recorded by boots that did not start, and no instance runs now; the next boot delivers these secrets again. Fix why the app does not start (its logs), not the secrets.")
		_, _ = fmt.Fprintf(osStdout, "Next step (POSIX shells):\n  %s logs %s --since 1h\n", recoveryCLI(), recoveryArgument(app))
		return
	case failed > 0:
		line += "; failures reported"
	case pending > 0:
		line += "; pending is not a failure"
	case unknown > 0:
		line += "; some evidence is unavailable"
	default:
		_, _ = fmt.Fprintln(osStdout, line+".")
		return
	}
	_, _ = fmt.Fprintf(osStdout, "%s. Next: %s app health %s --explain\n", line, recoveryCLI(), recoveryArgument(app))
}
