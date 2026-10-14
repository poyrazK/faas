package main

import (
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func printDeliveryHealthConfirmations(items []api.EventDeliveryHealthConfirmation) {
	for _, d := range items {
		_, _ = fmt.Fprintf(osStdout, "Confirmation %s: %s (%s)", oneLine(d.Metric), oneLine(d.Phase), oneLine(d.Reason))
		if d.RequiredSamples > 0 {
			_, _ = fmt.Fprintf(osStdout, " | fresh samples %d/%d | observed duration %.3f/%.3fs | remaining confirmation %.3fs", d.FreshSamples, d.RequiredSamples, d.ElapsedSeconds, d.RequiredSeconds, d.RemainingSeconds)
		}
		if d.ObservedAt != nil {
			_, _ = fmt.Fprintf(osStdout, " | observed %s", d.ObservedAt.Format(time.RFC3339Nano))
		}
		if d.LastConfirmationLossAt != nil {
			_, _ = fmt.Fprintf(osStdout, " | last loss %s at %s", oneLine(d.LastConfirmationLossReason), d.LastConfirmationLossAt.Format(time.RFC3339Nano))
		}
		_, _ = fmt.Fprintln(osStdout)
	}
}
