// gregale jobs — operator + customer CRUD over the /v1/jobs/* surface
// (ADR-099 PR-E). Mirrors the crons dispatcher shape (commands2.go:
// cmdCrons). The wire surface ships in PR-D; this file pair is the
// CLI binding only.
//
// Subcommand vocabulary is the URL noun set without `/jobs/{id}`-named
// verbs: `list` / `info` / `rm` operate on a job id (32 hex); `create`
// / `update` accept a partial-flag set; `run` enqueues a JobRun;
// `runs` / `cancel` walk the run-id sub-tree.
//
// Glyphs are routed through output.go (Enabled() gate) — never
// emitted via fmt.Fprintf literals, per the package-wide rule
// enforced by lint_tripwires_test.go.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// jobIDPattern is the 32-hex shape used by the API for job ids
// (JobResponse.ID, the path segment of /v1/jobs/{id}). Same generator
// as crons (uuid.NewString, hyphens stripped). Validated locally
// BEFORE the network round-trip so a bad id returns 1 with zero
// server calls, matching cmdCronsUpdate / cmdCronsInfo.
var jobIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// int32Ptr / jobStringPtr are partial-update helpers. The wire update
// shape uses *int32 / *string so absent-vs-zero is distinguishable
// (UpdateJobRequest, pkg/api/dto.go:4607), and the CLI uses fs.Visit
// to know which fields the user actually set on the command line.
// Keep these helpers next to the dispatcher so the rule is local.
// jobStringPtr is prefixed to avoid colliding with the test file's
// package-local stringPtr (commands_crons_fire_now_test.go:43).
func int32Ptr(i int32) *int32       { return &i }
func jobStringPtr(s string) *string { return &s }

// parseEnvOverrides parses --env k=v pairs from a flag. Repeated
// --env flags accumulate into one map; an empty value is allowed
// (used to delete an env var on update). The server treats the map
// as the new overlay — empty-string entries are still part of the
// overlay, NOT deletes (that's a contract server-side; mirror it).
func parseEnvOverrides(envs []string) map[string]string {
	if len(envs) == 0 {
		return nil
	}
	out := make(map[string]string, len(envs))
	for _, e := range envs {
		i := strings.IndexByte(e, '=')
		if i < 0 {
			// No `=`: treat the whole token as the key with empty
			// value. Matches kubectl/docker --env semantics.
			out[e] = ""
			continue
		}
		out[e[:i]] = e[i+1:]
	}
	return out
}

// cmdJobs implements the `gregale jobs` dispatcher. Mirrors
// commands2.go:cmdCrons — eight verbs, switch + lookupCliCommand + a
// suggestSubcommand fallback for unknown positionals.
func cmdJobs(args []string) int {
	parent, _ := lookupCliCommand("jobs")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs <list|create|info|update|rm|run|runs|cancel> [args]", "jobs")
		return 1
	}
	switch args[0] {
	case "list":
		return cmdJobsList(args[1:])
	case "create":
		return cmdJobsCreate(args[1:])
	case "info":
		return cmdJobsInfo(args[1:])
	case "update":
		return cmdJobsUpdate(args[1:])
	case "rm":
		return cmdJobsRm(args[1:])
	case "run":
		return cmdJobsRun(args[1:])
	case "runs":
		return cmdJobsRuns(args[1:])
	case "cancel":
		return cmdJobsCancel(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown jobs subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// cmdJobsList implements `gregale jobs list [--before C] [--limit N]`.
// Mirrors cmdDeploymentsList in commands2.go (same pagination shape:
// cursor is the last job id from the prior page; limit is page size
// 1..200, server caps at 100). The NextBefore returned in JSON becomes
// the next --before argument.
//
// Empty list prints nothing in human mode (the cron runs pattern).
// In JSON mode the response is a flat array of JobResponse.
func cmdJobsList(args []string) int {
	fs := flag.NewFlagSet("jobs-list", flag.ContinueOnError)
	before := fs.String("before", "", "pagination cursor (last job id of the prior page)")
	limit := fs.Int("limit", 25, "max rows (1..100; server caps at 100)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs list [--before C] [--limit N]")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	page, err := client.ListJobs(context.Background(), *before, *limit)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	for _, j := range page.Jobs {
		fmt.Printf("%-30s %-15s %s\n", j.Name, j.Status, j.ImageRef)
	}
	return 0
}

// cmdJobsCreate implements `gregale jobs create --name N --image REF
// [--ram MB] [--timeout S] [--parallelism N] [--retry-max N]
// [--env k=v]...`. Mirrors cmdCronsAdd (commands2.go:1500) — flags
// pass-through to CreateJobRequest. Required flags (--name --image)
// fail fast with the usage line; --ram --timeout --parallelism
// --retry-max default to plan-tier minimums the server fills in if
// omitted (server still applies plan caps).
func cmdJobsCreate(args []string) int {
	fs := flag.NewFlagSet("jobs-create", flag.ContinueOnError)
	name := fs.String("name", "", "job slug (lowercase + dashes; required)")
	image := fs.String("image", "", "OCI image reference, sha256:... (required)")
	ram := fs.Int("ram", 256, "RAM MB per task (Hobby 256; Pro 512..2048; Scale 512..4096)")
	timeout := fs.Int("timeout", 300, "per-task timeout seconds (Hobby 300; Pro 1800; Scale 3600)")
	parallelism := fs.Int("parallelism", 1, "max concurrent tasks per run (Hobby 10; Pro 25; Scale 50)")
	retryMax := fs.Int("retry-max", 0, "max retry attempts per task on non-zero exit (0..5)")
	var envs []string
	fs.Func("env", "env override k=v (repeatable)", func(s string) error {
		envs = append(envs, s)
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs create --name <slug> --image <ref> [flags]")
		return 1
	}
	if *name == "" || *image == "" {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs create --name <slug> --image <ref> [flags]")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	j, err := client.CreateJob(context.Background(), api.CreateJobRequest{
		Name:           *name,
		ImageRef:       *image,
		RAMMB:          int32(*ram),
		TaskTimeoutS:   int32(*timeout),
		MaxParallelism: int32(*parallelism),
		RetryMax:       int32(*retryMax),
		EnvOverrides:   parseEnvOverrides(envs),
	})
	if err != nil {
		return printErr("Create failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(j))
	}
	PrintOK(osStdout, "Created job %s", j.Name)
	return 0
}

// cmdJobsInfo implements `gregale jobs info <id>`. Mirrors
// cmdCronsInfo (commands_crons_info.go): flag set, positional id,
// local id regex pre-check, single SDK call, JSON-or-human output
// branch. Same posture as cmdCronsUpdate — the server returns a
// byte-identical 404 on missing or cross-account, so the CLI never
// invents a local branch that could leak existence.
func cmdJobsInfo(args []string) int {
	fs := flag.NewFlagSet("jobs-info", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs info <id>")
		return 1
	}
	id := fs.Arg(0)
	if !jobIDPattern.MatchString(id) {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs info <id>")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetJob(context.Background(), id)
	if err != nil {
		return printErr("Could not load job", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderJobInfo(osStdout, resp)
	return 0
}

// cmdJobsUpdate implements `gregale jobs update <id>
// [--image REF] [--ram MB] [--timeout S] [--parallelism N]
// [--retry-max N] [--env k=v]...`. Mirrors cmdCronsUpdate
// (commands2.go:1569) — partial-update with fs.Visit pattern:
// explicit map distinguishes "unset" from explicit zero so a customer
// can pass `--ram 0` to mean it (server-side zero is rejected with
// job_ram_too_large; the 400 is the operator's signal the value is
// out of range).
func cmdJobsUpdate(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs update <id> [--image REF] [--ram MB] [--timeout S] [--parallelism N] [--retry-max N] [--env k=v]...")
		return 1
	}
	id := args[0]
	if !jobIDPattern.MatchString(id) {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs update <id> [--image REF] [--ram MB] [--timeout S] [--parallelism N] [--retry-max N] [--env k=v]...")
		return 1
	}
	fs := flag.NewFlagSet("jobs-update", flag.ContinueOnError)
	image := fs.String("image", "", "OCI image reference")
	ram := fs.Int("ram", 0, "RAM MB per task")
	timeout := fs.Int("timeout", 0, "per-task timeout seconds")
	parallelism := fs.Int("parallelism", 0, "max concurrent tasks per run")
	retryMax := fs.Int("retry-max", 0, "max retry attempts per task")
	var envs []string
	fs.Func("env", "env override k=v (repeatable)", func(s string) error {
		envs = append(envs, s)
		return nil
	})
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs update <id> [--image REF] [--ram MB] [--timeout S] [--parallelism N] [--retry-max N] [--env k=v]...")
		return 1
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if !explicit["image"] && !explicit["ram"] && !explicit["timeout"] && !explicit["parallelism"] && !explicit["retry-max"] && !explicit["env"] {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs update <id> [--image REF] [--ram MB] [--timeout S] [--parallelism N] [--retry-max N] [--env k=v]...")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var req api.UpdateJobRequest
	if explicit["image"] {
		req.ImageRef = jobStringPtr(*image)
	}
	if explicit["ram"] {
		req.RAMMB = int32Ptr(int32(*ram))
	}
	if explicit["timeout"] {
		req.TaskTimeoutS = int32Ptr(int32(*timeout))
	}
	if explicit["parallelism"] {
		req.MaxParallelism = int32Ptr(int32(*parallelism))
	}
	if explicit["retry-max"] {
		req.RetryMax = int32Ptr(int32(*retryMax))
	}
	if explicit["env"] {
		req.EnvOverrides = parseEnvOverrides(envs)
	}
	updated, err := client.UpdateJob(context.Background(), id, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
	}
	PrintOK(osStdout, "Updated job %s", updated.Name)
	return 0
}

// cmdJobsRm implements `gregale jobs rm <id>`. Soft-delete (the
// server keeps the row with status="deleted"); a follow-up get
// returns 200 with status="deleted". Mirrors cmdCronsRm in commands2.go.
func cmdJobsRm(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs rm <id>")
		return 1
	}
	id := args[0]
	if !jobIDPattern.MatchString(id) {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs rm <id>")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteJob(context.Background(), id); err != nil {
		return printErr("Delete failed", err)
	}
	PrintOK(osStdout, "Removed")
	return 0
}

// cmdJobsRun implements `gregale jobs run <id> --tasks N
// [--parallelism N]`. POST /v1/jobs/{id}/runs — the server creates
// one JobRun with N task rows (queued) and returns the run id.
// parallel runs concurrent; the dispatch tick (runJobsTick) claims
// tasks until the parallelism cap is hit, then fans in terminal
// results. Mirrors cmdCronsRun (commands_crons_fire_now.go:69).
func cmdJobsRun(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs run <id> --tasks N [--parallelism N]")
		return 1
	}
	id := args[0]
	if !jobIDPattern.MatchString(id) {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs run <id> --tasks N [--parallelism N]")
		return 1
	}
	fs := flag.NewFlagSet("jobs-run", flag.ContinueOnError)
	tasks := fs.Int("tasks", 0, "number of tasks to enqueue (required)")
	parallelism := fs.Int("parallelism", 0, "override parallelism (0 = job's max_parallelism)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs run <id> --tasks N [--parallelism N]")
		return 1
	}
	if *tasks <= 0 {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs run <id> --tasks N [--parallelism N]")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	req := api.CreateRunRequest{Tasks: int32(*tasks)}
	if *parallelism > 0 {
		req.Parallelism = int32Ptr(int32(*parallelism))
	}
	r, err := client.CreateRun(context.Background(), id, req)
	if err != nil {
		return printErr("Run enqueue failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(r))
	}
	PrintOK(osStdout, "Run %s enqueued (%d tasks)", r.ID, r.Tasks)
	return 0
}

// cmdJobsRuns implements `gregale jobs runs <job-id> [--before C]
// [--limit N]`. GET /v1/jobs/{id}/runs — newest-first page of run
// rows for one job. Cursor is the run id (UUIDv7 — server emits
// NextBefore as the last row's id when the page is full).
//
// The id is the JOB id (32 hex), not a run id. The URL split is
// `/v1/jobs/{job-id}/runs` and the CLI mirrors it.
func cmdJobsRuns(args []string) int {
	fs := flag.NewFlagSet("jobs-runs", flag.ContinueOnError)
	before := fs.String("before", "", "pagination cursor (run id of the prior page)")
	limit := fs.Int("limit", 10, "max rows (1..100; server caps at 100)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs runs <job-id> [--before C] [--limit N]")
		return 1
	}
	id := fs.Arg(0)
	if !jobIDPattern.MatchString(id) {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs runs <job-id> [--before C] [--limit N]")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	page, err := client.ListRuns(context.Background(), id, *before, *limit)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	for _, r := range page.Runs {
		fmt.Printf("%-30s %-15s %d/%d\n", r.ID, r.AggregateStatus, r.TasksSucceeded, r.Tasks)
	}
	return 0
}

// cmdJobsCancel implements `gregale jobs cancel <run-id>`. POST
// /v1/runs/{run_id}/cancel — server returns 202 Accepted and
// transitions the run into the cancelled aggregate status. Mirrors
// cmdDelayedTaskCancel (commands_delayed_task.go). The run-id is
// the same 32-hex UUIDv7 shape as job ids.
func cmdJobsCancel(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs cancel <run-id>")
		return 1
	}
	id := args[0]
	if !jobIDPattern.MatchString(id) {
		fmt.Fprintln(os.Stderr, "usage: gregale jobs cancel <run-id>")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	r, err := client.CancelRun(context.Background(), id)
	if err != nil {
		return printErr("Cancel failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(r))
	}
	PrintOK(osStdout, "Cancelled run %s", r.ID)
	return 0
}

// renderJobInfo writes one human-mode block for a single job. Mirrors
// renderCronInfo (commands_crons_info.go:73) — column order is the
// dispatcher summary (name / status / image) plus the identity fields.
// UpdatedAt renders as "—" when the job has never been updated (zero
// value); this is the same fallback the cron info block uses.
func renderJobInfo(w io.Writer, j api.JobResponse) {
	_, _ = fmt.Fprintf(w, "job %s\n", j.ID)
	_, _ = fmt.Fprintf(w, "  name:        %s\n", j.Name)
	_, _ = fmt.Fprintf(w, "  kind:        %s\n", j.Kind)
	_, _ = fmt.Fprintf(w, "  status:      %s\n", j.Status)
	_, _ = fmt.Fprintf(w, "  image:       %s\n", j.ImageRef)
	_, _ = fmt.Fprintf(w, "  ram_mb:      %s\n", strconv.FormatInt(int64(j.RAMMB), 10))
	_, _ = fmt.Fprintf(w, "  timeout_s:   %s\n", strconv.FormatInt(int64(j.TaskTimeoutS), 10))
	_, _ = fmt.Fprintf(w, "  parallelism: %s\n", strconv.FormatInt(int64(j.MaxParallelism), 10))
	_, _ = fmt.Fprintf(w, "  retry_max:   %s\n", strconv.FormatInt(int64(j.RetryMax), 10))
	updated := j.UpdatedAt
	if updated == "" {
		updated = "—"
	}
	_, _ = fmt.Fprintf(w, "  updated_at:  %s\n", updated)
}
