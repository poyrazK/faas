// Authenticated operator job-run incident controls.
//
// Routine diagnosis and cancellation go through apid; gregalectl never opens
// PostgreSQL and never needs a customer's credential.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

const dispatchJobs = "jobs"

var (
	operatorJobReasonShape  = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	operatorJobTraceIDShape = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type operatorJobCancelOutput struct {
	api.OperatorJobRunCancelResponse
	TraceID string `json:"trace_id"`
}

func cmdJobsDispatch(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl jobs: missing subcommand; want active|inspect|cancel")
		return 2
	}
	switch args[0] {
	case "active":
		return cmdJobsActive(args[1:])
	case "inspect":
		return cmdJobsInspect(args[1:])
	case "cancel":
		return cmdJobsCancel(args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl jobs: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdJobsActive(args []string) int {
	fs := flag.NewFlagSet("active", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	accountID := fs.String("account-id", "", "account id (uuid)")
	limit := fs.Int("limit", 50, "maximum active runs to return (1..500)")
	offset := fs.Int("offset", 0, "active-run pagination offset")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl jobs active: positional arguments are not accepted")
		return 2
	}
	id := strings.TrimSpace(*accountID)
	if id != "" {
		if _, err := uuid.Parse(id); err != nil {
			_, _ = fmt.Fprintln(osStderr, "gregalectl jobs active: --account-id must be a UUID when supplied")
			return 2
		}
	}
	if *limit < 1 || *limit > 500 || *offset < 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl jobs active: --limit must be 1..500 and --offset must be non-negative")
		return 2
	}
	query := url.Values{
		"limit":  {strconv.Itoa(*limit)},
		"offset": {strconv.Itoa(*offset)},
	}
	if id != "" {
		query.Set("account_id", id)
	}
	var response api.OperatorJobRunListResponse
	if code := operatorJobRequest("active", http.MethodGet, "/v1/admin/ops/jobs/runs?"+query.Encode(), nil, &response, false, nil); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	_, _ = fmt.Fprintf(osStdout, "account_id=%s active_runs=%d limit=%d offset=%d next_offset=%d\n",
		response.AccountID, len(response.Runs), response.Limit, response.Offset, response.NextOffset)
	for _, run := range response.Runs {
		printOperatorJobRun(run)
	}
	return 0
}

func cmdJobsInspect(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	runID := fs.String("run-id", "", "job run id (uuid)")
	taskLimit := fs.Int("task-limit", 50, "maximum tasks to return (1..500)")
	taskOffset := fs.Int("task-offset", 0, "task pagination offset")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, ok := validateOperatorJobID(fs, "inspect", "run-id", *runID)
	if !ok {
		return 2
	}
	if *taskLimit < 1 || *taskLimit > 500 || *taskOffset < 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl jobs inspect: --task-limit must be 1..500 and --task-offset must be non-negative")
		return 2
	}
	query := url.Values{
		"task_limit":  {strconv.Itoa(*taskLimit)},
		"task_offset": {strconv.Itoa(*taskOffset)},
	}
	path := "/v1/admin/ops/jobs/runs/" + url.PathEscape(id) + "?" + query.Encode()
	var response api.OperatorJobRunDetailResponse
	if code := operatorJobRequest("inspect", http.MethodGet, path, nil, &response, false, nil); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	printOperatorJobRun(response.Run)
	_, _ = fmt.Fprintf(osStdout, "tasks_shown=%d task_limit=%d task_offset=%d next_task_offset=%d\n",
		len(response.Tasks), response.TaskLimit, response.TaskOffset, response.NextTaskOffset)
	for _, task := range response.Tasks {
		_, _ = fmt.Fprintf(osStdout, "task index=%d status=%s attempt=%d instance_id=%s error_class=%s exit_code=%d created_at=%s\n",
			task.TaskIndex, task.Status, task.Attempt, task.InstanceID, task.ErrorClass, task.ExitCode, task.CreatedAt)
	}
	return 0
}

func cmdJobsCancel(args []string) int {
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	runID := fs.String("run-id", "", "job run id (uuid)")
	reason := fs.String("reason", "", "durable audit reason slug ([a-z0-9_]{1,64})")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	yes := fs.Bool("yes", false, "acknowledge cancelling every non-terminal task in the run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, ok := validateOperatorJobID(fs, "cancel", "run-id", *runID)
	if !ok {
		return 2
	}
	cleanReason := strings.TrimSpace(*reason)
	if !operatorJobReasonShape.MatchString(cleanReason) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl jobs cancel: --reason is required and must match [a-z0-9_]{1,64}")
		return 2
	}
	if !*yes {
		_, _ = fmt.Fprintln(osStderr, "gregalectl jobs cancel: --yes required")
		return 2
	}
	traceID := strings.TrimSpace(*traceIDFlag)
	if traceID == "" {
		traceID = wire.NewTraceID()
	}
	if !operatorJobTraceIDShape.MatchString(traceID) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl jobs cancel: --trace-id must be 32 lowercase hex characters")
		return 2
	}
	path := "/v1/admin/ops/jobs/runs/" + url.PathEscape(id) + "/cancel?confirm=true&reason=" + url.QueryEscape(cleanReason)
	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, traceID)
	var response api.OperatorJobRunCancelResponse
	if code := operatorJobRequest("cancel", http.MethodPost, path, nil, &response, true, headers); code != 0 {
		return code
	}
	output := operatorJobCancelOutput{OperatorJobRunCancelResponse: response, TraceID: traceID}
	if jsonEnabled() {
		return emitOperatorJSON(output)
	}
	_, _ = fmt.Fprintf(osStdout, "cancelled run_id=%s job=%s account_id=%s status=%s cancelled_at=%s reason=%s trace_id=%s\n",
		response.Run.RunID, response.Run.JobName, response.Run.AccountID, response.Run.AggregateStatus,
		response.CancelledAt, response.Reason, traceID)
	return 0
}

func validateOperatorJobID(fs *flag.FlagSet, action, flagName, raw string) (string, bool) {
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl jobs %s: positional arguments are not accepted\n", action)
		return "", false
	}
	id := strings.TrimSpace(raw)
	if _, err := uuid.Parse(id); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl jobs %s: --%s is required and must be a UUID\n", action, flagName)
		return "", false
	}
	return id, true
}

func operatorJobRequest(action, method, path string, input, output any, idempotent bool, headers http.Header) int {
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl jobs %s: %v\n", action, err)
		return 1
	}
	if err := newOperatorHTTPClient(&sess).doJSONWithHeaders(context.Background(), method, path, input, output, idempotent, nil, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl jobs %s: %v\n", action, err)
		return 1
	}
	return 0
}

func printOperatorJobRun(run api.OperatorJobRun) {
	_, _ = fmt.Fprintf(osStdout, "run_id=%s job=%s job_id=%s account_id=%s status=%s tasks=%d running=%d succeeded=%d failed=%d cancelled=%d created_at=%s",
		run.RunID, run.JobName, run.JobID, run.AccountID, run.AggregateStatus, run.Tasks,
		run.TasksRunning, run.TasksSucceeded, run.TasksFailed, run.TasksCancelled, run.CreatedAt)
	_, _ = fmt.Fprintf(osStdout, " ram_mb=%d", run.RAMMB)
	if run.StartedAt != "" {
		_, _ = fmt.Fprintf(osStdout, " started_at=%s", run.StartedAt)
	}
	_, _ = fmt.Fprintln(osStdout)
}
