package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdWorkflows_NoArgs(t *testing.T) {
	code, captured := runWithStderr(t, func() int { return cmdWorkflows([]string{}) })
	if code != 1 {
		t.Errorf("cmdWorkflows(no args) = %d, want 1", code)
	}
	if !strings.Contains(captured, "gregale workflows") {
		t.Errorf("usage must mention 'gregale workflows'; got: %s", captured)
	}
}

func TestCmdWorkflows_UnknownSubcommand(t *testing.T) {
	code, captured := runWithStderr(t, func() int { return cmdWorkflows([]string{"unknown-sub"}) })
	if code != 1 {
		t.Errorf("cmdWorkflows(unknown) = %d, want 1", code)
	}
	if !strings.Contains(captured, "unknown workflows subcommand") {
		t.Errorf("usage must mention 'unknown workflows subcommand'; got: %s", captured)
	}
}

func TestCmdWorkflowsList_MissingApp(t *testing.T) {
	code, captured := runWithStderr(t, func() int { return cmdWorkflowsList([]string{}) })
	if code != 1 {
		t.Errorf("cmdWorkflowsList(no --app) = %d, want 1", code)
	}
	if !strings.Contains(captured, "--app <slug>") {
		t.Errorf("usage must mention '--app <slug>'; got: %s", captured)
	}
}

func TestCmdWorkflowsList_InvalidStatus(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdWorkflowsList([]string{"--app", "my-app", "--status", "nonsense"})
	})
	if code != 1 {
		t.Fatalf("cmdWorkflowsList(invalid status) = %d, want 1", code)
	}
	if !strings.Contains(captured, "invalid workflow status") {
		t.Errorf("validation error = %q", captured)
	}
}

func TestCmdWorkflowsList_FiltersByWorkflowAndCreationWindow(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	f := authedFakeAPI(t, `{"runs":[],"total":0}`, http.StatusOK)
	args := []string{
		"--app", "billing", "--limit", "20", "--offset", "3", "--status", "failed",
		"--workflow-name", "paid + invoice", "--created-after", "2026-10-01T02:00:00+02:00",
		"--created-before", "2026-10-05T23:59:59Z",
	}
	var output bytes.Buffer
	if code := captureStdoutSwap(t, &output, func() int { return cmdWorkflowsList(args) }); code != 0 {
		t.Fatalf("exit = %d, output=%s", code, output.String())
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/apps/billing/workflows/runs" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	query, err := url.ParseQuery(f.sawQuery)
	if err != nil {
		t.Fatalf("parse query %q: %v", f.sawQuery, err)
	}
	for key, want := range map[string]string{
		"limit": "20", "offset": "3", "status": "failed", "workflow_name": "paid + invoice",
		"created_after": "2026-10-01T00:00:00Z", "created_before": "2026-10-05T23:59:59Z",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("query %s = %q, want %q; all=%v", key, got, want, query)
		}
	}
}

func TestCmdWorkflowsListRejectsInvalidFiltersBeforeRequest(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"runs":[],"total":0}`, http.StatusOK)
	cases := []struct {
		name string
		args []string
	}{
		{"invalid start timestamp", []string{"--created-after", "yesterday"}},
		{"invalid end timestamp", []string{"--created-before", "tomorrow"}},
		{"reversed timestamps", []string{"--created-after", "2026-10-05T00:00:00Z", "--created-before", "2026-10-01T00:00:00Z"}},
		{"overlong workflow name", []string{"--workflow-name", strings.Repeat("a", api.WorkflowWebhookNameMaxBytes+1)}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"--app", "billing"}, test.args...)
			code, output := runWithStderr(t, func() int { return cmdWorkflowsList(args) })
			if code != 1 || f.sawMethod != "" {
				t.Fatalf("exit=%d stderr=%q request=%s %s", code, output, f.sawMethod, f.sawPath)
			}
		})
	}
}

func TestCmdWorkflowsRun_MissingArgs(t *testing.T) {
	code, captured := runWithStderr(t, func() int { return cmdWorkflowsRun([]string{}) })
	if code != 1 {
		t.Errorf("cmdWorkflowsRun(no args) = %d, want 1", code)
	}
	if !strings.Contains(captured, "gregale workflows run") {
		t.Errorf("usage must mention 'gregale workflows run'; got: %s", captured)
	}
}

func TestCmdWorkflowsRun_InvalidJSON(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdWorkflowsRun([]string{"my-workflow", "--app", "my-app", "--input", "{bad-json"})
	})
	if code != 1 {
		t.Errorf("cmdWorkflowsRun(bad json) = %d, want 1", code)
	}
	if !strings.Contains(captured, "must be valid JSON") {
		t.Errorf("expected JSON validation error, got: %s", captured)
	}
}

func TestCmdWorkflowsStatus_InvalidUUID(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdWorkflowsStatus([]string{"not-a-uuid"})
	})
	if code != 1 {
		t.Errorf("cmdWorkflowsStatus(bad uuid) = %d, want 1", code)
	}
	if !strings.Contains(captured, "invalid run ID") {
		t.Errorf("expected invalid run ID error, got: %s", captured)
	}
}

func TestCmdWorkflowsStatus_PrintsResumeCount(t *testing.T) {
	resetJSONOut(t)
	runID := "00000000-0000-4000-8000-000000000005"
	authedFakeAPI(t, `{"id":"`+runID+`","workflow_name":"paid-invoice","status":"failed","resume_count":2}`, http.StatusOK)
	var stdout bytes.Buffer
	if code := captureStdoutSwap(t, &stdout, func() int { return cmdWorkflowsStatus([]string{runID}) }); code != 0 {
		t.Fatalf("exit = %d, output=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "Resume Count: 2") {
		t.Fatalf("status output = %q, want current resume count", stdout.String())
	}
}

func TestCmdWorkflowsSteps_InvalidUUID(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdWorkflowsSteps([]string{"not-a-uuid"})
	})
	if code != 1 {
		t.Errorf("cmdWorkflowsSteps(bad uuid) = %d, want 1", code)
	}
	if !strings.Contains(captured, "invalid run ID") {
		t.Errorf("expected invalid run ID error, got: %s", captured)
	}
}

func TestCmdWorkflowsResume_UsesExpectedCountAndIdempotencyKey(t *testing.T) {
	resetJSONOut(t)
	runID := "00000000-0000-4000-8000-000000000005"
	f := authedFakeAPI(t, `{"id":"`+runID+`","status":"pending","resume_count":1}`, http.StatusOK)
	if code := cmdWorkflows([]string{"resume", runID, "--expected-resume-count", "0", "--idempotency-key", "resume-after-outage-1"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/workflows/runs/"+runID+"/resume" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	if got := f.sawHeader.Get("Idempotency-Key"); got != "resume-after-outage-1" {
		t.Fatalf("Idempotency-Key = %q", got)
	}
	var body api.ResumeWorkflowRunRequest
	if err := json.Unmarshal(f.sawBody, &body); err != nil {
		t.Fatal(err)
	}
	if body.ExpectedResumeCount == nil || *body.ExpectedResumeCount != 0 {
		t.Fatalf("expected_resume_count = %v", body.ExpectedResumeCount)
	}
}

func TestCmdWorkflowsResumeRequiresCurrentCount(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{}`, http.StatusOK)
	runID := "00000000-0000-4000-8000-000000000005"
	code, stderr := runWithStderr(t, func() int { return cmdWorkflowsResume([]string{runID}) })
	if code != 1 || f.sawMethod != "" || !strings.Contains(stderr, "expected-resume-count") {
		t.Fatalf("exit=%d stderr=%q request=%s %s", code, stderr, f.sawMethod, f.sawPath)
	}
}

func TestCmdWorkflowsResumes_ListsContinuationHistory(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	runID := "00000000-0000-4000-8000-000000000005"
	f := authedFakeAPI(t, `{"resumes":[{"run_id":"`+runID+`","resume_number":1,"account_id":"account","previous_status":"dead","resumed_steps":["send"],"created_at":"2026-10-05T12:00:00Z"}]}`, http.StatusOK)
	var stdout bytes.Buffer
	if code := captureStdoutSwap(t, &stdout, func() int { return cmdWorkflows([]string{"resumes", runID}) }); code != 0 {
		t.Fatalf("exit = %d, output=%s", code, stdout.String())
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/workflows/runs/"+runID+"/resumes" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	var got api.WorkflowResumeResponse
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &got); err != nil {
		t.Fatalf("decode JSON output: %v; output=%s", err, stdout.String())
	}
	if got.ResumeNumber != 1 || got.PreviousStatus != "dead" {
		t.Fatalf("resume history = %+v", got)
	}
}

func TestCmdWorkflowsCancel_InvalidUUID(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdWorkflowsCancel([]string{"not-a-uuid"})
	})
	if code != 1 {
		t.Errorf("cmdWorkflowsCancel(bad uuid) = %d, want 1", code)
	}
	if !strings.Contains(captured, "invalid run ID") {
		t.Errorf("expected invalid run ID error, got: %s", captured)
	}
}

func TestCmdWorkflowsRetry_ValidationAndAPIJourney(t *testing.T) {
	resetJSONOut(t)
	if code, captured := runWithStderr(t, func() int { return cmdWorkflowsRetry([]string{"bad-id", "charge-order"}) }); code != 1 || !strings.Contains(captured, "gregale workflows retry <run_id> <step_name>") {
		t.Fatalf("invalid run: exit=%d stderr=%q", code, captured)
	}

	runID := "00000000-0000-4000-8000-000000000005"
	f := authedFakeAPI(t, `{"id":"`+runID+`","status":"running"}`, http.StatusOK)
	oldOut := osStdout
	var out strings.Builder
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	if code := cmdWorkflows([]string{"retry", runID, "charge-order"}); code != 0 {
		t.Fatalf("retry command exit = %d", code)
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/workflows/runs/"+runID+"/steps/charge-order/retry" {
		t.Fatalf("request = %s %s, want retry POST for the named step", f.sawMethod, f.sawPath)
	}
	if f.sawHeader.Get("Idempotency-Key") == "" {
		t.Fatal("retry request did not carry an idempotency key")
	}
	if !strings.Contains(out.String(), "requeued at step charge-order") {
		t.Fatalf("user-facing result = %q", out.String())
	}
}

func TestCmdWorkflowsEvents_MissingArgs(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdWorkflowsEvents([]string{"send", "not-enough-args"})
	})
	if code != 1 {
		t.Errorf("cmdWorkflowsEvents(not enough args) = %d, want 1", code)
	}
	if !strings.Contains(captured, "gregale workflows events <run_id> <event_name>") {
		t.Errorf("usage must show the documented form; got: %s", captured)
	}
}

// production-us hunt #5 (H5-46): the documented form had no "send" and was
// refused with a usage error.
func TestCmdWorkflowsEvents_DocumentedFormSends(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"status":"received","event_name":"go"}`, http.StatusOK)
	if code := cmdWorkflowsEvents([]string{"00000000-0000-4000-8000-000000000005", "go", "--payload", `{"n":1}`}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(string(f.sawBody), `"n":1`) {
		t.Fatalf("body = %s, want the payload", f.sawBody)
	}
}

func TestCmdWorkflowsEvents_TrailingPayloadIsSent(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"status":"received","event_name":"audit.ready"}`, http.StatusOK)
	runID := "00000000-0000-4000-8000-000000000005"
	if code := cmdWorkflowsEvents([]string{"send", runID, "audit.ready", "--payload", `{"marker":"must-survive"}`}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Payload) != `{"marker":"must-survive"}` {
		t.Fatalf("payload = %s", got.Payload)
	}
}

func TestCmdWorkflowsEvents_TrailingMalformedPayloadFailsBeforeRequest(t *testing.T) {
	resetJSONOut(t)
	runID := "00000000-0000-4000-8000-000000000005"
	code, output := runWithStderr(t, func() int {
		return cmdWorkflowsEvents([]string{"send", runID, "audit.ready", "--payload", "{bad"})
	})
	if code != 1 || !strings.Contains(output, "must be valid JSON") {
		t.Fatalf("exit=%d stderr=%q", code, output)
	}
}

// production-us hunt #5: `workflows get <run>` and `workflows runs` were
// refused with no hint; they are the natural names for status and list.
func TestCmdWorkflowsAcceptsCommonAliases(t *testing.T) {
	for alias, want := range map[string]string{"get": "workflows status", "show": "workflows status", "runs": "--app <slug>", "ls": "--app <slug>"} {
		code, captured := runWithStderr(t, func() int { return cmdWorkflows([]string{alias}) })
		if code != 1 || strings.Contains(captured, "unknown workflows subcommand") || !strings.Contains(captured, want) {
			t.Errorf("workflows %s: code=%d stderr=%q, want the %q usage", alias, code, captured, want)
		}
	}
}

func TestCmdWorkflowsSuggestsTheClosestSubcommand(t *testing.T) {
	_, captured := runWithStderr(t, func() int { return cmdWorkflows([]string{"statsu"}) })
	if !strings.Contains(captured, "status") || !strings.Contains(strings.ToLower(captured), "did you mean") {
		t.Fatalf("typo got no suggestion: %q", captured)
	}
}
