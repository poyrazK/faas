package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsBulkRecovery(args []string, preview bool) int {
	name := "recovery-create"
	if preview {
		name = "recovery-preview"
	}
	flags, positional := splitArgsForFlags(args, "yes", "include-non-retryable")
	fs := newFlagSet("events "+name, flag.ContinueOnError)
	mode := fs.String("mode", "routing", "routing or execution recovery")
	outcome := fs.String("outcome", "", "execution outcome: failed or dead_letter")
	sub := fs.String("subscription-id", "", "filter by captured consumer identifier")
	source := fs.String("event-source", "", "filter by exact event source")
	eventType := fs.String("event-type", "", "filter by exact event type")
	code := fs.String("failure-code", "", "filter by failure classification")
	age := fs.Duration("min-age", 0, "minimum failure age in whole seconds, e.g. 10m")
	include := fs.Bool("include-non-retryable", false, "include failures classified as non-retryable")
	rate := fs.Int("rate", api.EventRecoveryRateDefault, "maximum retries per second (1..100)")
	reason := fs.String("reason", "", "optional operator reason")
	yes := fs.Bool("yes", false, "confirm creating a recovery job")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	req := api.EventRecoveryRequest{Reason: *reason, Mode: *mode, Outcome: *outcome, SubscriptionID: *sub, EventSource: *source, EventType: *eventType, FailureCode: *code, MinAgeSeconds: int64(*age / time.Second), IncludeNonRetryable: *include, RatePerSecond: *rate}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || *age < 0 || *age%time.Second != 0 || *rate < 1 || req.Validate() != nil || !preview && !*yes {
		PrintUsage(os.Stderr, "usage: gregale events "+name+" <app> [--mode routing|execution] [--outcome failed|dead_letter] [--subscription-id ID] [--event-source SOURCE] [--event-type TYPE] [--failure-code CODE] [--min-age 10m] [--include-non-retryable] [--rate N]"+map[bool]string{true: "", false: " --yes"}[preview], "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if preview {
		out, err := client.PreviewEventRecovery(context.Background(), positional[0], req)
		if err != nil {
			return printErr("Recovery preview failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		_, _ = fmt.Fprintf(osStdout, "Matched: %d | coverage: %s\n", out.MatchedCount, oneLine(out.Coverage))
		if out.ExceedsJobLimit {
			_, _ = fmt.Fprintln(osStdout, "Selection exceeds 10000 recipients; narrow the filters. The match count is a lower bound.")
		}
		writeEventRecoveryItems(out.Sample)
		return 0
	}
	out, err := client.CreateEventRecovery(context.Background(), positional[0], req)
	if err != nil {
		return printErr("Recovery creation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	writeEventRecoveryJob(out)
	return 0
}
func cmdEventsRecoveryJob(args []string, action string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("events recovery-"+action, flag.ContinueOnError)
	reason := fs.String("reason", "", "optional operator reason")
	yes := fs.Bool("yes", false, "confirm changing recovery admissions")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || action != "status" && !*yes || action == "status" && *reason != "" || (api.EventRecoveryControlRequest{Reason: *reason}).Validate() != nil {
		PrintUsage(os.Stderr, "usage: gregale events recovery-"+action+" <job-id>"+map[bool]string{true: " --yes", false: ""}[action != "status"], "events")
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		return printErr("Invalid recovery job ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.EventRecoveryJob
	switch action {
	case "cancel":
		out, err = client.CancelEventRecoveryWithReason(context.Background(), positional[0], api.EventRecoveryControlRequest{Reason: *reason})
	case "pause":
		out, err = client.PauseEventRecoveryWithReason(context.Background(), positional[0], api.EventRecoveryControlRequest{Reason: *reason})
	case "resume":
		out, err = client.ResumeEventRecoveryWithReason(context.Background(), positional[0], api.EventRecoveryControlRequest{Reason: *reason})
	default:
		out, err = client.GetEventRecovery(context.Background(), positional[0])
	}
	if err != nil {
		return printErr("Recovery lookup failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	writeEventRecoveryJob(out)
	return 0
}
func cmdEventsRecoveryItems(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events recovery-items", flag.ContinueOnError)
	after := fs.Int64("after", 0, "last item position from the previous page")
	limit := fs.Int("limit", api.EventRecoveryItemsPageMax, "items per page (1..100)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || *after < 0 || validateCLILimit("limit", *limit, api.EventRecoveryItemsPageMax) != nil {
		PrintUsage(os.Stderr, "usage: gregale events recovery-items <job-id> [--after POSITION] [--limit N]", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		return printErr("Invalid recovery job ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListEventRecoveryItems(context.Background(), positional[0], *after, *limit)
	if err != nil {
		return printErr("Recovery items lookup failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	writeEventRecoveryItems(out.Items)
	if out.NextAfter != 0 {
		_, _ = fmt.Fprintf(osStdout, "Next page: --after %d\n", out.NextAfter)
	}
	return 0
}
func writeEventRecoveryJob(out api.EventRecoveryJob) {
	_, _ = fmt.Fprintf(osStdout, "Recovery %s: %s | selected %d | pending %d | queued %d | skipped %d | cancelled %d\n", oneLine(out.ID), oneLine(out.State), out.SelectedCount, out.PendingCount, out.QueuedCount, out.SkippedCount, out.CancelledCount)
	_, _ = fmt.Fprintf(osStdout, "Admission rate: %d/s\n", out.RatePerSecond)
	if out.PausedAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Paused at: %s\n", out.PausedAt.Format(time.RFC3339))
	}
	if e := out.Execution; e != nil {
		_, _ = fmt.Fprintf(osStdout, "Execution: tracked %d | queued %d | running %d | retrying %d | succeeded %d | failed %d | dead letters %d | expired %d | cancelled %d | superseded %d | unknown %d\n", e.TrackedCount, e.Queued, e.Running, e.Retrying, e.Succeeded, e.Failed, e.DeadLettered, e.Expired, e.Cancelled, e.Superseded, e.Unknown)
	}

}
func writeEventRecoveryItems(items []api.EventRecoveryItem) {
	_, _ = fmt.Fprintln(osStdout, "POSITION\tINVOCATION\tREPLAY\tGENERATION\tSOURCE\tEVENT\tSUBSCRIPTION\tFAILURE\tFAILED AT\tSTATE\tEXECUTION\tATTEMPTS\tREASON")
	for _, item := range items {
		generation, execution, attempts := "", "", ""
		if item.ReplayGeneration != nil {
			generation = fmt.Sprint(*item.ReplayGeneration)
		}
		if item.Execution != nil {
			execution = item.Execution.State
			attempts = fmt.Sprint(item.Execution.Attempts)
		}
		_, _ = fmt.Fprintf(osStdout, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", item.Position, oneLine(item.InvocationID), oneLine(item.ReplayInvocationID), generation, oneLine(item.EventSource), oneLine(item.EventID), oneLine(item.SubscriptionID), oneLine(item.FailureCode), item.FailedAt.Format(time.RFC3339), oneLine(item.State), oneLine(execution), attempts, oneLine(item.Reason))
	}
}

func cmdEventsRecoveryRate(args []string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("events recovery-rate", flag.ContinueOnError)
	rate := fs.Int("rate", 0, "maximum items per second (1..100)")
	reason := fs.String("reason", "", "optional operator reason")
	yes := fs.Bool("yes", false, "confirm changing admission rate")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	req := api.EventRecoveryRateRequest{Reason: *reason, RatePerSecond: *rate}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || !*yes || req.Validate() != nil {
		PrintUsage(os.Stderr, "usage: gregale events recovery-rate <job-id> --rate N --yes", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		return printErr("Invalid recovery job ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.SetEventRecoveryRate(context.Background(), positional[0], req)
	if err != nil {
		return printErr("Recovery rate update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	writeEventRecoveryJob(out)
	return 0
}

func cmdEventsRecoveryList(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events recovery-list", flag.ContinueOnError)
	state := fs.String("state", "", "running, paused, completed, or cancelled")
	mode := fs.String("mode", "", "routing or execution")
	sub := fs.String("subscription-id", "", "filter by selected or captured subscription")
	after := fs.String("created-after", "", "exclusive creation lower bound (RFC3339)")
	before := fs.String("created-before", "", "exclusive creation upper bound (RFC3339)")
	cursor := fs.String("cursor", "", "next cursor from the previous page")
	limit := fs.Int("limit", api.EventRecoveryJobsPageMax, "jobs per page (1..50)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || *limit < 1 {
		PrintUsage(os.Stderr, "usage: gregale events recovery-list <app> [--state STATE] [--mode MODE] [--subscription-id ID] [--created-after TIME] [--created-before TIME] [--cursor CURSOR] [--limit N]", "events")
		return 1
	}
	q := api.EventRecoveryListQuery{State: *state, Mode: *mode, SubscriptionID: *sub, Cursor: *cursor, Limit: *limit}
	for _, bound := range []struct {
		value  string
		target **time.Time
	}{{*after, &q.CreatedAfter}, {*before, &q.CreatedBefore}} {
		value, target := bound.value, bound.target
		if value != "" {
			t, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return printErr("Invalid creation time", err)
			}
			t = t.UTC()
			*target = &t
		}
	}
	if err := q.Validate(); err != nil {
		return printErr("Invalid recovery filters", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListEventRecoveries(context.Background(), positional[0], q)
	if err != nil {
		return printErr("Recovery listing failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	for _, job := range out.Jobs {
		writeEventRecoveryJob(job)
		mode := job.Selection.Mode
		if mode == "" {
			mode = "routing"
		}
		_, _ = fmt.Fprintf(osStdout, "Mode: %s | created: %s | expires: %s\n", oneLine(mode), job.CreatedAt.Format(time.RFC3339), job.ExpiresAt.Format(time.RFC3339))
	}
	if len(out.Jobs) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No matching recovery jobs.")
	}
	if out.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "Next cursor: %s\n", out.NextCursor)
	}
	return 0
}

func cmdEventsRecoveryHistory(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events recovery-history", flag.ContinueOnError)
	after := fs.Int64("after", 0, "last history entry ID from the previous page")
	limit := fs.Int("limit", api.EventRecoveryHistoryPageMax, "entries per page (1..100)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || *after < 0 || *limit < 1 || *limit > api.EventRecoveryHistoryPageMax {
		PrintUsage(os.Stderr, "usage: gregale events recovery-history <job-id> [--after ID] [--limit N]", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		return printErr("Invalid recovery job ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListEventRecoveryHistory(context.Background(), positional[0], *after, *limit)
	if err != nil {
		return printErr("Recovery history failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintln(osStdout, "ID\tTIME\tACTION\tACTOR\tSTATE\tRATE\tREASON")
	for _, entry := range out.Entries {
		_, _ = fmt.Fprintf(osStdout, "%d\t%s\t%s\t%s:%s\t%s -> %s\t%d -> %d\t%s\n", entry.ID, entry.OccurredAt.Format(time.RFC3339), oneLine(entry.Action), oneLine(entry.ActorKind), oneLine(entry.ActorID), oneLine(entry.PreviousState), oneLine(entry.State), entry.PreviousRate, entry.Rate, oneLine(entry.Reason))
	}
	if len(out.Entries) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No retained recovery history entries.")
	}
	if out.NextAfter != 0 {
		_, _ = fmt.Fprintf(osStdout, "Next after: %d\n", out.NextAfter)
	}
	return 0
}

func cmdEventsRecoveryHealth(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events recovery-health", flag.ContinueOnError)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events recovery-health <app>", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetEventRecoveryHealth(context.Background(), positional[0])
	if err != nil {
		return printErr("Recovery health failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Running: %d | paused: %d | stalled: %d | expiring running: %d | expiring paused: %d\n", out.RunningJobs, out.PausedJobs, out.StalledJobs, out.ExpiringJobs, out.PausedExpiringJobs)
	_, _ = fmt.Fprintf(osStdout, "Capacity waiting: %d | prolonged capacity waits: %d\n", out.CapacityWaitingJobs, out.ProlongedCapacityWaitJobs)
	_, _ = fmt.Fprintln(osStdout, "JOB\tMODE\tSTATUS\tPENDING\tRATE\tLAST PROGRESS\tELIGIBLE\tEXPIRES\tEXPIRING")
	for _, job := range out.Jobs {
		progress := "unknown (creation baseline)"
		if job.LastProgressAt != nil {
			progress = job.LastProgressAt.Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%d\t%d/s\t%s\t%s\t%s\t%t\n", oneLine(job.JobID), oneLine(job.Mode), oneLine(job.Status), job.PendingCount, job.RatePerSecond, progress, job.EligibleAt.Format(time.RFC3339), job.ExpiresAt.Format(time.RFC3339), job.Expiring)
		if wait := job.CapacityWait; wait != nil {
			_, _ = fmt.Fprintf(osStdout, "  Capacity: %s | gate: %s | since: %s | last observed: %s | age: %.0fs\n  %s\n", oneLine(wait.Scope), oneLine(wait.Gate), wait.StartedAt.Format(time.RFC3339), wait.ObservedAt.Format(time.RFC3339), wait.AgeSeconds, oneLine(wait.Explanation))
		}
	}
	return 0
}

func cmdEventsRecoveryPreflight(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events recovery-preflight", flag.ContinueOnError)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events recovery-preflight <job-id>", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		return printErr("Invalid recovery job ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetEventRecoveryPreflight(context.Background(), positional[0])
	if err != nil {
		return printErr("Recovery preflight failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Recovery %s: %s | active: %t\nPending: %d | eligible: %d | waiting: %d | likely skipped: %d | unknown: %d\n", oneLine(out.JobID), oneLine(out.State), out.Active, out.PendingCount, out.EligibleCount, out.WaitingCount, out.LikelySkippedCount, out.UnknownCount)
	_, _ = fmt.Fprintf(osStdout, "Optimistic admission minimum: %.0fs at %d/s | remaining lifetime: %.0fs | fits before expiry: %t\n", out.MinimumDrainSeconds, out.RatePerSecond, out.RemainingLifetimeSeconds, out.FitsBeforeExpiry)
	if out.AssumesImmediateResume {
		_, _ = fmt.Fprintln(osStdout, "Timing assumes immediate resume of this paused job.")
	}
	for _, group := range []struct {
		label  string
		counts map[string]int64
	}{{"Reasons", out.ReasonCounts}, {"Capacity scopes", out.CapacityScopes}} {
		if len(group.counts) == 0 {
			continue
		}
		keys := make([]string, 0, len(group.counts))
		for key := range group.counts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		_, _ = fmt.Fprintf(osStdout, "%s:", group.label)
		for _, key := range keys {
			_, _ = fmt.Fprintf(osStdout, " %s=%d", oneLine(key), group.counts[key])
		}
		_, _ = fmt.Fprintln(osStdout)
	}
	_, _ = fmt.Fprintln(osStdout, "Read-only snapshot. Eligibility can change; timing excludes future capacity waits and handler execution.")
	_, _ = fmt.Fprintln(osStdout, "POSITION\tSTATUS\tREASON\tCAPACITY SCOPE")
	for _, item := range out.Sample {
		_, _ = fmt.Fprintf(osStdout, "%d\t%s\t%s\t%s\n", item.Position, oneLine(item.Status), oneLine(item.Reason), oneLine(item.CapacityScope))
	}
	return 0
}
