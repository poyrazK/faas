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
		for _, blocker := range pool.Blockers {
			renderInspectWorkerBlocker(w, blocker)
		}
		if len(pool.Blockers) == 0 && pool.Desired > pool.Running+pool.Starting {
			_, _ = fmt.Fprintln(w, "      admission blockers: not observed")
		}
	}
}

func renderInspectWorkerBlocker(w io.Writer, b api.WorkerAdmissionBlocker) {
	_, _ = fmt.Fprintf(w, "      admission blocker: %s", b.Code)
	if b.Resource != "" {
		_, _ = fmt.Fprintf(w, " · resource=%s", b.Resource)
	}
	if b.Requested != nil {
		_, _ = fmt.Fprintf(w, " requested=%d", *b.Requested)
	}
	if b.Used != nil {
		_, _ = fmt.Fprintf(w, " used=%d", *b.Used)
	}
	if b.Limit != nil {
		_, _ = fmt.Fprintf(w, " limit=%d", *b.Limit)
	}
	_, _ = fmt.Fprintf(w, " · observed=%s", b.ObservedAt.UTC().Format(time.RFC3339))
	if b.Stale {
		_, _ = fmt.Fprint(w, " · stale")
	}
	if b.RetryAt != nil {
		_, _ = fmt.Fprintf(w, " · retry after %s", b.RetryAt.UTC().Format(time.RFC3339))
	}
	_, _ = fmt.Fprintf(w, "\n        %s\n", workerBlockerAction(b.Code))
}

func workerBlockerAction(code string) string {
	switch code {
	case "account_worker_limit":
		return "Free account worker slots or increase the account plan limit; draining workers hold slots until teardown finishes."
	case "concurrency_limit":
		return "Review this environment's concurrency ceiling and the app's shared concurrency budget."
	case "node_ram", "node_vcpu", "node_cpu":
		return "Add capacity for this resource or wait for existing reservations to be released."
	case "no_admitting_nodes":
		return "Check node availability and maintenance state."
	case "placement_changed":
		return "Wait for the next placement attempt after the node lifecycle change."
	case "node_ports":
		return "Free listener ports on the compute fleet or add capacity."
	case "spend_cap":
		return "Review the account spend cap and billing status."
	case "retry_cooldown":
		return "Wait for the app-wide retry deadline; earlier refusals have their own observation times."
	case "deployment_unavailable":
		return "Check the target deployment's live status."
	default:
		return "Check scheduler admission logs for this observation time."
	}
}
