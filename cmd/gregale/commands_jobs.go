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
//   gregale jobs run   <name> (--tasks N | --input ID=REF ...)
//                             [--parallelism N] [--arg VALUE ...]       CreateJobRun
//   gregale jobs runs  <name>                                                  ListJobRuns
//   gregale jobs cancel <name> <run-id>                                  CancelJobRun
//   gregale jobs tasks <name> <run-id>                                  ListJobRunTasks
//   gregale jobs retry  <name> <run-id> <task-index>                    RetryJobTask
//   gregale jobs logs  <name> <run-id> <task-index> [--max-bytes N]     GetJobTaskLogs
//
// Authentication is via authedClient() (the same Bearer-token
// surface as crons). All mutating calls are auto-minted an
// Idempotency-Key by the underlying Client.do (TestDo_MutatingCalls
// CarryIdempotencyKey in pkg/api/client_test.go pins this).

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/jobresult"
	"github.com/onebox-faas/faas/pkg/workpolicy"
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
		return cmdJobsOverview(nil)
	}
	subcommand, rest := args[0], args[1:]
	switch subcommand {
	case subList:
		return cmdJobsList(rest)
	case subAdd:
		return cmdJobsAdd(rest)
	case subInfo:
		return cmdJobsInfo(rest)
	case subUpdate:
		return cmdJobsUpdate(rest)
	case subRm:
		return cmdJobsRm(rest)
	case "next":
		return cmdJobsNext(rest)
	case "resume":
		return resumeRecordedRun("job", rest)
	case "run":
		return cmdJobsRun(rest)
	case subRuns:
		return cmdJobsRuns(rest)
	case "status":
		return cmdJobsStatus(rest)
	case "occurrences":
		return cmdJobsOccurrences(rest)
	case "overview":
		return cmdJobsOverview(rest)
	case "defaults":
		return cmdJobsDefaults(rest)
	case "results":
		return cmdJobsResults(rest)
	case "wait":
		return cmdJobsWait(rest)
	case "cancel":
		return cmdJobsCancel(rest)
	case "tasks":
		return cmdJobsTasks(rest)
	case "attempts":
		return cmdJobsAttempts(rest)
	case "retry":
		return cmdJobsRetry(rest)
	case "replay-failed":
		return cmdJobsReplayFailed(rest)
	case "artifact-url":
		return cmdJobsArtifactURL(rest)
	case "artifact-download":
		return cmdJobsArtifactDownload(rest)
	case "artifacts":
		return cmdJobsArtifacts(rest)
	case "logs":
		return cmdJobsLogs(rest)
	case "registry":
		return cmdJobsRegistry(rest)
	}
	printCommandValidation(os.Stderr, "unknown jobs subcommand %q\n", subcommand)
	sug, _ := suggestSubcommand(subcommand, parent)
	maybeSuggestSub(sug)
	return 1
}

// cmdJobsList implements `gregale jobs list`. Returns the
// account-scoped list. `--limit N` / `--offset N` paginate the
// server-side list. Output is either the complete pagination envelope
// in JSON mode or a tabular row per job.
func cmdJobsList(args []string) int {
	fs := newFlagSet("jobs-list", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "page size (1..200; omit for server default 50)")
	offset := fs.Int("offset", 0, "starting page offset")
	all := fs.Bool("all", false, "walk every page using --limit and --offset")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	limitProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			limitProvided = true
		}
	})
	if limitProvided && (*limit < 1 || *limit > 200) {
		PrintUsage(os.Stderr, "usage: gregale jobs list [--limit N] [--offset N] [--all]   (--limit must be between 1 and 200)", "jobs")
		return 1
	}
	if *offset < 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs list [--limit N] [--offset N] [--all]   (--offset must be >= 0)", "jobs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.ListJobsResponse
	first := true
	items, next, err := collectOffsetPages(context.Background(), *offset, *all, func(ctx context.Context, offset int) ([]api.JobResponse, int, error) {
		page, err := client.ListJobs(ctx, *limit, offset)
		if first {
			out = page
			first = false
		} else {
			out.Total = page.Total
		}
		return page.Jobs, page.NextOffset, err
	})
	out.Jobs, out.NextOffset = items, next
	if err != nil {
		return printErr("Could not list jobs", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if len(out.Jobs) == 0 {
		next := recoveryCLI() + " jobs list"
		if *offset == 0 {
			next = recoveryCLI() + " jobs add --help"
		}
		printEmptyResult("Jobs", fmt.Sprintf("selected account; starting offset=%d", *offset), next)
		if out.NextOffset >= 0 && out.NextOffset > out.Offset {
			_, _ = fmt.Fprintf(osStdout, "Next page (POSIX shells):\n  %s jobs list --offset %d\n", recoveryCLI(), out.NextOffset)
		}
		return 0
	}
	renderJobsTable(osStdout, out.Jobs)
	if out.NextOffset >= 0 && out.NextOffset > out.Offset {
		_, _ = fmt.Fprintf(osStdout, "next page: gregale jobs list --offset %d\n", out.NextOffset)
	}
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
	if len(args) == 1 && args[0] == "--interactive" { //nolint:gosec // G602: len(args) == 1 guarantees index zero exists.
		return cmdJobsAddInteractive()
	}
	fs := newFlagSet("jobs-add", flag.ContinueOnError)
	image := fs.String("image", "", "OCI image name[:tag | @digest] (required)")
	command := fs.String("command", "", "comma-separated entrypoint (e.g. /bin/sh,-c,echo hi)")
	ram := fs.Int("ram", 0, "billable memory in MB (0 = plan default)")
	timeout := fs.Int("timeout", 0, "per-task wall-clock deadline in seconds (0 = plan default)")
	parallelism := fs.Int("parallelism", 0, "max concurrent tasks across the run (0 = plan default)")
	retries := fs.Int("retries", 0, "per-task max retries (0 = plan default)")
	schedule := fs.String("schedule", "", "recurring five-field cron schedule")
	timezone := fs.String("timezone", "", "IANA timezone for --schedule (default UTC)")
	schedulePolicyJSON := fs.String("schedule-policy", "", "versioned schedule policy as JSON")
	failureRulesJSON := fs.String("failure-rules", "", "versioned retry/failure rules as JSON")
	env := registerJobsMultiFlag(fs, "env", "repeatable; e.g. --env K=V --env K2=V2")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale jobs add <name> --image REF [--schedule EXPR [--timezone TZ]] [--command A,B,C] [--ram N] [--timeout S] [--parallelism N] [--retries N] [--env K=V ...]", "jobs")
		return 1
	}
	name := fs.Arg(0)
	if !jobSlugPattern.MatchString(name) {
		PrintUsage(os.Stderr, "usage: gregale jobs add <name>   (name is 3..40 lowercase / digits / hyphens)", "jobs")
		return 1
	}
	if *image == "" {
		PrintUsage(os.Stderr, "usage: gregale jobs add <name> --image REF [--schedule EXPR [--timezone TZ]] [--command A,B,C] [--ram N] [--timeout S] [--parallelism N] [--retries N] [--env K=V ...]", "jobs")
		return 1
	}
	req := api.CreateJobRequest{
		Name:           name,
		Kind:           "batch",
		Schedule:       *schedule,
		Timezone:       *timezone,
		ImageRef:       *image,
		EnvOverrides:   parseEnvOverrides([]string(*env)),
		RAMMB:          *ram,
		TaskTimeoutSec: *timeout,
		MaxParallelism: *parallelism,
		RetryMax:       *retries,
	}
	if *schedule != "" {
		req.Kind = "recurring"
	}
	if *schedulePolicyJSON != "" {
		policy, err := parseSchedulePolicyJSON(*schedulePolicyJSON)
		if err != nil {
			return printErr("Invalid schedule policy", err)
		}
		req.SchedulePolicy = policy
	}
	if *failureRulesJSON != "" {
		rules, err := parseFailureRulesJSON(*failureRulesJSON)
		if err != nil {
			return printErr("Invalid failure rules", err)
		}
		req.FailureRules = rules
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
		return printErr("Could not create job", err)
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
	return cmdJobsInfoWaitArgs(args)
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
	if len(args) == 1 && args[0] == "--interactive" { //nolint:gosec // G602: len(args) == 1 guarantees index zero exists.
		return cmdJobsUpdateInteractive()
	}
	fs := newFlagSet("jobs-update", flag.ContinueOnError)
	image := fs.String("image", "", "new OCI image")
	command := fs.String("command", "", "new comma-separated entrypoint")
	ram := fs.Int("ram", 0, "new RAM (MB)")
	timeout := fs.Int("timeout", 0, "new per-task timeout (s)")
	parallelism := fs.Int("parallelism", 0, "new max parallel tasks")
	retries := fs.Int("retries", 0, "new per-task max retries")
	schedule := fs.String("schedule", "", "replace recurring five-field cron schedule")
	timezone := fs.String("timezone", "", "replace schedule IANA timezone")
	schedulePolicyJSON := fs.String("schedule-policy", "", "replace versioned schedule policy JSON")
	failureRulesJSON := fs.String("failure-rules", "", "replace versioned retry/failure rules JSON")
	unschedule := fs.Bool("unschedule", false, "remove recurring schedule and convert to batch job")
	pause := fs.Bool("pause", false, "halt future dispatches (status=paused)")
	resume := fs.Bool("resume", false, "resume dispatches (status=active)")
	env := registerJobsMultiFlag(fs, "env", "repeatable; e.g. --env K=V --env K2=V2")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale jobs update <name> [--image REF] [--command A,B,C] [--schedule EXPR] [--timezone TZ] [--unschedule] [--ram N] [--timeout S] [--parallelism N] [--retries N] [--pause|--resume]", "jobs")
		return 1
	}
	name := fs.Arg(0)
	if !jobSlugPattern.MatchString(name) {
		PrintUsage(os.Stderr, "usage: gregale jobs update <name>   (name is 3..40 lowercase / digits / hyphens)", "jobs")
		return 1
	}
	if *pause && *resume {
		PrintUsage(os.Stderr, "--pause and --resume are mutually exclusive", "jobs")
		return 1
	}
	if *unschedule && (*schedule != "" || *timezone != "") {
		PrintUsage(os.Stderr, "--unschedule is mutually exclusive with --schedule and --timezone", "jobs")
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
	if set["schedule"] {
		s := *schedule
		req.Schedule = &s
		touched = true
	}
	if set["timezone"] {
		t := *timezone
		req.Timezone = &t
		touched = true
	}
	if set["schedule-policy"] {
		policy, err := parseSchedulePolicyJSON(*schedulePolicyJSON)
		if err != nil {
			return printErr("Invalid schedule policy", err)
		}
		req.SchedulePolicy = policy
		touched = true
	}
	if set["failure-rules"] {
		rules, err := parseFailureRulesJSON(*failureRulesJSON)
		if err != nil {
			return printErr("Invalid failure rules", err)
		}
		req.FailureRules = rules
		touched = true
	}
	if *unschedule {
		s := ""
		req.Schedule = &s
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
		PrintUsage(os.Stderr, "at least one patch field is required (--image / --command / --schedule / --timezone / --unschedule / --ram / --timeout / --parallelism / --retries / --env / --pause / --resume)", "jobs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	j, err := client.UpdateJob(context.Background(), name, req)
	if err != nil {
		return printErr("Could not update job", err)
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
	if len(args) == 1 && args[0] == "--interactive" { //nolint:gosec // G602: len(args) == 1 guarantees index zero exists.
		return cmdJobsRmInteractive()
	}
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale jobs rm <name>", "jobs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	_, err = client.DeleteJob(context.Background(), args[0])
	if err != nil {
		return printErr("Could not delete job", err)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(map[string]any{"name": args[0], "deleted": true}))
	}
	PrintOK(osStdout, "Removed job %s", args[0])
	return 0
}

// cmdJobsRun implements `gregale jobs run <name> --tasks N
// [--parallelism N] [--retries N] [--timeout S] [--env K=V ...]`.
// Atomic fan-out via a single generate_series INSERT inside
// state.PgStore.JobRunCreate; the handler validates Tasks against
// the plan cap before the store call. Plan caps: Hobby=100,
// Pro=1000, Scale=5000.
func cmdJobsRun(args []string) int {
	if len(args) == 1 && args[0] == "--interactive" { //nolint:gosec // G602: len(args) == 1 guarantees index zero exists.
		return cmdJobsRunInteractive()
	}
	fs := newFlagSet("jobs-run", flag.ContinueOnError)
	savedJob := fs.Bool("saved-job", false, "use the Job name and omitted run settings from local jobs defaults; excludes a positional Job name")
	waitReady := fs.Bool("wait-ready", false, "wait for selected image preparation before submitting")
	imageWaitTimeout := fs.Duration("image-wait-timeout", 5*time.Minute, "image preparation deadline; requires --wait-ready")
	imagePollInterval := fs.Duration("image-poll-interval", 2*time.Second, "image polling interval; requires --wait-ready")
	dryRun := fs.Bool("dry-run", false, "preview effective run settings locally without submitting")
	tasks := fs.Int("tasks", 0, "number of tasks to fan out (or use --input)")
	parallelism := fs.Int("parallelism", 0, "override job parallelism for this run")
	retries := fs.Int("retries", 0, "override retry max for this run")
	timeout := fs.Int("timeout", 0, "override task timeout (s) for this run")
	allArtifacts := fs.Bool("all-artifacts", false, "ignore saved artifact filters for this invocation")
	includes := registerJobsMultiFlag(fs, "include", "repeatable artifact-name glob for collection; requires --out-dir or --results; quote patterns")
	excludes := registerJobsMultiFlag(fs, "exclude", "repeatable artifact-name glob to omit; exclusions take precedence")
	results := fs.Bool("results", false, "wait and collect outputs (default ./results/JOB/RUN_ID)")
	outDir := fs.String("out-dir", "", "collect managed artifacts after success; implies --wait")
	wait := fs.Bool("wait", false, "wait for the accepted run to succeed")
	waitTimeout := fs.Duration("wait-timeout", 10*time.Minute, "maximum wait after acceptance (requires --wait, --out-dir, or --results)")
	pollInterval := fs.Duration("poll-interval", 2*time.Second, "wait polling interval (requires --wait; 1s to 1h)")
	env := registerJobsMultiFlag(fs, "env", "repeatable; e.g. --env K=V --env K2=V2")
	arguments := registerJobsMultiFlag(fs, "arg", "repeatable command argument override")
	inputFlags := registerJobsMultiFlag(fs, "input", "repeatable input binding: ID=REF")
	manifestURI := fs.String("input-manifest-uri", "", "account-readable obj:// URI containing JSON input bindings")
	manifestSHA256 := fs.String("input-manifest-sha256", "", "sha256:<64 hex> digest of exact manifest bytes")
	flexible := fs.Bool("flexible", false, "use spare capacity within a start window")
	failFast := fs.Bool("fail-fast", false, "cancel unstarted tasks after permanent failure")
	eligibleAtFlag := fs.String("eligible-at", "", "earliest task start (RFC3339; flexible runs)")
	latestStartAtFlag := fs.String("latest-start-at", "", "latest task start (RFC3339; required for flexible runs)")
	failureRulesJSON := fs.String("failure-rules", "", "override versioned retry/failure rules as JSON for this run")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 0 && fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale jobs run <name> (--tasks N | --input ID=REF ...) [--parallelism N] [--retries N] [--timeout S] [--env K=V ...] [--arg VALUE ...]", "jobs")
		return 1
	}
	if *savedJob && fs.NArg() != 0 {
		return printErr("Conflicting Job selectors", fmt.Errorf("use --saved-job without a positional Job name, or omit --saved-job and supply the name"))
	}
	imageOptions := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "image-wait-timeout" || f.Name == "image-poll-interval" {
			imageOptions = true
		}
	})
	if err := validateAllJobArtifacts(fs, *allArtifacts); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	sources := jobRunSettingSources(fs, *allArtifacts)
	localDefaults := jobRunPreviewDefaults{}
	name, defaultsErr := applyJobRunDefaults(fs, fs.Arg(0), tasks, results, waitReady, len(*inputFlags) > 0 || *manifestURI != "" || *manifestSHA256 != "", includes, excludes, *allArtifacts, sources, &localDefaults)
	if defaultsErr != nil {
		return printErr("Could not apply Job defaults", defaultsErr)
	}
	if (imageOptions && !*waitReady) || (*waitReady && (*imageWaitTimeout <= 0 || *imagePollInterval <= 0)) {
		return printErr("Invalid image wait options", fmt.Errorf("image timing options require enabled --wait-ready and positive durations. Add --wait-ready, or remove the image timing flags if waiting is disabled; for example --wait-ready --image-wait-timeout 5m --image-poll-interval 2s"))
	}
	outDirSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "out-dir" {
			outDirSet = true
		}
	})
	collectResults := outDirSet || *results
	if *results {
		*wait = true
	}
	patterns := []string(*includes)
	exclusions := []string(*excludes)
	if err := validateJobArtifactExcludes(exclusions); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	if err := validateJobArtifactIncludes(patterns); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	if len(exclusions) > 0 && !collectResults {
		return printErr("Invalid artifact filter", fmt.Errorf("--exclude requires artifact collection. Add --results or --out-dir DIR to your command. Preview collection settings with %s jobs run %s --tasks 1 --results --dry-run", recoveryCLI(), recoveryArgument(name)))
	}
	if len(patterns) > 0 && !collectResults {
		return printErr("Invalid artifact filter", fmt.Errorf("--include requires artifact collection. Add --results or --out-dir DIR to your command. Preview collection settings with %s jobs run %s --tasks 1 --results --dry-run", recoveryCLI(), recoveryArgument(name)))
	}
	if outDirSet {
		if *outDir == "" {
			return printErr("Invalid output directory", fmt.Errorf("--out-dir is empty. Supply a destination such as --out-dir ./results, or omit --out-dir and use --results for a folder per run"))
		}
		absolute, err := filepath.Abs(*outDir)
		if err != nil {
			return printErr("Invalid output directory", err)
		}
		inspected, err := inspectArtifactOutputDirectory(absolute)
		if err != nil {
			return printErr("Invalid output directory", err)
		}
		inspected.Close()
		*outDir = absolute
		*wait = true
	}
	// Pin and inspect the default parent before submitting. The run UUID is
	// appended only after acceptance and validation, never from a latest lookup.
	defaultResultsParent := ""
	if *results && !outDirSet {
		if !jobSlugPattern.MatchString(name) {
			return printErr("Invalid Job name", fmt.Errorf("provide a valid Job name"))
		}
		absolute, err := filepath.Abs(filepath.Join("results", name))
		if err != nil {
			return printErr("Invalid output directory", err)
		}
		inspected, err := inspectArtifactOutputDirectory(absolute)
		if err != nil {
			return printErr("Invalid output directory", err)
		}
		inspected.Close()
		defaultResultsParent = absolute
	}
	waitOptions := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "wait-timeout" || f.Name == "poll-interval" {
			waitOptions = true
		}
	})
	if (waitOptions && !*wait) || (*wait && (*waitTimeout <= 0 || *pollInterval < time.Second || *pollInterval > time.Hour || !jobSlugPattern.MatchString(name))) {
		return printErr("Invalid Job wait options", fmt.Errorf("run waiting requires a valid Job name, a positive wait timeout, and a poll interval between 1s and 1h. Add --wait (or --results/--out-dir) when using timing flags; for example --wait --wait-timeout 10m --poll-interval 2s"))
	}
	retriesProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "retries" {
			retriesProvided = true
		}
	})
	if (*manifestURI == "") != (*manifestSHA256 == "") || (*manifestURI != "" && (*tasks != 0 || len(*inputFlags) != 0)) {
		return printErr("Conflicting input options", fmt.Errorf("--input-manifest-uri and --input-manifest-sha256 must be supplied together. Choose either --tasks N, --input ID=REF, or the manifest pair; remove --tasks/--input when using a manifest"))
	}
	if *tasks <= 0 && len(*inputFlags) == 0 && *manifestURI == "" {
		return printErr("Task selection required", fmt.Errorf("provide a positive --tasks count, input bindings, or an input manifest. To preview one task: %s jobs run %s --tasks 1 --dry-run. To save a task count for the configured Job: %s jobs defaults update --tasks 1; inspect the configured Job first with %s jobs defaults show", recoveryCLI(), recoveryArgument(name), recoveryCLI(), recoveryCLI()))
	}
	req := api.CreateJobRunRequest{
		Tasks:               *tasks,
		EnvOverrides:        parseEnvOverrides([]string(*env)),
		InputManifestURI:    *manifestURI,
		InputManifestSHA256: *manifestSHA256,
	}
	for _, raw := range *inputFlags {
		id, ref, ok := strings.Cut(raw, "=")
		if !ok || id == "" || ref == "" {
			return printErr("Invalid input binding", fmt.Errorf("--input requires a nonempty ID and reference, in the form --input item=obj://bucket/input; replace that example with your input binding"))
		}
		req.Inputs = append(req.Inputs, api.JobRunInput{ID: id, Ref: ref})
	}
	if *flexible {
		req.ExecutionClass = "flexible"
	}
	if *failFast {
		req.FailurePolicy = "fail_fast"
	}
	if *eligibleAtFlag != "" {
		parsed, err := time.Parse(time.RFC3339, *eligibleAtFlag)
		if err != nil {
			PrintUsage(os.Stderr, "--eligible-at must be RFC3339", "jobs")
			return 1
		}
		req.EligibleAt = &parsed
	}
	if *latestStartAtFlag != "" {
		parsed, err := time.Parse(time.RFC3339, *latestStartAtFlag)
		if err != nil {
			PrintUsage(os.Stderr, "--latest-start-at must be RFC3339", "jobs")
			return 1
		}
		req.LatestStartAt = &parsed
	}
	if *failureRulesJSON != "" {
		rules, err := parseFailureRulesJSON(*failureRulesJSON)
		if err != nil {
			return printErr("Invalid failure rules", err)
		}
		req.FailureRules = rules
	}
	if len(*arguments) > 0 {
		args := append([]string(nil), (*arguments)...)
		req.Arguments = &args
	}
	if *parallelism > 0 {
		p := *parallelism
		req.Parallelism = &p
	}
	if retriesProvided {
		if *retries < 0 {
			return printErr("Invalid retry count", fmt.Errorf("--retries must be nonnegative. Use --retries 0 to disable retries, a positive count to allow retries, or omit the flag to use the remote Job default"))
		}
		r := *retries
		req.RetryMax = &r
	}
	if *timeout > 0 {
		t := *timeout
		req.TaskTimeoutSec = &t
	}
	if *dryRun {
		finishJobRunSettingSources(sources, req, *results, outDirSet, collectResults)
		return previewJobRun(name, req, *wait, collectResults, *waitTimeout, *pollInterval, *outDir, defaultResultsParent, patterns, exclusions, *waitReady, *imageWaitTimeout, *imagePollInterval, sources, localDefaults)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	submissionCtx := context.Background()
	if *waitReady {
		if !jobSlugPattern.MatchString(name) {
			return printErr("Invalid Job name", fmt.Errorf("provide a valid Job name"))
		}
		signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		submissionCtx = signalCtx
		if code := waitForJobRunImage(signalCtx, client, name, *imageWaitTimeout, *imagePollInterval); code != 0 {
			return code
		}
		if err := signalCtx.Err(); err != nil {
			return printErr("Run cancelled before submission", err)
		}
	}
	run, err := client.CreateJobRun(submissionCtx, name, req)
	if err != nil {
		return printRunSubmissionError("Run failed", err, recoveryCLI()+" jobs runs "+recoveryArgument(name), false)
	}
	rememberAcceptedJob(name, run)
	if collectResults {
		if defaultResultsParent != "" {
			*outDir = defaultResultsParent
			if jobRunIDPattern.MatchString(run.ID) {
				*outDir = filepath.Join(defaultResultsParent, run.ID)
			}
		}
		if *results && !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Results destination: %s\n", *outDir)
		}
		return waitAndDownloadAcceptedJobRun(client, name, run, *waitTimeout, *pollInterval, *outDir, patterns, exclusions)
	}
	if *wait {
		return waitAcceptedJobRun(client, name, run, *waitTimeout, *pollInterval)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(run))
	}
	PrintOK(osStdout, "Job %s run accepted: %s (%d tasks, parallelism=%d, status=%s)", oneLine(name), run.ID, run.Tasks, run.Parallelism, oneLine(run.AggregateStatus))
	printJobRunNextSteps(name, run.ID)
	return 0
}

// cmdJobsRuns implements `gregale jobs runs <name>`. Returns a
// page of runs newest-first. Server clamps limit to [1,200].
func cmdJobsRuns(args []string) int {
	fs := newFlagSet("jobs-runs", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "page size (1..200)")
	offset := fs.Int("offset", 0, "starting page offset")
	all := fs.Bool("all", false, "walk every page using --limit and --offset")
	failedOnly := fs.Bool("failed-only", false, "show failed and dead-letter runs in fetched history")
	status := fs.String("status", "", "filter returned history by aggregate status")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() > 1 || *offset < 0 || validateCLILimit("limit", *limit, 200) != nil {
		PrintUsage(os.Stderr, "usage: gregale jobs runs [name] [--limit N] [--offset N] [--all] (1 <= N <= 200, offset >= 0)", "jobs")
		return 1
	}
	statusSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "status" {
			statusSet = true
		}
	})
	if statusSet && *failedOnly {
		return printErr("Conflicting history filters", fmt.Errorf("use either --failed-only or --status"))
	}
	if statusSet {
		switch *status {
		case "queued", "running", "succeeded", "failed", "cancelled", "dead_letter":
		default:
			return printErr("Invalid Job run status", fmt.Errorf("--status must be queued, running, succeeded, failed, cancelled, or dead_letter"))
		}
	}
	name := fs.Arg(0)
	if name == "" {
		defaults, path, err := readJobDefaultsForCommand([]string{"jobs", "runs", "my-job"})
		if err != nil {
			return printJobDefaultsSelectionError("Could not read Job defaults", err)
		}
		name = defaults.Job
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: job=%s\n", path, name)
		}
	}
	if !jobSlugPattern.MatchString(name) {
		return printErr("Invalid Job name", fmt.Errorf("provide a valid Job name"))
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	items, next, err := collectOffsetPages(context.Background(), *offset, *all, func(ctx context.Context, offset int) ([]api.JobRunResponse, int, error) {
		page, err := client.ListJobRunsPage(ctx, name, *limit, offset)
		return page.Runs, page.NextOffset, err
	})
	if err != nil {
		return printErr("Could not list job runs", err)
	}
	if statusSet || *failedOnly {
		filtered := make([]api.JobRunResponse, 0)
		for _, run := range items {
			if (statusSet && run.AggregateStatus == *status) || (*failedOnly && (run.AggregateStatus == "failed" || run.AggregateStatus == "dead_letter")) {
				filtered = append(filtered, run)
			}
		}
		items = filtered
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(items))
	}
	if statusSet || *failedOnly {
		filterDescription := "status=" + *status
		filterCommand := "--status " + *status
		if *failedOnly {
			filterDescription = "failed or dead_letter"
			filterCommand = "--failed-only"
		}
		if len(items) == 0 {
			_, _ = fmt.Fprintf(osStdout, "No runs matching %s in the fetched history for Job %s (starting offset=%d). Run states may change.\n", filterDescription, name, *offset)
		} else {
			renderJobRunsTable(osStdout, items)
			if *status == "succeeded" {
				printSucceededJobArtifactCommands(osStdout, name, items)
			}
			if *failedOnly || *status == "failed" || *status == "dead_letter" || *status == "queued" || *status == "running" {
				printJobHistoryNextSteps(osStdout, name, items)
			}
		}
		if next > *offset {
			_, _ = fmt.Fprintf(osStdout, "Next history page with this filter:\n  %s jobs runs %s %s --limit %d --offset %d\n", recoveryCLI(), recoveryArgument(name), filterCommand, *limit, next)
		}
		return 0
	}
	if len(items) == 0 {
		nextCommand := recoveryCLI() + " jobs runs " + recoveryArgument(name)
		if *offset == 0 {
			nextCommand = recoveryCLI() + " jobs info " + recoveryArgument(name)
		}
		printEmptyResult("runs of Job "+name, fmt.Sprintf("starting offset=%d", *offset), nextCommand)
		_, _ = fmt.Fprintln(osStdout, "No returned runs does not establish whether an uncertain submission was accepted.")
		if next > *offset {
			_, _ = fmt.Fprintf(osStdout, "Next page (POSIX shells):\n  %s jobs runs %s --offset %d\n", recoveryCLI(), recoveryArgument(name), next)
		}
		return 0
	}
	renderJobRunsTable(osStdout, items)
	printJobHistoryNextSteps(osStdout, name, items)
	if next >= 0 && next > *offset {
		_, _ = fmt.Fprintf(osStdout, "next page: gregale jobs runs %s --offset %d\n", name, next)
	}
	return 0
}

func cmdJobsOccurrences(args []string) int {
	if len(args) == 1 && args[0] == "--interactive" { //nolint:gosec // G602: len(args) == 1 guarantees index zero exists.
		return cmdJobsOccurrencesInteractive()
	}
	fs := newFlagSet("jobs-occurrences", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "number of occurrence decisions to return (1..200)")
	before := fs.String("before", "", "occurrence id cursor from the previous page")
	fs.StringVar(before, "cursor", "", "alias for --before")
	all := fs.Bool("all", false, "walk every page using --limit and --cursor")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale jobs occurrences <name> [--limit N] [--cursor ID] [--all]", "jobs")
		return 1
	}
	name := fs.Arg(0)
	if *limit < 1 || *limit > 200 {
		PrintUsage(os.Stderr, "--limit must be between 1 and 200", "jobs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var page api.ListScheduleOccurrencesResponse
	first := true
	items, next, err := collectListPages(context.Background(), *before, *all, func(ctx context.Context, cursor string) ([]api.ScheduleOccurrenceResponse, string, error) {
		current, err := client.ListJobScheduleOccurrences(ctx, name, *limit, cursor)
		if first {
			page = current
			first = false
		}
		return current.Occurrences, current.NextBefore, err
	})
	page.Occurrences, page.NextBefore = items, next
	if err != nil {
		return printErr("Could not list job schedule occurrences", err)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(struct {
			api.ListScheduleOccurrencesResponse
			NextCursor string `json:"next_cursor,omitempty"`
		}{page, next}))
	}
	renderScheduleOccurrences(osStdout, page.Occurrences)
	if page.NextBefore != "" {
		_, _ = fmt.Fprintf(osStdout, "next page: gregale jobs occurrences %s --cursor %s\n", name, page.NextBefore)
	}
	return 0
}

// cmdJobsCancel implements `gregale jobs cancel <name> <run-id>`.
// Naturally idempotent — a second cancel returns the already-
// cancelled run with the same wire shape. For claimed/running
// tasks: the server SIGTERMs via vmmd; the guest's job supervisor
// handles the 30s grace window before SIGKILL.
func cmdJobsCancel(args []string) int {
	if len(args) == 1 && args[0] == "--interactive" { //nolint:gosec // G602: len(args) == 1 guarantees index zero exists.
		return cmdJobsCancelInteractive()
	}
	fs := newFlagSet("jobs cancel", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "inspect cancellation target and effects without cancelling")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	args = fs.Args()
	if len(args) != 2 {
		PrintUsage(os.Stderr, "usage: gregale jobs cancel <name> <run-id> [--dry-run]", "jobs")
		return 1
	}
	if !jobRunIDPattern.MatchString(args[1]) {
		PrintUsage(os.Stderr, "usage: gregale jobs cancel <name> <run-id>   (run-id is uuid v4)", "jobs")
		return 1
	}
	if *dryRun && !jobSlugPattern.MatchString(args[0]) {
		return printErr("Invalid cancellation target", errors.New("preview requires a valid Job name and exact run UUID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *dryRun {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return previewJobCancel(readCtx, client, args[0], args[1])
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
	if len(args) == 1 && args[0] == "--interactive" { //nolint:gosec // G602: len(args) == 1 guarantees index zero exists.
		return cmdJobsTasksInteractive()
	}
	fs := newFlagSet("jobs tasks", flag.ContinueOnError)
	includes := registerJobsMultiFlag(fs, "include", "repeatable artifact-name glob; requires --artifacts-only; quote patterns")
	excludes := registerJobsMultiFlag(fs, "exclude", "repeatable artifact-name glob to omit; exclusions take precedence")
	artifactsOnly := fs.Bool("artifacts-only", false, "scan task pages for valid manifests containing artifacts")
	failedOnly := fs.Bool("failed-only", false, "show failed, timeout, and oom tasks across all pages")
	unsuccessfulOnly := fs.Bool("unsuccessful-only", false, "show failed, timeout, oom, and cancelled tasks across all pages")
	all := fs.Bool("all", false, "read all task pages from the starting offset")
	limit := fs.Int("limit", 0, "task page size (1..200; omitted uses server default)")
	offset := fs.Int("offset", 0, "starting task offset")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	patterns := []string(*includes)
	exclusions := []string(*excludes)
	if *failedOnly && *unsuccessfulOnly {
		return printErr("Conflicting task filters", errors.New("use --failed-only or --unsuccessful-only separately"))
	}
	if err := validateJobArtifactExcludes(exclusions); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	if err := validateJobArtifactIncludes(patterns); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	if len(exclusions) > 0 && !*artifactsOnly {
		return printErr("Invalid artifact filter", fmt.Errorf("--exclude requires --artifacts-only"))
	}
	if len(patterns) > 0 && !*artifactsOnly {
		return printErr("Invalid artifact filter", fmt.Errorf("--include requires --artifacts-only"))
	}
	limitSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			limitSet = true
		}
	})
	if *offset < 0 || (limitSet && (*limit < 1 || *limit > 200)) {
		return printErr("Invalid task pagination", fmt.Errorf("use --limit between 1 and 200 and a non-negative --offset"))
	}
	args = fs.Args()
	if len(args) > 2 {
		PrintUsage(os.Stderr, "usage: gregale jobs tasks [JOB] [RUN_ID|latest] [--failed-only | --unsuccessful-only] [--artifacts-only] [--all] [--limit N] [--offset N]", "jobs")
		return 1
	}
	if len(args) == 0 {
		defaults, path, err := readJobDefaultsForCommand([]string{"jobs", "tasks", "my-job"})
		if err != nil {
			return printJobDefaultsSelectionError("Could not read Job defaults", err)
		}
		args = []string{defaults.Job}
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: job=%s\n", path, defaults.Job)
		}
	}
	if len(args) == 1 {
		args = append(args, "latest")
	}
	if !jobSlugPattern.MatchString(args[0]) {
		return printErr("Invalid Job name", fmt.Errorf("provide a valid Job name"))
	}
	if args[1] != "latest" && !jobRunIDPattern.MatchString(args[1]) {
		PrintUsage(os.Stderr, "usage: gregale jobs tasks <name> <run-id|latest>   (run-id is uuid v4)", "jobs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if args[1] == "latest" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	runID, err := resolveJobRunReadArg(ctx, client, args[0], args[1])
	if err != nil {
		return printErr("Could not resolve job run", err)
	}
	if *artifactsOnly {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		job, err := client.GetJob(readCtx, args[0])
		if err != nil {
			return printErr("Could not read Job", err)
		}
		if job.Name != args[0] || job.ID == "" || job.AccountID == "" {
			return printErr("Invalid Job", fmt.Errorf("server did not identify the requested Job"))
		}
		run, err := client.GetJobRun(readCtx, args[0], runID)
		if err != nil {
			return printErr("Could not read Job run", err)
		}
		if run.ID != runID || run.JobID != job.ID || run.AccountID != job.AccountID {
			return printErr("Invalid Job run", fmt.Errorf("server returned another run, Job, or account"))
		}
	}
	var out api.ListJobTasksResponse
	if *failedOnly || *unsuccessfulOnly || *all || *artifactsOnly {
		pageLimit := *limit
		if !limitSet {
			pageLimit = 200
		}
		out.Tasks, err = collectJobTaskPages(client, args[0], runID, pageLimit, *offset, *failedOnly)
	} else {
		out, err = client.ListJobRunTasksPage(ctx, args[0], runID, *limit, *offset)
	}
	if err != nil {
		return printErr("Could not list job tasks", err)
	}
	if *unsuccessfulOnly {
		filtered := make([]api.JobTaskResponse, 0)
		for _, task := range out.Tasks {
			if jobTaskRetryStatus(task.Status) {
				filtered = append(filtered, task)
			}
		}
		out.Tasks = filtered
	}
	manifests := map[int]jobresult.Manifest{}
	if *artifactsOnly {
		filtered := make([]api.JobTaskResponse, 0)
		for _, task := range out.Tasks {
			if len(task.OutputManifest) == 0 || strings.TrimSpace(string(task.OutputManifest)) == "null" {
				continue
			}
			manifest, err := jobresult.Validate(task.OutputManifest)
			if err != nil {
				return printErr("Invalid output manifest", fmt.Errorf("task %d has an invalid recorded manifest; artifact discovery could not be completed", task.TaskIndex))
			}
			matching := make([]jobresult.Artifact, 0, len(manifest.Artifacts))
			for _, artifact := range manifest.Artifacts {
				if jobArtifactSelected(artifact.Name, patterns, exclusions) {
					matching = append(matching, artifact)
				}
			}
			manifest.Artifacts = matching
			if len(manifest.Artifacts) > 0 {
				filtered = append(filtered, task)
				manifests[task.TaskIndex] = manifest
			}
		}
		out.Tasks = filtered
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(out.Tasks))
	}
	if *artifactsOnly {
		statusFilter := ""
		if *failedOnly {
			statusFilter = "failed, timeout, or oom states"
		} else if *unsuccessfulOnly {
			statusFilter = "failed, timeout, oom, or cancelled states"
		}
		return renderJobTasksWithArtifacts(args[0], runID, out.Tasks, manifests, *offset, statusFilter, patterns, exclusions)
	}

	if *unsuccessfulOnly && len(out.Tasks) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No failed, timed-out, out-of-memory, or cancelled tasks found for Job %s run %s. Task states may still change.\n", args[0], runID)
		return 0
	}
	if *failedOnly && len(out.Tasks) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No failed, timed-out, or out-of-memory tasks found for Job %s run %s. Task states may still change.\n", args[0], runID)
		return 0
	}
	renderJobTasksTable(osStdout, out.Tasks)
	printFailedJobTaskLogs(osStdout, args[0], runID, out.Tasks)
	if !*all && !*failedOnly && !*unsuccessfulOnly && out.NextOffset > *offset {
		limitArg := ""
		if limitSet {
			limitArg = fmt.Sprintf(" --limit %d", *limit)
		}
		_, _ = fmt.Fprintf(osStdout, "Next task page (POSIX shells):\n  %s jobs tasks %s %s%s --offset %d\n", recoveryCLI(), recoveryArgument(args[0]), recoveryArgument(runID), limitArg, out.NextOffset)
	}
	return 0
}

// cmdJobsAttempts returns retained terminal outcomes, including earlier
// attempts whose task projection was subsequently retried.
func cmdJobsAttempts(args []string) int {
	if len(args) == 1 && args[0] == "--interactive" { //nolint:gosec // G602: len(args) == 1 guarantees index zero exists.
		return cmdJobsAttemptsInteractive()
	}
	remaining, taskFlag, taskSet, err := splitJobLogTask(args)
	if err != nil {
		return printErr("Invalid task selection", err)
	}
	args = remaining
	fs := newFlagSet("jobs attempts", flag.ContinueOnError)
	failedOnly := fs.Bool("failed-only", false, "show only failed, timeout, and oom retained attempts")
	unsuccessfulOnly := fs.Bool("unsuccessful-only", false, "show failed, timeout, oom, and cancelled retained attempts")
	all := fs.Bool("all", false, "read all retained attempt pages")
	limit := fs.Int("limit", 0, "history page size (1..200; omitted uses server default)")
	offset := fs.Int("offset", 0, "starting history offset")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if *failedOnly && *unsuccessfulOnly {
		return printErr("Conflicting attempt filters", errors.New("use --failed-only or --unsuccessful-only separately"))
	}
	limitSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			limitSet = true
		}
	})
	if *offset < 0 || (limitSet && (*limit < 1 || *limit > 200)) {
		return printErr("Invalid attempt pagination", fmt.Errorf("use --limit between 1 and 200 and a non-negative --offset"))
	}
	args = fs.Args()
	if (taskSet && len(args) > 2) || (!taskSet && len(args) != 3) {
		PrintUsage(os.Stderr, "usage: gregale jobs attempts [JOB] [RUN_ID|latest] --task N [--all] [--limit N] [--offset N] [--failed-only | --unsuccessful-only], or jobs attempts JOB RUN_ID|latest TASK_INDEX", "jobs")
		return 1
	}
	if taskSet {
		if len(args) == 0 {
			defaults, path, err := readJobDefaultsForCommand([]string{"jobs", "attempts", "my-job", "latest", "--task", taskFlag})
			if err != nil {
				return printJobDefaultsSelectionError("Could not read Job defaults", err)
			}
			args = []string{defaults.Job}
			if !jsonOutput {
				_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: job=%s\n", path, defaults.Job)
			}
		}
		if len(args) == 1 {
			args = append(args, "latest")
		}
		if !jobSlugPattern.MatchString(args[0]) {
			return printErr("Invalid Job name", fmt.Errorf("provide a valid Job name"))
		}
		args = append(args, taskFlag)
	}
	if args[1] != "latest" && !jobRunIDPattern.MatchString(args[1]) {
		return printErr("Invalid Job run", fmt.Errorf("provide a valid run UUID or latest"))
	}
	taskIdx, err := strconv.Atoi(args[2])
	if err != nil || taskIdx < 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs attempts <name> <run-id|latest> <task-index>   (task-index >= 0)", "jobs")
		return 1
	}
	if args[1] == "latest" && !jobSlugPattern.MatchString(args[0]) {
		return printErr("Invalid Job name", fmt.Errorf("latest requires a valid Job name"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if args[1] == "latest" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	runID, err := resolveJobRunReadArg(ctx, client, args[0], args[1])
	if err != nil {
		return printErr("Could not resolve job run", err)
	}
	out, err := collectJobAttemptHistory(client, ctx, args[0], runID, taskIdx, *limit, *offset, *all)
	if err != nil {
		return printErr("Could not list job attempts", err)
	}
	if *failedOnly || *unsuccessfulOnly {
		filtered := make([]api.JobTaskAttemptResponse, 0)
		for _, attempt := range out.Attempts {
			if (*unsuccessfulOnly && jobTaskRetryStatus(attempt.Status)) || (*failedOnly && (attempt.Status == "failed" || attempt.Status == "timeout" || attempt.Status == "oom")) {
				filtered = append(filtered, attempt)
			}
		}
		out.Attempts = filtered
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(out))
	}
	for _, attempt := range out.Attempts {
		outcomeCode := attempt.OutcomeCode
		if outcomeCode == "" {
			outcomeCode = "-"
		}
		if _, err := fmt.Fprintf(osStdout, "%d\t%s\t%s\t%s\t%s\t%s\n", attempt.Attempt, attempt.Status, attempt.InputID, outcomeCode, workDecisionLabel(attempt.WorkDecision), attempt.ErrorMessage); err != nil {
			return printErr("Output failed", err)
		}
		if jobSlugPattern.MatchString(args[0]) && attempt.RunID == runID && attempt.TaskIndex == taskIdx && attempt.Attempt > 0 {
			command := savedNameSuggestionCommand([]string{"jobs", "logs", args[0], runID, strconv.Itoa(taskIdx), "--attempt", strconv.Itoa(attempt.Attempt)})
			if _, err := fmt.Fprintf(osStdout, "  Retained output (POSIX shells): %s\n", command); err != nil {
				return printErr("Output failed", err)
			}
			if manifest, err := jobresult.Validate(attempt.OutputManifest); err == nil && len(manifest.Artifacts) > 0 {
				command := savedNameSuggestionCommand([]string{"jobs", "artifacts", args[0], runID, strconv.Itoa(taskIdx), "--attempt", strconv.Itoa(attempt.Attempt)})
				if _, err := fmt.Fprintf(osStdout, "  Recorded artifacts (metadata only; POSIX shells): %s\n", command); err != nil {
					return printErr("Output failed", err)
				}
			}
		}
	}
	if out.NextOffset > *offset {
		commandArgs := []string{"jobs", "attempts", args[0], runID, strconv.Itoa(taskIdx)}
		if limitSet {
			commandArgs = append(commandArgs, "--limit", strconv.Itoa(*limit))
		}
		if *failedOnly {
			commandArgs = append(commandArgs, "--failed-only")
		} else if *unsuccessfulOnly {
			commandArgs = append(commandArgs, "--unsuccessful-only")
		}
		commandArgs = append(commandArgs, "--offset", strconv.Itoa(out.NextOffset))
		_, _ = fmt.Fprintf(osStdout, "Next retained-attempt page (POSIX shells):\n  %s\n", savedNameSuggestionCommand(commandArgs))
	}
	if *unsuccessfulOnly && len(out.Attempts) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No failed, timed-out, out-of-memory, or cancelled attempts found in fetched history for run %s task %d starting at offset %d. Active or expired attempts may not appear.\n", runID, taskIdx, *offset)
	} else if *failedOnly && len(out.Attempts) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No failed, timed-out, or out-of-memory attempts found in fetched history for run %s task %d starting at offset %d. Active or expired attempts may not appear.\n", runID, taskIdx, *offset)
	} else if *all && len(out.Attempts) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No retained attempts found for run %s task %d starting at offset %d. Active or expired attempts may not appear.\n", runID, taskIdx, *offset)
	}
	return 0
}

// cmdJobsReplayFailed creates a linked run containing unsuccessful inputs.
func cmdJobsReplayFailed(args []string) int {
	if len(args) == 1 && args[0] == "--interactive" { //nolint:gosec // G602: len(args) == 1 guarantees index zero exists.
		return cmdJobsReplayFailedInteractive()
	}
	fs := newFlagSet("jobs replay-failed", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "read source tasks and preview replay without creating a run")
	results := fs.Bool("results", false, "wait for recovery success and collect outputs into ./results/JOB/NEW_RUN_ID")
	allArtifacts := fs.Bool("all-artifacts", false, "ignore saved artifact filters; requires --results")
	includes := registerJobsMultiFlag(fs, "include", "artifact-name glob; requires --results")
	excludes := registerJobsMultiFlag(fs, "exclude", "artifact-name glob to omit; requires --results")
	wait := fs.Bool("wait", false, "wait for the recovery run to succeed")
	waitTimeout := fs.Duration("wait-timeout", 10*time.Minute, "maximum wait after replay acceptance (requires --wait or --results)")
	interval := fs.Duration("poll-interval", 2*time.Second, "wait poll interval (requires --wait or --results; 1s to 1h)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	args = fs.Args()
	if len(args) > 2 {
		PrintUsage(os.Stderr, "usage: gregale jobs replay-failed [JOB] [RUN_ID|latest] [--dry-run] [--wait | --results] [--wait-timeout 10m] [--poll-interval 2s]", "jobs")
		return 1
	}
	if len(args) == 0 {
		defaults, path, err := readJobDefaultsForCommand([]string{"jobs", "replay-failed", "my-job", "latest", "--dry-run"})
		if err != nil {
			return printJobDefaultsSelectionError("Could not read Job defaults", err)
		}
		args = []string{defaults.Job}
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: job=%s\n", path, defaults.Job)
		}
	}
	if len(args) == 1 {
		args = append(args, "latest")
	}
	if (args[1] == "latest" && !jobSlugPattern.MatchString(args[0])) || (args[1] != "latest" && !jobRunIDPattern.MatchString(args[1])) {
		return printErr("Invalid replay target", fmt.Errorf("use jobs replay-failed [JOB] [RUN_ID|latest]; provide a valid Job name and run UUID"))
	}
	if *dryRun && !jobSlugPattern.MatchString(args[0]) {
		return printErr("Invalid replay target", errors.New("preview requires a valid Job name"))
	}
	if err := validateAllJobArtifacts(fs, *allArtifacts); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	if !*results && (*allArtifacts || len(*includes) > 0 || len(*excludes) > 0) {
		return printErr("Results collection required", fmt.Errorf("--include, --exclude, and --all-artifacts require --results"))
	}
	resultsParent := ""
	if *results {
		if !jobSlugPattern.MatchString(args[0]) {
			return printErr("Invalid Job name", fmt.Errorf("provide a valid Job name"))
		}
		if _, err := applyJobResultsDefaults(fs, args[0], includes, excludes, *allArtifacts); err != nil {
			return printErr("Could not apply Job defaults", err)
		}
		if err := validateJobArtifactIncludes([]string(*includes)); err != nil {
			return printErr("Invalid artifact filter", err)
		}
		if err := validateJobArtifactExcludes([]string(*excludes)); err != nil {
			return printErr("Invalid artifact filter", err)
		}
		var err error
		resultsParent, err = filepath.Abs(filepath.Join("results", args[0]))
		if err != nil {
			return printErr("Invalid results directory", err)
		}
		inspected, err := inspectArtifactOutputDirectory(resultsParent)
		if err != nil {
			return printErr("Invalid results directory", err)
		}
		inspected.Close()
	}
	waitRequested := *wait || *results
	waitOptions := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "wait-timeout" || f.Name == "poll-interval" {
			waitOptions = true
		}
	})
	if (waitOptions && !waitRequested) || (waitRequested && (*waitTimeout <= 0 || *interval < time.Second || *interval > time.Hour || !jobSlugPattern.MatchString(args[0]))) {
		return printErr("Invalid replay wait options", fmt.Errorf("--wait-timeout and --poll-interval require --wait or --results; provide a valid Job name, a positive wait timeout, and a poll interval between 1s and 1h"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if args[1] == "latest" {
		selected, err := resolveJobRecoveryRun(requestCtx, client, args[0], args[1], "Replay source", "replay")
		if err != nil {
			return printErr("Could not select replay source", err)
		}
		args[1] = selected
	}
	if *dryRun {
		return previewJobReplay(requestCtx, client, args[0], args[1], jobRecoveryPreviewOptions{
			Wait: waitRequested, CollectResults: *results, WaitTimeout: *waitTimeout, PollInterval: *interval,
			ResultsParent: resultsParent, Include: []string(*includes), Exclude: []string(*excludes),
		})
	}
	run, err := client.ReplayFailedJobRun(requestCtx, args[0], args[1])
	if err != nil {
		return printRunSubmissionError("Replay failed", err, recoveryCLI()+" jobs runs "+recoveryArgument(args[0]), false)
	}
	if run.ID != args[1] {
		rememberAcceptedJob(args[0], run)
	}
	if waitRequested {
		if run.ID == args[1] {
			if jsonOutput {
				if code := jsonOut(writeJSONSingle(run)); code != 0 {
					return code
				}
			}
			return printErr("Cannot wait for replay", fmt.Errorf("replay response identified the source run instead of a new recovery run; inspect %s jobs runs %s before repeating the replay request", recoveryCLI(), recoveryArgument(args[0])))
		}
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Replay accepted for source run %s; following recovery run %s.\n", args[1], run.ID)
		}
		if *results {
			if !jobRunIDPattern.MatchString(run.ID) {
				if jsonOutput {
					if code := jsonOut(writeJSONSingle(run)); code != 0 {
						return code
					}
				}
				return printErr("Cannot collect replay results", fmt.Errorf("replay response did not identify a valid recovery run; inspect %s jobs runs %s before repeating the replay request", recoveryCLI(), recoveryArgument(args[0])))
			}
			directory := filepath.Join(resultsParent, run.ID)
			if !jsonOutput {
				_, _ = fmt.Fprintf(osStderr, "Results destination: %s\n", directory)
			}
			return waitAndCollectJobRun(ctx, client, args[0], run, *waitTimeout, *interval, directory, true, []string(*includes), []string(*excludes))
		}
		return waitAcceptedJobRun(client, args[0], run, *waitTimeout, *interval)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(run))
	}
	PrintOK(osStdout, "Job %s run accepted: %s, replaying %s (%d tasks, status=%s)", oneLine(args[0]), run.ID, args[1], run.Tasks, oneLine(run.AggregateStatus))
	printJobRunNextSteps(args[0], run.ID)
	return 0
}

// cmdJobsArtifactURL verifies a managed result and returns its short-lived
// signed GET URL together with the expected size and SHA-256.
func cmdJobsArtifactURL(args []string) int {
	if len(args) == 1 && args[0] == "--interactive" { //nolint:gosec // G602: len(args) == 1 guarantees index zero exists.
		return cmdJobsArtifactURLInteractive()
	}
	if len(args) != 4 || (args[1] != "latest" && !jobRunIDPattern.MatchString(args[1])) || args[3] == "" {
		PrintUsage(os.Stderr, "usage: gregale jobs artifact-url <name> <run-id|latest> <task-index> <artifact-name>", "jobs")
		return 1
	}
	taskIdx, err := strconv.Atoi(args[2])
	if err != nil || taskIdx < 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs artifact-url <name> <run-id|latest> <task-index> <artifact-name>   (task-index >= 0)", "jobs")
		return 1
	}
	if args[1] == "latest" && !jobSlugPattern.MatchString(args[0]) {
		return printErr("Invalid Job name", fmt.Errorf("latest requires a valid Job name"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if args[1] == "latest" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	runID, err := resolveJobRunReadArg(ctx, client, args[0], args[1])
	if err != nil {
		return printErr("Could not resolve job run", err)
	}
	out, err := client.DownloadJobArtifact(ctx, args[0], runID, taskIdx, args[3])
	if err != nil {
		return printErr("Download request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(out))
	}
	if _, err := fmt.Fprintf(osStdout, "%s\n%s  %d bytes\n", out.Download.URL, out.SHA256, out.SizeBytes); err != nil {
		return printErr("Output failed", err)
	}
	return 0
}

// cmdJobsRetry implements `gregale jobs retry <name> <run-id> <task-index>`.
// The server enforces the task state and retry budget; this command only
// validates the stable positional shape before making the request.
func cmdJobsRetry(args []string) int {
	if len(args) == 1 && args[0] == "--interactive" { //nolint:gosec // G602: len(args) == 1 guarantees index zero exists.
		return cmdJobsRetryInteractive()
	}
	remaining, taskFlag, taskSet, err := splitJobLogTask(args)
	if err != nil {
		return printErr("Invalid task selection", err)
	}
	args = remaining
	fs := newFlagSet("jobs retry", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "read task state and preview retry without starting another attempt")
	results := fs.Bool("results", false, "wait for success and collect outputs into ./results/JOB/RUN_ID")
	allArtifacts := fs.Bool("all-artifacts", false, "ignore saved artifact filters; requires --results")
	includes := registerJobsMultiFlag(fs, "include", "artifact-name glob; requires --results")
	excludes := registerJobsMultiFlag(fs, "exclude", "artifact-name glob to omit; requires --results")
	wait := fs.Bool("wait", false, "wait for the whole retried run to succeed")
	waitTimeout := fs.Duration("wait-timeout", 10*time.Minute, "maximum wait after retry acceptance (requires --wait or --results)")
	interval := fs.Duration("poll-interval", 2*time.Second, "wait poll interval (requires --wait or --results; 1s to 1h)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	args = fs.Args()
	if (taskSet && len(args) > 2) || (!taskSet && len(args) != 3) {
		PrintUsage(os.Stderr, "usage: gregale jobs retry [JOB] [RUN_ID|latest] --task N [--wait | --results], or jobs retry JOB RUN_ID TASK_INDEX [--wait | --results]", "jobs")
		return 1
	}
	if taskSet {
		if len(args) == 0 {
			defaults, path, err := readJobDefaultsForCommand([]string{"jobs", "retry", "my-job", "latest", "--task", taskFlag, "--dry-run"})
			if err != nil {
				return printJobDefaultsSelectionError("Could not read Job defaults", err)
			}
			args = []string{defaults.Job}
			if !jsonOutput {
				_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: job=%s\n", path, defaults.Job)
			}
		}
		if len(args) == 1 {
			args = append(args, "latest")
		}
		args = append(args, taskFlag)
		if !jobSlugPattern.MatchString(args[0]) {
			return printErr("Invalid Job name", fmt.Errorf("provide a valid Job name"))
		}
	}
	if (!taskSet || args[1] != "latest") && !jobRunIDPattern.MatchString(args[1]) {
		PrintUsage(os.Stderr, "usage: gregale jobs retry <name> <run-id> <task-index>   (run-id is uuid v4)", "jobs")
		return 1
	}
	taskIdx, err := strconv.Atoi(args[2])
	if err != nil || taskIdx < 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs retry <name> <run-id> <task-index>   (task-index >= 0)", "jobs")
		return 1
	}
	if *dryRun && !jobSlugPattern.MatchString(args[0]) {
		return printErr("Invalid retry target", errors.New("preview requires a valid Job name"))
	}
	if err := validateAllJobArtifacts(fs, *allArtifacts); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	if !*results && (*allArtifacts || len(*includes) > 0 || len(*excludes) > 0) {
		return printErr("Results collection required", fmt.Errorf("--include, --exclude, and --all-artifacts require --results"))
	}
	resultsParent := ""
	if *results {
		if !jobSlugPattern.MatchString(args[0]) {
			return printErr("Invalid Job name", fmt.Errorf("provide a valid Job name"))
		}
		if _, err := applyJobResultsDefaults(fs, args[0], includes, excludes, *allArtifacts); err != nil {
			return printErr("Could not apply Job defaults", err)
		}
		if err := validateJobArtifactIncludes([]string(*includes)); err != nil {
			return printErr("Invalid artifact filter", err)
		}
		if err := validateJobArtifactExcludes([]string(*excludes)); err != nil {
			return printErr("Invalid artifact filter", err)
		}
		resultsParent, err = filepath.Abs(filepath.Join("results", args[0]))
		if err != nil {
			return printErr("Invalid results directory", err)
		}
		inspected, err := inspectArtifactOutputDirectory(resultsParent)
		if err != nil {
			return printErr("Invalid results directory", err)
		}
		inspected.Close()
	}
	waitRequested := *wait || *results
	waitOptions := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "wait-timeout" || f.Name == "poll-interval" {
			waitOptions = true
		}
	})
	if (waitOptions && !waitRequested) || (waitRequested && (*waitTimeout <= 0 || *interval < time.Second || *interval > time.Hour || !jobSlugPattern.MatchString(args[0]))) {
		return printErr("Invalid retry wait options", fmt.Errorf("--wait-timeout and --poll-interval require --wait or --results; provide a valid Job name, a positive wait timeout, and a poll interval between 1s and 1h"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if taskSet {
		selected, err := resolveJobRetryRun(requestCtx, client, args[0], args[1])
		if err != nil {
			return printErr("Could not select retry run", err)
		}
		args[1] = selected
	}
	directory := ""
	if *results {
		directory = filepath.Join(resultsParent, args[1])
		inspected, err := inspectArtifactOutputDirectory(directory)
		if err != nil {
			return printErr("Invalid results directory", err)
		}
		inspected.Close()
	}
	if *dryRun {
		return previewJobRetry(requestCtx, client, args[0], args[1], taskIdx, jobRecoveryPreviewOptions{
			Wait: waitRequested, CollectResults: *results, WaitTimeout: *waitTimeout, PollInterval: *interval,
			ResultsParent: resultsParent, Include: []string(*includes), Exclude: []string(*excludes),
		})
	}
	resp, err := client.RetryJobTask(requestCtx, args[0], args[1], taskIdx)
	if err != nil {
		return printErr("Retry failed", err)
	}
	if resp.Run.ID == args[1] && resp.Task.RunID == args[1] && resp.Task.TaskIndex == taskIdx {
		rememberAcceptedJob(args[0], resp.Run)
	}
	if waitRequested {
		if resp.Run.ID != args[1] || resp.Task.RunID != args[1] || resp.Task.TaskIndex != taskIdx {
			if jsonOutput {
				if code := jsonOut(writeJSONSingle(resp)); code != 0 {
					return code
				}
			}
			return printErr("Cannot wait for retried Job", fmt.Errorf("retry response did not identify the requested run and task; inspect %s jobs tasks %s %s before repeating the retry request", recoveryCLI(), recoveryArgument(args[0]), recoveryArgument(args[1])))
		}
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Retry accepted for task %d in run %s. Waiting assesses the whole run, including other tasks.\n", taskIdx, args[1])
		}
		if *results {
			if !jsonOutput {
				_, _ = fmt.Fprintf(osStderr, "Results destination: %s\n", directory)
			}
			return waitAndCollectJobRun(ctx, client, args[0], resp.Run, *waitTimeout, *interval, directory, true, []string(*includes), []string(*excludes))
		}
		return waitAcceptedJobRun(client, args[0], resp.Run, *waitTimeout, *interval)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(resp))
	}
	PrintOK(osStdout, "Retried task %s/%d (attempt=%d, next_attempt_at=%s)", args[1], taskIdx, resp.Task.Attempt, resp.NextAttemptAt)
	return 0
}

// cmdJobsLogs implements `gregale jobs logs <name> <run-id>
// <task-index> [--max-bytes N] [--follow]`. Without --follow it prints the
// task's stored output tail: Truncated=true means the tail was capped at
// MaxBytes (re-fetch with --max-bytes N for more), and empty LogContent with
// Truncated=false means the task produced no output. --follow streams the
// task's output live until it finishes (ADR-1163).
func cmdJobsLogs(args []string) int {
	for _, arg := range args {
		if arg == "--interactive" || arg == "-interactive" || strings.HasPrefix(arg, "--interactive=") || strings.HasPrefix(arg, "-interactive=") {
			return cmdJobsLogsInteractiveArgs(args)
		}
	}
	failedArgs, failedOnly, unsuccessfulOnly, filtersProvided, failedErr := splitJobLogsStateFlags(args)
	if failedErr != nil {
		return printErr("Invalid failed logs arguments", failedErr)
	}
	if failedOnly || unsuccessfulOnly {
		return cmdJobsFilteredLogs(failedArgs, unsuccessfulOnly)
	}
	args, follow := splitJobLogFollow(failedArgs)
	taskArgs, taskFlag, taskSet, err := splitJobLogTask(args)
	if err != nil {
		return printErr("Invalid task selection", err)
	}
	logArgs, attempt, err := splitJobLogAttempt(taskArgs)
	if err != nil {
		return printErr("Invalid retained attempt", err)
	}
	positionals, maxBytes, maxBytesSet, ok := parseJobsLogsArgs(logArgs)
	if follow && ok && !taskSet && len(positionals) != 3 {
		return printErr("Invalid follow arguments", fmt.Errorf("--follow streams one task; select it with --task N or JOB RUN_ID TASK_INDEX"))
	}
	if ok && !taskSet && attempt == 0 && !filtersProvided && len(positionals) <= 2 {
		if !jsonOutput {
			_, _ = fmt.Fprintln(osStderr, "No task selected; showing current logs for failed, timed-out, out-of-memory, and cancelled tasks. Use --task N to inspect one task.")
		}
		return cmdJobsFilteredLogs(logArgs, true)
	}
	if !ok || (!taskSet && len(positionals) != 3) || (taskSet && len(positionals) > 2) {
		PrintUsage(os.Stderr, "usage: gregale jobs logs [JOB] [RUN_ID|latest] --task N [--max-bytes N] [--attempt N], or jobs logs JOB RUN_ID|latest TASK_INDEX", "jobs")
		return 1
	}
	if taskSet {
		if len(positionals) == 0 {
			defaults, path, err := readJobDefaultsForCommand([]string{"jobs", "logs", "my-job", "latest", "--task", taskFlag})
			if err != nil {
				return printJobDefaultsSelectionError("Could not read Job defaults", err)
			}
			positionals = []string{defaults.Job}
			if !jsonOutput {
				_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: job=%s\n", path, defaults.Job)
			}
		}
		if len(positionals) == 1 {
			positionals = append(positionals, "latest")
		}
		if !jobSlugPattern.MatchString(positionals[0]) {
			return printErr("Invalid Job name", fmt.Errorf("provide a valid Job name"))
		}
		positionals = append(positionals, taskFlag)
	}
	if positionals[1] != "latest" && !jobRunIDPattern.MatchString(positionals[1]) {
		PrintUsage(os.Stderr, "usage: gregale jobs logs <name> <run-id|latest> <task-index>   (run-id is uuid v4)", "jobs")
		return 1
	}
	taskIdx, err := strconv.Atoi(positionals[2])
	if err != nil || taskIdx < 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs logs <name> <run-id> <task-index>   (task-index >= 0)", "jobs")
		return 1
	}
	if attempt > 0 && !jobSlugPattern.MatchString(positionals[0]) {
		return printErr("Invalid Job name", fmt.Errorf("--attempt requires a valid Job name"))
	}
	if follow && (attempt > 0 || maxBytesSet || jsonOutput) {
		return printErr("Invalid follow arguments", fmt.Errorf("--follow streams the current attempt; it cannot be combined with --attempt, --max-bytes or --json"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if !maxBytesSet {
		maxBytes = 0 // preserve the server's default when the flag is omitted
	}
	ctx := context.Background()
	if positionals[1] == "latest" || attempt > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	runID, err := resolveJobRunReadArg(ctx, client, positionals[0], positionals[1])
	if err != nil {
		return printErr("Could not resolve job run", err)
	}
	if attempt > 0 {
		return readRetainedJobAttemptLogs(ctx, client, positionals[0], runID, taskIdx, attempt, maxBytes)
	}
	if follow {
		return followJobTaskLogsCommand(client, positionals[0], runID, taskIdx)
	}
	logs, err := client.GetJobTaskLogsWithMaxBytes(ctx, positionals[0], runID, taskIdx, maxBytes)
	if err != nil {
		return printErr("Logs request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(logs))
	}
	return renderJobTaskLogs(logs)
}

func renderJobTaskLogs(logs api.JobTaskLogResponse) int {
	if logs.LogContent == "" && !logs.Truncated {
		_, _ = fmt.Fprintf(osStdout, "(task %s produced no output)\n", logs.TaskStatus)
		return 0
	}
	_, _ = fmt.Fprint(osStdout, logs.LogContent)
	if logs.Truncated {
		_, _ = fmt.Fprintln(osStdout, "...[truncated]")
	}
	return 0
}

// parseJobsLogsArgs accepts flags before or after the three positional
// arguments. The standard flag package stops parsing at the first positional,
// while the documented CLI form places --max-bytes after task-index, so this
// small parser keeps both forms valid and rejects unknown flags locally.
func parseJobsLogsArgs(args []string) (positionals []string, maxBytes int, maxBytesSet, ok bool) {
	maxBytes = api.DefaultJobTaskLogMaxBytes
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--max-bytes" || arg == "-max-bytes" {
			if maxBytesSet || i+1 >= len(args) {
				return nil, 0, false, false
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 || n > api.MaxJobTaskLogMaxBytes {
				return nil, 0, false, false
			}
			maxBytes, maxBytesSet = n, true
			continue
		}
		if strings.HasPrefix(arg, "--max-bytes=") || strings.HasPrefix(arg, "-max-bytes=") {
			if maxBytesSet {
				return nil, 0, false, false
			}
			raw := arg[strings.IndexByte(arg, '=')+1:]
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > api.MaxJobTaskLogMaxBytes {
				return nil, 0, false, false
			}
			maxBytes, maxBytesSet = n, true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return nil, 0, false, false
		}
		positionals = append(positionals, arg)
	}
	return positionals, maxBytes, maxBytesSet, true
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
	if j.Schedule != "" {
		_, _ = fmt.Fprintf(w, "  %-10s %s (%s)\n", "schedule:", j.Schedule, j.Timezone)
		if j.LastScheduledAt != "" {
			_, _ = fmt.Fprintf(w, "  %-10s %s\n", "last run:", j.LastScheduledAt)
		}
	}
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "image:", j.ImageRef)
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "command:", formatCommand(j.Command))
	_, _ = fmt.Fprintf(w, "  %-10s %d MB\n", "ram:", j.RAMMB)
	_, _ = fmt.Fprintf(w, "  %-10s %d s\n", "timeout:", j.TaskTimeoutSec)
	_, _ = fmt.Fprintf(w, "  %-10s %d\n", "parallel:", j.MaxParallelism)
	_, _ = fmt.Fprintf(w, "  %-10s %d\n", "retries:", j.RetryMax)
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "status:", j.Status)
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "image status:", j.ImageMaterializationStatus)
	if j.ImageMaterializationError != "" {
		_, _ = fmt.Fprintf(w, "  %-10s %s\n", "image error:", j.ImageMaterializationError)
	}
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

func renderScheduleOccurrences(w io.Writer, rows []api.ScheduleOccurrenceResponse) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(w, "(no scheduled occurrences)")
		return
	}
	_, _ = fmt.Fprintf(w, "%-36s %-20s %-20s %-10s %s\n", "occurrence-id", "scheduled-for", "started-at", "status", "reason")
	for _, row := range rows {
		started := "-"
		if row.StartedAt != nil {
			started = row.StartedAt.UTC().Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(w, "%-36s %-20s %-20s %-10s %s\n", row.ID,
			row.ScheduledFor.UTC().Format(time.RFC3339), started, row.Status, row.Reason)
	}
}

func parseSchedulePolicyJSON(raw string) (*workpolicy.SchedulePolicy, error) {
	var policy workpolicy.SchedulePolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return nil, err
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &policy, nil
}

func parseFailureRulesJSON(raw string) (*workpolicy.FailureRules, error) {
	var rules workpolicy.FailureRules
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, err
	}
	if err := rules.Validate(); err != nil {
		return nil, err
	}
	return &rules, nil
}

// renderJobTasksTable writes a tabular row per task. task_index
// runs 0..N-1 (zero-based; matches the server's CTE fan-out).
func renderJobTasksTable(w io.Writer, tasks []api.JobTaskResponse) {
	if len(tasks) == 0 {
		_, _ = fmt.Fprintln(w, "(no tasks)")
		return
	}
	_, _ = fmt.Fprintf(w, "%4s  %-10s %3s %5s  %-14s  %-20s  %-22s  %s\n",
		"idx", "status", "try", "exit", "error_class", "outcome_code", "decision", "instance")
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
		outcomeCode := t.OutcomeCode
		if outcomeCode == "" {
			outcomeCode = "-"
		}
		_, _ = fmt.Fprintf(w, "%4d  %-10s %3d %5s  %-14s  %-20s  %-22s  %s\n",
			t.TaskIndex, t.Status, t.Attempt, exit, errClass, outcomeCode, workDecisionLabel(t.WorkDecision), inst)
	}
}

func workDecisionLabel(decision *workpolicy.Decision) string {
	if decision == nil {
		return "-"
	}
	return decision.Classification + "/" + decision.Action
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
	_, err = fmt.Fprintln(osStdout, string(b))
	return err
}
