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
	_, _ = fmt.Fprintf(osStdout, "Delivery overview for listed secrets (app=%s; scope=%s): %d delivered at current version; %d pending; %d failed; %d unknown or unconfirmed.\n",
		compactInspectFailureText(app), compactInspectFailureText(scopeOrDefault(scope)), delivered, pending, failed, unknown)
	_, _ = fmt.Fprintln(osStdout, "Delivery records describe runtime-start delivery. Runtime reload and application acknowledgement are separate evidence shown in each row; delivery does not confirm application adoption or every active instance.")
	switch {
	case failed > 0 && failed == failedNoInstance:
		_, _ = fmt.Fprintln(osStdout, "The failures were recorded by boots that did not start, and no instance runs now; the next boot delivers these secrets again. Fix why the app does not start (its logs), not the secrets.")
		_, _ = fmt.Fprintf(osStdout, "Next step (POSIX shells):\n  %s logs %s --since 1h\n", recoveryCLI(), recoveryArgument(app))
		return
	case failed > 0:
		_, _ = fmt.Fprintln(osStdout, "Delivery failures were reported. Review current operations before attempting recovery.")
	case pending > 0:
		_, _ = fmt.Fprintln(osStdout, "Pending delivery is not a failure. Review current operations and the serving scope before deciding whether a fresh boot is needed.")
	case unknown > 0:
		_, _ = fmt.Fprintln(osStdout, "Some current-version delivery evidence is unavailable or inconsistent. Review current operations before retrying a write or requesting a restart.")
	default:
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Next step (POSIX shells):\n  %s inspect %s\n", recoveryCLI(), recoveryArgument(app))
}
