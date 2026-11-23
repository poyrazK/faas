package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// Summarize only the returned (possibly filtered) rows. A delivered status
// counts as confirmed only when it refers to the current nonzero version.
func printSecretDeliveryOverview(app, scope string, resp *api.AppSecretListResponse) {
	delivered, pending, failed, unknown := 0, 0, 0, 0
	add := func(status string, current, observed int64) {
		switch secretDeliveryState(status, current, observed) {
		case "delivered":
			delivered++
		case "pending":
			pending++
		case "failed":
			failed++
		default:
			unknown++
		}
	}
	if len(resp.SecretsByScope) > 0 {
		for _, rows := range resp.SecretsByScope {
			for _, row := range rows {
				add(row.DeliveryStatus, row.DeliveryVersion, row.DeliveredVersion)
			}
		}
	} else {
		for _, row := range resp.Secrets {
			add(row.DeliveryStatus, row.DeliveryVersion, row.DeliveredVersion)
		}
	}
	// One line: the counts and what to run. The caveat that delivery records
	// runtime-start delivery, not application adoption, lives in the help
	// notes of `env list`; repeating it on every listing was most of the
	// output agents read in the 2026-11-23 eval.
	line := fmt.Sprintf("Delivery (app=%s; scope=%s): %d delivered at current version, %d pending, %d failed, %d unknown",
		compactInspectFailureText(app), compactInspectFailureText(scopeOrDefault(scope)), delivered, pending, failed, unknown)
	switch {
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
	_, _ = fmt.Fprintf(osStdout, "%s. Next: %s inspect %s\n", line, recoveryCLI(), recoveryArgument(app))
}
