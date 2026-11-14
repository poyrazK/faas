package main

import (
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type workflowWaitRecovery struct {
	RunID          string `json:"run_id"`
	InspectCommand string `json:"inspect_command"`
	ResumeCommand  string `json:"resume_command"`
}

func workflowWaitResumeCommand(id string, timeout, interval time.Duration) string {
	return savedNameSuggestionCommand([]string{"workflow", "wait", id, "--timeout", timeout.String(), "--poll-interval", interval.String()})
}

func newWorkflowWaitRecovery(id string, timeout, interval time.Duration) *workflowWaitRecovery {
	if !workflowUUIDPattern.MatchString(id) || timeout <= 0 || interval < time.Second || interval > time.Hour {
		return nil
	}
	redact := errorTextRedactor()
	return &workflowWaitRecovery{
		RunID:          redact(id),
		InspectCommand: redact(savedNameSuggestionCommand([]string{"workflow", "status", id})),
		ResumeCommand:  redact(workflowWaitResumeCommand(id, timeout, interval)),
	}
}

func renderWorkflowWaitProblem(problem api.Problem, code int, id string, timeout, interval time.Duration) int {
	problem = diagnosticProblem(problem)
	recovery := newWorkflowWaitRecovery(id, timeout, interval)
	if recovery != nil {
		problem.Hint = strings.TrimSpace(problem.Hint + " The recovery commands only read this run; they do not start, resume, retry or cancel workflow execution.")
	}
	_ = writeJSONWaitProblem(osStderr, problem, code, recovery)
	return code
}

func printWorkflowWaitError(title string, err error, id string, timeout, interval time.Duration) int {
	if !jsonOutput {
		return printErr(title, err)
	}
	var remote *APIError
	if errors.As(err, &remote) {
		return renderWorkflowWaitProblem(remote.Problem, exitCodeForStatus(remote.Problem.Status), id, timeout, interval)
	}
	problem := api.Problem{Status: 400, Code: "invalid_request", Title: title, Detail: err.Error(), DocsURL: cliDocsURL}
	code := 1
	if isTransportError(err) {
		problem, code = transportProblem(err), 3
	}
	return renderWorkflowWaitProblem(problem, code, id, timeout, interval)
}
