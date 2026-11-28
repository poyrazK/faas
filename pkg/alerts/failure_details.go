package alerts

// ADR-045 amendment 1: a failed_invocations rule can watch one cron, and a
// firing boot_failures, job_failures or failed_invocations notification
// names the newest failures it counted, so the receiver can act without
// opening the dashboard first.

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// failureErrorMaxBytes bounds the error text one note quotes; the note as a
// whole is bounded again by routeAlertNote.
const failureErrorMaxBytes = 200

// observeCronFailures counts one cron's failed invocations, in production
// or in the rule's stage. A store without state.FailureAlertDetailer
// degrades the rule instead of counting every cron.
func (e *Evaluator) observeCronFailures(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	store, ok := e.store.(state.FailureAlertDetailer)
	if !ok || rule.AppID == "" {
		return 0, false, skipDegraded
	}
	since := e.windowStart(rule.WindowSpec, e.now())
	n, err := store.CountFailedCronInvocations(ctx, rule.AccountID, rule.AppID, rule.EnvironmentID, rule.CronID, since)
	if err != nil {
		e.log.Warn("alerts: count cron failures", "rule", rule.ID, "cron", rule.CronID, "err", err)
		return 0, false, skipDegraded
	}
	return float64(n), compareFloat(float64(n), rule.Comparison, rule.Threshold), ""
}

// failureNotes describes the newest failures a firing failure rule counted.
// Any other metric, a store without details or a read failure leaves the
// notification without them.
func (e *Evaluator) failureNotes(ctx context.Context, rule state.AlertRule) []string {
	store, ok := e.store.(state.FailureAlertDetailer)
	if !ok {
		return nil
	}
	now := e.now()
	since := e.windowStart(rule.WindowSpec, now)
	var notes []string
	switch rule.Metric {
	case state.AlertMetricBootFailures:
		if rule.AppID == "" {
			return nil
		}
		boots, err := store.ListAppBootFailures(ctx, rule.AppID, since, now, api.AlertFailureNotes)
		if err != nil {
			e.log.Warn("alerts: read boot failure notes", "rule", rule.ID, "err", err)
			return nil
		}
		for _, b := range boots {
			notes = append(notes, routeAlertNote(failedBootNote(b)))
		}
	case state.AlertMetricJobFailures:
		runs, err := store.ListAccountFailedJobRuns(ctx, rule.AccountID, rule.JobName, since, now, api.AlertFailureNotes)
		if err != nil {
			e.log.Warn("alerts: read job failure notes", "rule", rule.ID, "err", err)
			return nil
		}
		for _, r := range runs {
			notes = append(notes, routeAlertNote(failedJobRunNote(r)))
		}
	case state.AlertMetricFailedInvocs:
		if rule.AppID == "" {
			return nil
		}
		invs, err := store.ListFailedInvocations(ctx, rule.AccountID, rule.AppID, rule.EnvironmentID, rule.FailureSource, rule.CronID, since, api.AlertFailureNotes)
		if err != nil {
			e.log.Warn("alerts: read failed invocation notes", "rule", rule.ID, "err", err)
			return nil
		}
		for _, inv := range invs {
			notes = append(notes, routeAlertNote(failedInvocationNote(inv)))
		}
	}
	return notes
}

const failureNoteTime = "2006-01-02 15:04 UTC"

// failedBootNote names one failed boot: when, which deployment, which node.
func failedBootNote(b state.FailedBoot) string {
	note := "Boot failed " + b.At.Format(failureNoteTime)
	if b.DeploymentID != "" {
		note += ", deployment " + b.DeploymentID
	}
	if b.Method != "" {
		note += " (" + b.Method + ")"
	}
	if b.NodeID != "" {
		note += " on node " + b.NodeID
	}
	return note
}

// failedJobRunNote names one failed run: the job, the run, how many tasks
// failed and the first failed task's error.
func failedJobRunNote(r state.FailedJobRun) string {
	verb := "failed"
	if r.Status == "dead_letter" {
		verb = "dead-lettered"
	}
	note := fmt.Sprintf("Job %s run %s %s %s: %d of %d tasks failed", r.JobName, r.ID, verb, r.FinishedAt.Format(failureNoteTime), r.TasksFailed, r.Tasks)
	if r.FirstError != "" {
		note += " — " + failureErrorText(r.FirstError)
	}
	return note
}

// failedInvocationNote names one failed invocation: its source and path,
// the cron behind it, attempts and the last error.
func failedInvocationNote(inv state.FailedInvocation) string {
	note := fmt.Sprintf("%s %s failed %s", inv.Source, inv.Path, inv.FailedAt.Format(failureNoteTime))
	if inv.CronID != "" {
		note += ", cron " + inv.CronID
	}
	if inv.Attempts > 1 {
		note += fmt.Sprintf(" after %d attempts", inv.Attempts)
	}
	if inv.LastError != "" {
		note += " — " + failureErrorText(inv.LastError)
	}
	return note
}

// failureErrorText keeps one line of an error and bounds it.
func failureErrorText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > failureErrorMaxBytes {
		return truncateUTF8(s, failureErrorMaxBytes-len("…")) + "…"
	}
	return s
}
