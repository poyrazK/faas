package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func renderInspectWorkerRecovery(w io.Writer, recovery *api.AppWorkerRecovery) {
	if recovery == nil {
		return // Older servers omit this optional evidence.
	}
	if !recovery.Available {
		_, _ = fmt.Fprintf(w, "  worker recovery: unavailable · %s\n", recovery.Reason)
		return
	}
	_, _ = fmt.Fprintf(w, "  worker recovery (app-wide): %s · failures=%d · eligible=%t\n", recovery.Status, recovery.Failures, recovery.Eligible)
	if recovery.NextAttemptAt != nil {
		_, _ = fmt.Fprintf(w, "    next attempt: %s\n", recovery.NextAttemptAt.UTC().Format(time.RFC3339))
	}
	if recovery.LeaseUntil != nil {
		_, _ = fmt.Fprintf(w, "    reconciliation lease until: %s\n", recovery.LeaseUntil.UTC().Format(time.RFC3339))
	}
	if len(recovery.Pools) == 0 {
		_, _ = fmt.Fprintln(w, "    worker pool counts: not observed for this selection")
	}
	for _, pool := range recovery.Pools {
		_, _ = fmt.Fprintf(w, "    worker pool %s: desired=%d running=%d starting=%d draining=%d\n", pool.Scope, pool.Desired, pool.Running, pool.Starting, pool.Draining)
		_, _ = fmt.Fprintf(w, "      observed: %s", pool.ObservedAt.UTC().Format(time.RFC3339))
		if pool.Stale {
			_, _ = fmt.Fprint(w, " · stale")
		}
		_, _ = fmt.Fprintln(w)
	}
}
