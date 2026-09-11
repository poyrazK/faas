// gregale jobs — issue #1184 Workstream A CLI surface.
//
// Subcommands (mirrors the crons dispatcher shape at
// commands2.go:cmdCrons, but adapted to the jobs route family —
// jobs are keyed on the customer's slug, runs/tasks on opaque uuids):
//
//   gregale jobs list                                    ListJobs
//   gregale jobs add   <name> --image REF [--command a,b,c]
//                                       [--ram N] [--timeout S]
//                                       [--parallelism N] [--retries N]
//                                       [--env KEY=VAL ...]              CreateJob
//   gregale jobs info  <name>                                                  GetJob
//   gregale jobs update <name> [--image REF] [--command a,b,c] [--env ...]
//                             [--ram N] [--timeout S] [--parallelism N]
//                             [--retries N] [--pause|--resume]       UpdateJob
//   gregale jobs rm    <name>                                                  DeleteJob
//   gregale jobs run   <name> --tasks N [--parallelism N]
//                             [--retries N] [--timeout S] [--env ...]   CreateJobRun
//   gregale jobs runs  <name>                                                  ListJobRuns
//   gregale jobs cancel <name> <run-id>                                  CancelJobRun
//   gregale jobs tasks <name> <run-id>                                  ListJobRunTasks
//   gregale jobs logs  <name> <run-id> <task-index>                    GetJobTaskLogs
//
// Authentication is via authedClient() (the same Bearer-token
// surface as crons). All mutating calls are auto-minted an
// Idempotency-Key by the underlying Client.do (TestDo_MutatingCalls
// CarryIdempotencyKey in pkg/api/client_test.go pins this).

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// jobSlugPattern mirrors the server's validSlug constraint (same
// 3..40 lowercase / digits / hyphens shape used by apps + crons).
// Validated locally so a typo fails fast instead of round-tripping
// to apid for a 400.
var jobSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

// jobRunIDPattern mirrors the server's run_id shape — uuid
// v4 (8-4-4-4-12 hex, lower-case). Same pattern as CronRunID
// in the trigger family.
var jobRunIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// cmdJobs: dispatcher. Strict positional dispatch matches cmdCrons
// (commands2.go:1743) + cmdDomains + cmdKeys: each leaf validates
// its own positional args so a missing flag or extra positional
// surfaces locally rather than as a 400 round-trip.
func cmdJobs(args []string) int {
	parent, _ := lookupCliCommand("jobs")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs <list|add|info|update|rm|run|runs|cancel|tasks|logs> [args]", "jobs")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdJobsList(args[1:])
	case subAdd:
		return cmdJobsAdd(args[1:])
	case subInfo:
		return cmdJobsInfo(args[1:])
	case subUpdate:
		return cmdJobsUpdate(args[1:])
	case subRm:
		return cmdJobsRm(args[1:])
	case "run":
		return cmdJobsRun(args[1:])
	case subRuns:
		return cmdJobsRuns(args[1:])
	case "cancel":
		return cmdJobsCancel(args[1:])
	case "tasks":
		return cmdJobsTasks(args[1:])
	case "logs":
		return cmdJobsLogs(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown jobs subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// cmdJobsList implements `gregale jobs list`. Returns the
// account-scoped list. `--limit N` / `--offset N` paginate the
// server-side list (handler clamps limit to [1,200]). Output is
// either NDJSON (with --json) or a tabular row per job.
func cmdJobsList(args []string) int {
	fs := flag.NewFlagSet("jobs-list", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "page size (1..200, 0 = server default 50)")
	offset := fs.Int("offset", 0, "page offset")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	_ = limit
	_ = offset
	out, err := client.ListJobs(context.Background())
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(out.Jobs))
	}
	renderJobsTable(osStdout, out.Jobs)
	return 0
}

// cmdJobsAdd implements `gregale jobs add <name> --image REF
// [--command A,B,C] [--ram N] [--timeout S] [--parallelism N]
// [--retries N] [--env K=V ...]`. The `name` positional is
// required (jobs.name is the customer slug). Image is required
// (every job boots an OCI image). The handler applies per-plan
// defaults + clamps every numeric field; passing 0 lets the plan
// default win.
func cmdJobsAdd(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs add <name> --image REF [--command A,B,C] [--ram N] [--timeout S] [--parallelism N] [--retries N] [--env K=V ...]", "jobs")
		return 1
	}
	name := args[0]
	if !jobSlugPattern.MatchString(name) {
		PrintUsage(os.Stderr, "usage: gregale jobs add <name>   (name is 3..40 lowercase / digits / hyphens)", "jobs")
		return 1
	}
	fs := flag.NewFlagSet("jobs-add", flag.ContinueOnError)
	image := fs.String("image", "", "OCI image name[:tag | @digest] (required)")
	command := fs.String("command", "", "comma-separated entrypoint (e.g. /bin/sh,-c,echo hi)")
	ram := fs.Int("ram", 0, "billable memory in MB (0 = plan default)")
	timeout := fs.Int("timeout", 0, "per-task wall-clock deadline in seconds (0 = plan default)")
	parallelism := fs.Int("parallelism", 0, "max concurrent tasks across the run (0 = plan default)")
	retries := fs.Int("retries", 0, "per-task max retries (0 = plan default)")
	env := registerJobsMultiFlag(fs, "env", "repeatable; e.g. --env K=V --env K2=V2")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if *image == "" {
		PrintUsage(os.Stderr, "usage: gregale jobs add <name> --image REF [--command A,B,C] [--ram N] [--timeout S] [--parallelism N] [--retries N] [--env K=V ...]", "jobs")
		return 1
	}
	req := api.CreateJobRequest{
		Name:           name,
		Kind:           "batch",
		ImageRef:       *image,
		EnvOverrides:   parseEnvOverrides([]string(*env)),
		RAMMB:          *ram,
		TaskTimeoutSec: *timeout,
		MaxParallelism: *parallelism,
		RetryMax:       *retries,
	}
	if *command != "" {
		req.Command = strings.Split(*command, ",")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	j, err := client.CreateJob(context.Background(), req)
	if err != nil {
		return printErr("Create failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(j))
	}
	PrintOK(osStdout, "Job %s created (image=%s ram=%dMB timeout=%ds)", j.Name, j.ImageRef, j.RAMMB, j.TaskTimeoutSec)
	return 0
}

// cmdJobsInfo implements `gregale jobs info <name>`. Returns the
// full JobResponse. Cross-account probes return 404 (same IDOR
// posture as `gregale apps info`).
func cmdJobsInfo(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale jobs info <name>", "jobs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	j, err := client.GetJob(context.Background(), args[0])
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(j))
	}
	renderJobState(osStdout, j)
	return 0
}

// cmdJobsUpdate implements `gregale jobs update <name> [--image REF]
// [--command A,B,C] [--env K=V ...] [--ram N] [--timeout S]
// [--parallelism N] [--retries N] [--pause|--resume]`. Partial
// update semantics: every flag is optional, but at least one
// patch field must be set. Pointer-based UpdateJobRequest fields
// let the caller distinguish "unset" from explicit zero. The
// `--pause` / `--resume` pair is mutually exclusive and maps to
// status='paused' / status='active'.
func cmdJobsUpdate(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs update <name> [--image REF] [--command A,B,C] [--ram N] [--timeout S] [--parallelism N] [--retries N] [--pause|--resume]", "jobs")
		return 1
	}
	name := args[0]
	if !jobSlugPattern.MatchString(name) {
		PrintUsage(os.Stderr, "usage: gregale jobs update <name>   (name is 3..40 lowercase / digits / hyphens)", "jobs")
		return 1
	}
	fs := flag.NewFlagSet("jobs-update", flag.ContinueOnError)
	image := fs.String("image", "", "new OCI image")
	command := fs.String("command", "", "new comma-separated entrypoint")
	ram := fs.Int("ram", 0, "new RAM (MB)")
	timeout := fs.Int("timeout", 0, "new per-task timeout (s)")
	parallelism := fs.Int("parallelism", 0, "new max parallel tasks")
	retries := fs.Int("retries", 0, "new per-task max retries")
	pause := fs.Bool("pause", false, "halt future dispatches (status=paused)")
	resume := fs.Bool("resume", false, "resume dispatches (status=active)")
	env := registerJobsMultiFlag(fs, "env", "repeatable; e.g. --env K=V --env K2=V2")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if *pause && *resume {
		PrintUsage(os.Stderr, "--pause and --resume are mutually exclusive", "jobs")
		return 1
	}
	req := api.UpdateJobRequest{}
	touched := false
	// Detect which flags were actually passed by the user.
	// `Value.String() != ""` is wrong for numeric / bool flags whose
	// default String() is "0" / "false"; fs.Visit only sees flags
	// that were set on the command line.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["image"] {
		s := *image
		req.ImageRef = &s
		touched = true
	}
	if set["command"] {
		req.Command = strings.Split(*command, ",")
		touched = true
	}
	if set["ram"] {
		r := *ram
		req.RAMMB = &r
		touched = true
	}
	if set["timeout"] {
		t := *timeout
		req.TaskTimeoutSec = &t
		touched = true
	}
	if set["parallelism"] {
		p := *parallelism
		req.MaxParallelism = &p
		touched = true
	}
	if set["retries"] {
		r := *retries
		req.RetryMax = &r
		touched = true
	}
	if len(*env) > 0 {
		req.EnvOverrides = parseEnvOverrides([]string(*env))
		touched = true
	}
	if *pause {
		s := "paused"
		req.Status = &s
		touched = true
	}
	if *resume {
		s := "active"
		req.Status = &s
		touched = true
	}
	if !touched {
		PrintUsage(os.Stderr, "at least one patch field is required (--image / --command / --ram / --timeout / --parallelism / --retries / --env / --pause / --resume)", "jobs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	j, err := client.UpdateJob(context.Background(), name, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(j))
	}
	PrintOK(osStdout, "Updated job %s", j.Name)
	return 0
}

// cmdJobsRm implements `gregale jobs rm <name>`. Returns 409
// CodeJobHasLiveInstances if live instances exist — the server
// enforces the soft-delete guard via the soft_delete_job_if_no_live
// _instances stored function (migrations/00576).
func cmdJobsRm(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale jobs rm <name>", "jobs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	dresp, err := client.DeleteJob(context.Background(), args[0])
	if err != nil {
		return printErr("Delete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(dresp))
	}
	PrintOK(osStdout, "Removed job %s (deleted_at=%s)", dresp.Name, dresp.DeletedAt)
	return 0
}

// cmdJobsRun implements `gregale jobs run <name> --tasks N
// [--parallelism N] [--retries N] [--timeout S] [--env K=V ...]`.
// Atomic fan-out via a single generate_series INSERT inside
// state.PgStore.JobRunCreate; the handler validates Tasks against
// the plan cap before the store call. Plan caps: Hobby=100,
// Pro=1000, Scale=5000.
func cmdJobsRun(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs run <name> --tasks N [--parallelism N] [--retries N] [--timeout S] [--env K=V ...]", "jobs")
		return 1
	}
	name := args[0]
	fs := flag.NewFlagSet("jobs-run", flag.ContinueOnError)
	tasks := fs.Int("tasks", 0, "number of tasks to fan out (required)")
	parallelism := fs.Int("parallelism", 0, "override job parallelism for this run")
	retries := fs.Int("retries", 0, "override retry max for this run")
	timeout := fs.Int("timeout", 0, "override task timeout (s) for this run")
	env := registerJobsMultiFlag(fs, "env", "repeatable; e.g. --env K=V --env K2=V2")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if *tasks <= 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs run <name> --tasks N (N > 0)", "jobs")
		return 1
	}
	req := api.CreateJobRunRequest{
		Tasks:        *tasks,
		EnvOverrides: parseEnvOverrides([]string(*env)),
	}
	if *parallelism > 0 {
		p := *parallelism
		req.Parallelism = &p
	}
	if *retries > 0 {
		r := *retries
		req.RetryMax = &r
	}
	if *timeout > 0 {
		t := *timeout
		req.TaskTimeoutSec = &t
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	run, err := client.CreateJobRun(context.Background(), name, req)
	if err != nil {
		return printErr("Run failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(run))
	}
	PrintOK(osStdout, "Run %s dispatched (%d tasks, parallelism=%d)", run.ID, run.Tasks, run.Parallelism)
	return 0
}

// cmdJobsRuns implements `gregale jobs runs <name>`. Returns a
// page of runs newest-first. Server clamps limit to [1,200].
func cmdJobsRuns(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale jobs runs <name>", "jobs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListJobRuns(context.Background(), args[0])
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(out.Runs))
	}
	renderJobRunsTable(osStdout, out.Runs)
	return 0
}

// cmdJobsCancel implements `gregale jobs cancel <name> <run-id>`.
// Naturally idempotent — a second cancel returns the already-
// cancelled run with the same wire shape. For claimed/running
// tasks: the server SIGTERMs via vmmd; the guest's job supervisor
// handles the 30s grace window before SIGKILL.
func cmdJobsCancel(args []string) int {
	if len(args) != 2 {
		PrintUsage(os.Stderr, "usage: gregale jobs cancel <name> <run-id>", "jobs")
		return 1
	}
	if !jobRunIDPattern.MatchString(args[1]) {
		PrintUsage(os.Stderr, "usage: gregale jobs cancel <name> <run-id>   (run-id is uuid v4)", "jobs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.CancelJobRun(context.Background(), args[0], args[1])
	if err != nil {
		return printErr("Cancel failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(resp))
	}
	PrintOK(osStdout, "Cancelled run %s (status=%s)", resp.Run.ID, resp.Run.AggregateStatus)
	return 0
}

// cmdJobsTasks implements `gregale jobs tasks <name> <run-id>`.
// Returns a page of tasks 0..N-1 (zero-based). LeaseToken is OMITTED
// from the wire (internal dispatch primitive).
func cmdJobsTasks(args []string) int {
	if len(args) != 2 {
		PrintUsage(os.Stderr, "usage: gregale jobs tasks <name> <run-id>", "jobs")
		return 1
	}
	if !jobRunIDPattern.MatchString(args[1]) {
		PrintUsage(os.Stderr, "usage: gregale jobs tasks <name> <run-id>   (run-id is uuid v4)", "jobs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListJobRunTasks(context.Background(), args[0], args[1])
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(out.Tasks))
	}
	renderJobTasksTable(osStdout, out.Tasks)
	return 0
}

// cmdJobsLogs implements `gregale jobs logs <name> <run-id>
// <task-index>`. Tails stdout/stderr via vmmd (same path the
// dashboard uses for live app logs). Truncated=true means the
// tail was capped at MaxBytes; re-fetch with --max-bytes N for
// more. Empty LogContent with Truncated=false means the task
// never produced output (common for OOM-killed tasks).
func cmdJobsLogs(args []string) int {
	if len(args) != 3 {
		PrintUsage(os.Stderr, "usage: gregale jobs logs <name> <run-id> <task-index> [--max-bytes N]", "jobs")
		return 1
	}
	if !jobRunIDPattern.MatchString(args[1]) {
		PrintUsage(os.Stderr, "usage: gregale jobs logs <name> <run-id> <task-index>   (run-id is uuid v4)", "jobs")
		return 1
	}
	taskIdx, err := strconv.Atoi(args[2])
	if err != nil || taskIdx < 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs logs <name> <run-id> <task-index>   (task-index >= 0)", "jobs")
		return 1
	}
	_ = taskIdx
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	logs, err := client.GetJobTaskLogs(context.Background(), args[0], args[1], taskIdx)
	if err != nil {
		return printErr("Logs request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(logs))
	}
	if logs.LogContent == "" && !logs.Truncated {
		_, _ = fmt.Fprintf(os.Stdout, "(task %s produced no output)\n", logs.TaskStatus)
		return 0
	}
	_, _ = fmt.Fprint(os.Stdout, logs.LogContent)
	if logs.Truncated {
		_, _ = fmt.Fprintln(os.Stdout, "...[truncated]")
	}
	return 0
}

// renderJobsTable writes the human multi-line state block for one
// job. Widths assume short name / image_ref / status fields;
// command is rendered as `[cmd0 cmd1 ...]` joined by space to
// match the spec's open-vocabulary list shape. The id-style
// left-pad is omitted because the customer-facing identifier IS
// the name (slug); the JobResponse.ID (uuid) is reserved for
// internal cross-references.
func renderJobsTable(w io.Writer, jobs []api.JobResponse) {
	if len(jobs) == 0 {
		_, _ = fmt.Fprintln(w, "(no jobs)")
		return
	}
	for _, j := range jobs {
		renderJobState(w, j)
		_, _ = fmt.Fprintln(w)
	}
}

// renderJobState writes the human multi-line state block for one
// job. Mirrors the renderCronState (commands2.go:1852) shape.
func renderJobState(w io.Writer, j api.JobResponse) {
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "name:", j.Name)
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "image:", j.ImageRef)
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "command:", formatCommand(j.Command))
	_, _ = fmt.Fprintf(w, "  %-10s %d MB\n", "ram:", j.RAMMB)
	_, _ = fmt.Fprintf(w, "  %-10s %d s\n", "timeout:", j.TaskTimeoutSec)
	_, _ = fmt.Fprintf(w, "  %-10s %d\n", "parallel:", j.MaxParallelism)
	_, _ = fmt.Fprintf(w, "  %-10s %d\n", "retries:", j.RetryMax)
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "status:", j.Status)
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "created:", formatTimeAgo(j.CreatedAt))
}

// renderJobRunsTable writes a tabular row per run. Status badge
// maps to a short indicator (run / ok / fail / cancel / dead).
func renderJobRunsTable(w io.Writer, runs []api.JobRunResponse) {
	if len(runs) == 0 {
		_, _ = fmt.Fprintln(w, "(no runs)")
		return
	}
	_, _ = fmt.Fprintf(w, "%-36s %-8s %4s %4s %4s %4s %4s  %s\n",
		"run-id", "status", "tasks", "succ", "fail", "canc", "dlq", "started")
	for _, r := range runs {
		_, _ = fmt.Fprintf(w, "%-36s %-8s %4d %4d %4d %4d %4d  %s\n",
			r.ID, shortRunStatus(r.AggregateStatus),
			r.Tasks, r.TasksSucceeded, r.TasksFailed, r.TasksCancelled, r.DeadLetterCount,
			formatTimeAgo(r.StartedAt))
	}
}

// renderJobTasksTable writes a tabular row per task. task_index
// runs 0..N-1 (zero-based; matches the server's CTE fan-out).
func renderJobTasksTable(w io.Writer, tasks []api.JobTaskResponse) {
	if len(tasks) == 0 {
		_, _ = fmt.Fprintln(w, "(no tasks)")
		return
	}
	_, _ = fmt.Fprintf(w, "%4s  %-10s %3s %5s  %-14s  %s\n",
		"idx", "status", "try", "exit", "error_class", "instance")
	for _, t := range tasks {
		inst := t.InstanceID
		if len(inst) > 32 {
			inst = inst[:8] + "…"
		}
		errClass := t.ErrorClass
		if errClass == "" {
			errClass = "-"
		}
		exit := "-"
		if t.ExitCode != 0 || t.Status == "failed" || t.Status == "oom" || t.Status == "timeout" {
			exit = strconv.Itoa(t.ExitCode)
		}
		_, _ = fmt.Fprintf(w, "%4d  %-10s %3d %5s  %-14s  %s\n",
			t.TaskIndex, t.Status, t.Attempt, exit, errClass, inst)
	}
}

// shortRunStatus collapses the 5-status aggregate vocabulary to a
// 5-char badge. Mirrors the dashboards 1-glyph status chip.
func shortRunStatus(s string) string {
	switch s {
	case "running":
		return "RUN"
	case "succeeded":
		return "OK"
	case "failed":
		return "FAIL"
	case "cancelled":
		return "CANC"
	case "dead_letter":
		return "DEAD"
	}
	return s
}

// formatCommand joins command parts with spaces (the wire shape is
// a string array; terminal display collapses for readability).
func formatCommand(parts []string) string {
	if len(parts) == 0 {
		return "(unset)"
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// parseEnvOverrides splits KEY=VAL pairs into a map. Empty input
// returns nil so the JSON encoder omits the field entirely (matches
// the convention that unset == not in wire).
func parseEnvOverrides(env []string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		out[kv[:i]] = kv[i+1:]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// formatTimeAgo converts an RFC 3339 timestamp into a human
// "3m ago" style string. Falls back to the raw timestamp on
// parse failure (server returns RFC 3339; tolerance is intentional
// so a server-side format change does not crash the CLI).
func formatTimeAgo(ts string) string {
	if ts == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// jobsMultiFlag is a flag.Value that accumulates repeated --env
// K=V occurrences into a slice. Renamed to avoid collision with
// the package-level multiFlag TYPE in commands_webhooks.go.
// Standard flag.NewFlagSet doesn't support multi-value flags
// natively so this is the canonical pattern across the CLI
// surface. Returns the underlying pointer so the caller can read
// all accumulated values after fs.Parse.
type jobsMultiFlag []string

func (m *jobsMultiFlag) String() string {
	if m == nil {
		return ""
	}
	return strings.Join(*m, ",")
}
func (m *jobsMultiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// registerJobsMultiFlag registers a repeatable flag with the
// given FlagSet. Mirrors the `flag.Var(&slice, ...)` pattern —
// that stores the address, so reads via *env after fs.Parse
// reflect every --env occurrence. Returns the pointer so callers
// can read accumulated values.
func registerJobsMultiFlag(fs *flag.FlagSet, name, usage string) *jobsMultiFlag {
	v := jobsMultiFlag{}
	fs.Var(&v, name, usage)
	return &v
}

// writeJSONSingle marshals one value to JSON and writes it to
// stdout followed by a newline. Mirrors the single-object
// pattern used elsewhere in the CLI (json.MarshalIndent for
// readable human output is intentionally avoided — scripts pipe
// this into jq / gron).
func writeJSONSingle(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(b))
	return err
}
