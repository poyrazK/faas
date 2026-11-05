// adr: 570
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdAppTrafficStatus(slug string, args []string) int {
	fs := newFlagSet("app traffic-status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status, err := client.GetAppsSlugPolicyStatus(ctx, slug, 0)
	if err != nil {
		return printErr("Could not load traffic status", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	return renderAppTrafficStatus(osStdout, status)
}

func renderAppTrafficStatus(w io.Writer, status api.RuntimePolicyStatusResponse) int {
	runtime := status.TrafficRuntime
	if runtime.State == "" {
		runtime.State = "unverified"
		runtime.ServingGateways, runtime.MissingGateways = status.ServingGateways, status.ServingGateways
	}
	_, _ = fmt.Fprintf(w, "Traffic status\n  Policy convergence: %s (revision %d)\n  Compute gateway wiring: %s; fresh %d/%d, missing %d, stale %d\n",
		status.State, status.DesiredRevision, runtime.State, runtime.FreshGateways, runtime.ServingGateways, runtime.MissingGateways, runtime.StaleGateways)
	for _, feature := range []struct {
		name   string
		status api.TrafficRuntimeFeatureStatus
	}{
		{"Public edge retry", runtime.PublicRetry}, {"Rate counter", runtime.RateCounter}, {"Retry counter", runtime.RetryCounter},
		{"Deadline signing", runtime.DeadlineSigning}, {"Public policy snapshot", runtime.PolicySnapshot}, {"Security revocation", runtime.SecurityRevocation},
		{"Managed HTTP listener", runtime.ManagedHTTP}, {"Managed HTTP adaptive circuit", runtime.ManagedCircuit},
	} {
		value := feature.status.Mode
		if feature.status.State == "" || feature.status.State == "unverified" {
			value = "unverified"
		}
		_, _ = fmt.Fprintf(w, "  %s: %s\n", feature.name, value)
	}
	_, _ = fmt.Fprintln(w, "  Request enforcement: unverified\n  Wiring observations do not attest the public hop, backend health, VM admission, or outbound firewall enforcement.")
	return 0
}
