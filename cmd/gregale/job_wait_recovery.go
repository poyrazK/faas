package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type jobWaitRecovery struct {
	Job            string `json:"job"`
	RunID          string `json:"run_id"`
	InspectCommand string `json:"inspect_command"`
	ResumeCommand  string `json:"resume_command"`
}

func newJobWaitRecovery(job, id string, timeout, interval time.Duration) *jobWaitRecovery {
	if !jobSlugPattern.MatchString(job) || !jobRunIDPattern.MatchString(id) || timeout <= 0 || interval <= 0 {
		return nil
	}
	redact := errorTextRedactor()
	return &jobWaitRecovery{
		Job: redact(job), RunID: redact(id),
		InspectCommand: redact(savedNameSuggestionCommand([]string{"job", "status", job, id})),
		ResumeCommand: redact(savedNameSuggestionCommand([]string{"job", "wait", job, id,
			"--timeout", timeout.String(), "--poll-interval", interval.String()})),
	}
}

func renderJobWaitError(err error, code int, job, id string, timeout, interval time.Duration) int {
	problem := jobWaitProblem(err, code)
	recovery := newJobWaitRecovery(job, id, timeout, interval)
	if recovery != nil {
		problem.Hint = strings.TrimSpace(problem.Hint + " Inspect this run or resume waiting with the recovery commands; do not submit another run to resume the wait.")
	}
	problem = diagnosticProblem(problem)
	_ = writeJSONWaitProblem(osStderr, problem, code, recovery)
	return code
}

func jobWaitProblem(err error, code int) api.Problem {
	problem := api.Problem{Status: http.StatusBadRequest, Code: "invalid_request", Title: "Job wait ended", Detail: err.Error(), DocsURL: cliDocsURL}
	var remote *APIError
	switch {
	case code == 124:
		problem.Status, problem.Code, problem.Title = http.StatusRequestTimeout, "job_wait_timeout", "Job wait timed out"
		problem.Detail = "The local wait timed out; the Job run may still be queued or running."
	case code == 130:
		problem.Code, problem.Title = "job_wait_interrupted", "Job wait interrupted"
		problem.Detail = "The local wait was interrupted; the Job run may still be queued or running."
	case errors.As(err, &remote):
		problem = remote.Problem
	case isTransportError(err):
		problem = transportProblem(err)
	}
	if code == 124 || code == 130 {
		problem.Hint = "Stopping the CLI wait leaves the Job run unchanged."
	}
	return problem
}
