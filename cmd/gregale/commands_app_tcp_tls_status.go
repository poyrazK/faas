package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdAppsTCPTLSStatus(client *api.Client, slug string, args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale apps tcp <slug> tls-status NAME", "apps")
		return 1
	}
	status, err := client.AppTCPListenerTLSStatus(context.Background(), slug, args[0])
	if err != nil {
		return printErr("TLS status request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	_, _ = fmt.Fprintf(osStdout, "TCP listener %s: TLS=%s hostname=%s enabled=%t\n", status.Name, status.TLS.Mode, status.TLS.Hostname, status.Enabled)
	_, _ = fmt.Fprintln(osStdout, "Certificate evidence from observed edges; fleet coverage unknown.")
	if len(status.Observations) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No edge observations; certificate status unknown.")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "%-24s %-12s %-24s %s\n", "EDGE", "STATUS", "EXPIRES", "OBSERVED")
	for _, observation := range status.Observations {
		expiry := "-"
		if observation.NotAfter != nil {
			expiry = observation.NotAfter.UTC().Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(osStdout, "%-24s %-12s %-24s %s\n", observation.EdgeID, observation.Status, expiry, observation.ObservedAt.UTC().Format(time.RFC3339))
	}
	return 0
}
