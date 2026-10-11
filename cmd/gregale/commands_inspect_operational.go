package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func renderInspectOperational(w io.Writer, summary *api.AppOperationalSummary) {
	_, _ = fmt.Fprintln(w, "\nCurrent operations")
	if summary == nil {
		_, _ = fmt.Fprintln(w, "  unavailable · server did not provide an operational summary")
		return
	}
	monitor := summary.Monitoring
	if !monitor.Available {
		_, _ = fmt.Fprintf(w, "  production routes: unavailable · %s\n", monitor.Reason)
	} else {
		_, _ = fmt.Fprintf(w, "  production routes: %s · %s · coverage=%s\n", monitor.Status, monitor.Reason, monitor.Coverage)
		if monitor.CheckedAt != nil {
			_, _ = fmt.Fprintf(w, "  evaluated: %s\n", monitor.CheckedAt.UTC().Format(time.RFC3339))
		}
		if monitor.WindowStart != nil && monitor.WindowEnd != nil {
			_, _ = fmt.Fprintf(w, "  observation window: %s → %s\n", monitor.WindowStart.UTC().Format(time.RFC3339), monitor.WindowEnd.UTC().Format(time.RFC3339))
		}
	}
	if monitor.Incident != nil {
		_, _ = fmt.Fprintf(w, "  open incident: %s · deployment %s · opened %s\n", monitor.Incident.ID, monitor.Incident.DeploymentID, monitor.Incident.OpenedAt.UTC().Format(time.RFC3339))
	} else if !monitor.IncidentsAvailable {
		_, _ = fmt.Fprintln(w, "  open incidents: unavailable")
	}
	renderInspectRecovery(w, summary.Recovery)
}

func renderInspectRecovery(w io.Writer, recovery api.AppOperationalRecovery) {
	if !recovery.RollbacksAvailable {
		_, _ = fmt.Fprintln(w, "  rollbacks: unavailable")
	} else if len(recovery.Rollbacks) == 0 {
		_, _ = fmt.Fprintln(w, "  rollbacks: no pending operations")
	}
	for _, operation := range recovery.Rollbacks {
		_, _ = fmt.Fprintf(w, "  rollback %s: %s · scope=%s · target=%s", operation.ID, operation.Status, operation.Scope, operation.TargetDeploymentID)
		if operation.Code != "" {
			_, _ = fmt.Fprintf(w, " · %s", operation.Code)
		}
		_, _ = fmt.Fprintln(w)
	}
	if recovery.RollbacksTruncated {
		_, _ = fmt.Fprintln(w, "  More pending rollbacks exist; this list is truncated.")
	}
	if !recovery.RestartsAvailable {
		_, _ = fmt.Fprintln(w, "  restarts: unavailable")
	} else if len(recovery.Restarts) == 0 {
		_, _ = fmt.Fprintln(w, "  restarts: no pending or failed handoffs")
	}
	for _, operation := range recovery.Restarts {
		_, _ = fmt.Fprintf(w, "  restart %s: %s · attempts=%d", operation.WakeID, operation.Status, operation.Attempts)
		if operation.FailureReason != "" {
			_, _ = fmt.Fprintf(w, " · %s", operation.FailureReason)
		}
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintf(w, "    %s\n", operation.ProgressMessage())
	}
	if recovery.RestartsTruncated {
		_, _ = fmt.Fprintln(w, "  More pending or failed restarts exist; this list is truncated.")
	}
}
