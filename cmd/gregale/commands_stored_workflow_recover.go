package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type storedWorkflowRecoverySelection struct {
	FailureEvidenceID   string `json:"failure_evidence_id,omitempty"`
	ParentPosition      int64  `json:"parent_position,omitempty"`
	ID                  string `json:"id"`
	ExpectedResumeCount int    `json:"expected_resume_count"`
}
type storedWorkflowRecoveryOutcome struct {
	ID                  string   `json:"id"`
	ExpectedResumeCount int      `json:"expected_resume_count"`
	Outcome             string   `json:"outcome"`
	Reason              string   `json:"reason"`
	Status              string   `json:"status,omitempty"`
	ResumeCount         *int     `json:"resume_count,omitempty"`
	Blockers            []string `json:"blockers,omitempty"`
	HTTPStatus          int      `json:"http_status,omitempty"`
	ErrorCode           string   `json:"error_code,omitempty"`
}
type storedWorkflowRecoveryReport struct {
	App         string                          `json:"app"`
	Name        string                          `json:"name"`
	StartedAt   time.Time                       `json:"started_at"`
	CompletedAt time.Time                       `json:"completed_at"`
	Outcomes    []storedWorkflowRecoveryOutcome `json:"outcomes"`
}

func parseStoredWorkflowRecoverySelection(value string) (storedWorkflowRecoverySelection, error) {
	id, count, ok := strings.Cut(value, ":")
	expected, err := strconv.Atoi(count)
	if !ok || !workflowUUIDPattern.MatchString(strings.ToLower(id)) || err != nil || expected < 0 || expected > api.WorkflowRunMaxResumes {
		return storedWorkflowRecoverySelection{}, fmt.Errorf("--run must be UUID:COUNT with count between 0 and %d", api.WorkflowRunMaxResumes)
	}
	return storedWorkflowRecoverySelection{ID: strings.ToLower(id), ExpectedResumeCount: expected}, nil
}

func cmdStoredWorkflowsRecover(args []string) int {
	fs := newFlagSet("workflow stored recover", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "stored workflow name")
	planOut := fs.String("plan-out", "", "save a reviewed plan to a new file without resuming runs")
	key := fs.String("idempotency-key", "", "stable batch key; reuse for interrupted retries")
	var selection []storedWorkflowRecoverySelection
	seen := map[string]bool{}
	fs.Func("run", "run UUID:expected-resume-count (repeatable)", func(value string) error {
		run, err := parseStoredWorkflowRecoverySelection(value)
		if err != nil {
			return err
		}
		if seen[run.ID] {
			return errors.New("duplicate --run ID")
		}
		if len(selection) >= api.StoredWorkflowRecoveryBatchMaxRuns {
			return fmt.Errorf("select at most %d runs", api.StoredWorkflowRecoveryBatchMaxRuns)
		}
		seen[run.ID] = true
		selection = append(selection, run)
		return nil
	})
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	batchKey := strings.TrimSpace(*key)
	if !validStoredWorkflowRunSelection(*app, *name) || len(selection) == 0 || batchKey == "" {
		return printErr("Invalid recovery selection", errors.New("provide --app, --name, at least one --run UUID:COUNT and --idempotency-key"))
	}
	if err := validateDeployIdempotencyKey(batchKey); err != nil {
		return printErr("Invalid --idempotency-key", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if *planOut != "" {
		if err := saveStoredWorkflowRecoveryPlan(ctx, client, *app, *name, batchKey, selection, *planOut); err != nil {
			return printErr("Could not save recovery plan", err)
		}
		return 0
	}
	report, err := recoverStoredWorkflowRuns(ctx, client, *app, *name, batchKey, selection)
	if err != nil {
		return printErr("Could not prepare stored workflow recovery", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else if err := renderStoredWorkflowRecoveryReport(osStdout, report); err != nil {
		return printErr("Could not write recovery report", err)
	}
	for _, outcome := range report.Outcomes {
		if outcome.Outcome != "resumed" {
			return 1
		}
	}
	return 0
}

func recoveryRunInScope(run api.WorkflowRunResponse, appID, name, id string) bool {
	return strings.EqualFold(run.ID, id) && run.AppID == appID && run.WorkflowName == name
}

// Validate the entire explicit selection before the first mutation. Each run
// is then rechecked immediately before its own independently committed resume.
func recoverStoredWorkflowRuns(ctx context.Context, c *api.Client, app, name, key string, selection []storedWorkflowRecoverySelection) (storedWorkflowRecoveryReport, error) {
	report := storedWorkflowRecoveryReport{App: app, Name: name, StartedAt: time.Now().UTC(), Outcomes: []storedWorkflowRecoveryOutcome{}}
	appID, err := loadStoredWorkflowRunScope(ctx, c, app, name)
	if err != nil {
		return report, fmt.Errorf("resolve recovery scope: %w", err)
	}
	for _, item := range selection {
		run, err := c.GetWorkflowRun(ctx, item.ID)
		if err != nil {
			return report, fmt.Errorf("inspect selected run %s: %w", item.ID, err)
		}
		if !recoveryRunInScope(run, appID, name, item.ID) {
			return report, errors.New("selected run is outside the app and stored workflow")
		}
	}
	for _, item := range selection {
		report.Outcomes = append(report.Outcomes, recoverStoredWorkflowRun(ctx, c, appID, name, key, item))
	}
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func storedWorkflowRecoveryRunKey(key, appID, name string, item storedWorkflowRecoverySelection) string {
	// Quoted components preserve boundaries. The selected count is part of the
	// logical request, so a later intentional continuation needs a different key.
	digest := sha256.Sum256([]byte(fmt.Sprintf("%q:%q:%q:%q:%d", key, appID, name, item.ID, item.ExpectedResumeCount)))
	return fmt.Sprintf("stored-workflow-recover-%x", digest)
}

func recoverStoredWorkflowRun(ctx context.Context, c *api.Client, appID, name, key string, item storedWorkflowRecoverySelection) storedWorkflowRecoveryOutcome {
	out := storedWorkflowRecoveryOutcome{ID: item.ID, ExpectedResumeCount: item.ExpectedResumeCount, Outcome: "failed"}
	run, err := c.GetWorkflowRun(ctx, item.ID)
	if err != nil {
		return storedWorkflowRecoveryReadFailure(out, "run_read_failed", err)
	}
	if !recoveryRunInScope(run, appID, name, item.ID) {
		out.Reason = "run_scope_mismatch"
		return out
	}
	count := run.ResumeCount
	out.ResumeCount = &count
	out.Status = run.Status
	if count != item.ExpectedResumeCount || run.Status != "failed" && run.Status != "dead" {
		out.Outcome = "changed"
		out.Reason = "run_state_changed"
		return out
	}
	diagnostics, err := c.GetWorkflowRunDiagnostics(ctx, item.ID)
	if err != nil {
		return storedWorkflowRecoveryReadFailure(out, "diagnostics_read_failed", err)
	}
	if !strings.EqualFold(diagnostics.RunID, item.ID) || diagnostics.WorkflowName != name {
		out.Reason = "diagnostics_scope_mismatch"
		return out
	}
	count = diagnostics.Resume.ExpectedResumeCount
	out.Status = diagnostics.Status
	if count != item.ExpectedResumeCount || diagnostics.Status != "failed" && diagnostics.Status != "dead" {
		out.Outcome = "changed"
		out.Reason = "run_state_changed"
		return out
	}
	if !diagnostics.Resume.Eligible {
		out.Outcome = "blocked"
		out.Reason = "resume_ineligible"
		for _, blocker := range diagnostics.Resume.Blockers {
			out.Blockers = append(out.Blockers, blocker.Code)
		}
		return out
	}
	expected := item.ExpectedResumeCount
	resumed, err := c.ResumeWorkflowRunWithIdempotencyKey(ctx, item.ID, api.ResumeWorkflowRunRequest{ExpectedResumeCount: &expected}, storedWorkflowRecoveryRunKey(key, appID, name, item))
	if err != nil {
		return storedWorkflowRecoveryResumeFailure(out, err)
	}
	if !recoveryRunInScope(resumed, appID, name, item.ID) || resumed.ResumeCount != expected+1 {
		out.Reason = "resume_response_unconfirmed"
		return out
	}
	count = resumed.ResumeCount
	out.Status = resumed.Status
	out.Outcome = "resumed"
	out.Reason = "resume_accepted"
	return out
}

func storedWorkflowRecoveryReadFailure(out storedWorkflowRecoveryOutcome, reason string, err error) storedWorkflowRecoveryOutcome {
	out.Reason = reason
	var remote *api.APIError
	if errors.As(err, &remote) {
		out.HTTPStatus = remote.Problem.Status
		out.ErrorCode = remote.Problem.Code
	}
	return out
}
func storedWorkflowRecoveryResumeFailure(out storedWorkflowRecoveryOutcome, err error) storedWorkflowRecoveryOutcome {
	out = storedWorkflowRecoveryReadFailure(out, "resume_unconfirmed", err)
	switch out.ErrorCode {
	case api.CodeWorkflowResumeConflict:
		out.Outcome = "changed"
		out.Reason = "resume_conflict"
	case api.CodeWorkflowResumeUnsafe, api.CodeWorkflowResumeLimit, api.CodePlanWorkflowsQuota, api.CodePlanWorkflowsNotAllowed, api.CodeWorkflowDeploymentUnavailable:
		out.Outcome = "blocked"
		out.Reason = out.ErrorCode
	default:
		if out.HTTPStatus >= 400 && out.HTTPStatus < 500 {
			out.Reason = "resume_rejected"
		}
	}
	return out
}

func renderStoredWorkflowRecoveryReport(w io.Writer, report storedWorkflowRecoveryReport) error {
	if _, err := fmt.Fprintf(w, "Run recovery: %s / %s\nEach resume commits independently; pauses, events and schedules are unchanged.\n", report.App, report.Name); err != nil {
		return err
	}
	for _, out := range report.Outcomes {
		if _, err := fmt.Fprintf(w, "  %s: %s (%s), status=%s, expected-resume-count=%d, blockers=%s\n", out.ID, out.Outcome, out.Reason, out.Status, out.ExpectedResumeCount, strings.Join(out.Blockers, ",")); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "For interrupted or unconfirmed requests, retry the same run IDs, original counts and batch key. A changed result does not prove this batch caused the change; inspect run resume history.")
	return err
}
