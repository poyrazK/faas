package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const wakeAheadUsage = "usage: gregale app wake-ahead <slug> [on|off]"

// cmdWakeAhead reads or changes an app's service wake-ahead opt-in (ADR-977).
func cmdWakeAhead(args []string) int {
	if len(args) < 1 || len(args) > 2 || !validCLISlug(args[0]) {
		PrintUsage(osStderr, wakeAheadUsage, "service-wake-ahead")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	var out api.ServiceWakeAheadResponse
	switch {
	case len(args) == 1:
		out, err = client.GetServiceWakeAhead(ctx, args[0])
	case args[1] == "on" || args[1] == "off":
		out, err = client.SetServiceWakeAhead(ctx, args[0], args[1] == "on")
	default:
		PrintUsage(osStderr, wakeAheadUsage, "service-wake-ahead")
		return 1
	}
	if err != nil {
		return printErr("Could not reach service wake-ahead", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderWakeAhead(osStdout, out)
	return 0
}

func renderWakeAhead(w io.Writer, s api.ServiceWakeAheadResponse) {
	if !s.Enabled {
		_, _ = fmt.Fprintf(w, "Service wake-ahead is off for %s: services it calls wake only when called.\n", s.Slug)
	} else {
		_, _ = fmt.Fprintf(w, "Service wake-ahead is on for %s: when it wakes, Gregale also starts the services it declares (depends_on) until measurements show which it calls soon after waking.\n", s.Slug)
		_, _ = fmt.Fprintln(w, "Those services run, and are billed, from that moment; an unused one parks again after its idle timeout.")
		if s.UpdatedAt == "" {
			// A server reports the project default as on only when never set.
			_, _ = fmt.Fprintf(w, "This is the default for a service in a project under the project service policy; `gregale app wake-ahead %s off` turns it off.\n", s.Slug)
		}
	}
	if s.Learned != nil {
		renderWakeAheadLearned(w, *s.Learned)
	}
}

// renderWakeAheadLearned shows what the gateways measured (ADR-1152).
func renderWakeAheadLearned(w io.Writer, l api.ServiceWakeAheadLearned) {
	if l.Source != api.RouteEvidenceSourceLive {
		_, _ = fmt.Fprintln(w, "\nMeasured services: unavailable (gateway metrics are not reachable).")
		return
	}
	if len(l.Targets) == 0 {
		_, _ = fmt.Fprintf(w, "\nMeasured services: none yet (%d cold wakes observed; a service needs %d).\n", l.CallerWakes, api.ServiceWakeAheadMinWakes)
		return
	}
	_, _ = fmt.Fprintf(w, "\nMeasured services over %d cold wakes (outcomes in the last %s):\n", l.CallerWakes, l.Window)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SERVICE\tCALLED\tPARKED\tQUALIFIES\tWOKEN AHEAD\tUSED\tUNUSED\tFAILED")
	for _, t := range l.Targets {
		_, _ = fmt.Fprintf(tw, "%s\t%.0f%%\t%.0f%%\t%s\t%d\t%d\t%d\t%d\n", t.Slug, t.CallShare*100, t.ColdShare*100,
			map[bool]string{true: "yes", false: "no"}[t.Qualifies], t.Started, t.Used, t.Unused, t.Failed)
	}
	_ = tw.Flush()
}
