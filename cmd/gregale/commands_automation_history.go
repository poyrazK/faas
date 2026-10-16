package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdAutomationsHistory(args []string) int {
	fs := newFlagSet("automations history", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	workflow := fs.String("workflow", "", "native workflow name")
	run := fs.String("run", "", "native run UUID")
	version := fs.Int64("policy-version", 0, "policy version filter")
	after := fs.String("after", "", "opaque next page cursor")
	limit := fs.Int("limit", api.WorkflowRecoveryHistoryDefaultPage, "maximum entries")
	start := fs.String("created-after", "", "inclusive RFC3339 start")
	end := fs.String("created-before", "", "inclusive RFC3339 end")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *app == "" {
		return printErr("Invalid history scope", errors.New("provide --app"))
	}
	o := api.WorkflowRecoveryHistoryOptions{Workflow: *workflow, RunID: *run, PolicyVersion: *version, After: *after, Limit: *limit}
	var err error
	if *start != "" {
		o.CreatedAfter, err = time.Parse(time.RFC3339Nano, *start)
		if err != nil {
			return printErr("Invalid history start", err)
		}
	}
	if *end != "" {
		o.CreatedBefore, err = time.Parse(time.RFC3339Nano, *end)
		if err != nil {
			return printErr("Invalid history end", err)
		}
	}
	if err = o.Validate(); err != nil {
		return printErr("Invalid history filters", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	out, err := c.GetWorkflowRecoveryHistory(ctx, *app, o)
	if err != nil {
		return printErr("Could not inspect automation history", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Recovery automation history | %s to %s\n", out.CreatedAfter.Format(time.RFC3339), out.CreatedBefore.Format(time.RFC3339))
	for _, e := range out.Entries {
		provenance := "retained"
		if e.Durable {
			provenance = "durable"
		}
		_, _ = fmt.Fprintf(osStdout, "%s | %s | %s | %s | %s | policy %d | run %s | review %s | child %s | episode %s\n", e.OccurredAt.Format(time.RFC3339), provenance, oneLine(e.Kind), oneLine(e.Outcome), oneLine(e.Reason), e.PolicyVersion, oneLine(e.RunID), oneLine(e.ReviewID), oneLine(e.ChildJobID), oneLine(e.EpisodeID))

		if e.EvidenceKind != "" {
			generation := 0
			if e.ResumeCount != nil {
				generation = *e.ResumeCount
			}
			_, _ = fmt.Fprintf(osStdout, "  Evidence: %s generation %d | action %s | actor %s %s | request %s | notification %s | diagnosis %s\n", oneLine(e.EvidenceKind), generation, oneLine(e.ActionID), oneLine(e.ActorKind), oneLine(e.ActorID), oneLine(e.RequestID), oneLine(e.NotificationEventID), oneLine(e.DiagnosisReason))
			_, _ = fmt.Fprintf(osStdout, "  Links: review %s | evidence %s | job %s\n", oneLine(e.ReviewURL), oneLine(e.EvidenceURL), oneLine(e.JobURL))
		}
	}
	if out.NextAfter != "" {
		_, _ = fmt.Fprintln(osStdout, "Next cursor: "+oneLine(out.NextAfter))
	}
	_, _ = fmt.Fprintln(osStdout, "Durable events retain 30 days, capped at 10000 per app. Latest observations remain snapshots; older events were not backfilled.")
	return 0
}
