package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// These read-only commands identify the wait target, without claiming that a
// caller-supplied ID was accepted or that the invocation is still running.
type invocationWaitRecovery struct {
	InvocationID   string `json:"invocation_id"`
	InspectCommand string `json:"inspect_command"`
	ResumeCommand  string `json:"resume_command"`
}

func newInvocationWaitRecovery(id string) *invocationWaitRecovery {
	if !validInvocationWaitID(id) {
		return nil
	}
	redact := errorTextRedactor()
	return &invocationWaitRecovery{
		InvocationID:   redact(id),
		InspectCommand: redact(invocationRecoveryCommand("get", id)),
		ResumeCommand:  redact(invocationRecoveryCommand("wait", id)),
	}
}

func invocationRecoveryCommand(verb, id string) string {
	args := []string{"invocation", verb}
	if strings.HasPrefix(id, "-") {
		args = append(args, "--")
	}
	return savedNameSuggestionCommand(append(args, id))
}

// Invocation wait retains its dedicated exit contract instead of the shared
// HTTP mapping. Recovery is a CLI extension of the single Problem envelope.
func renderInvocationWaitProblem(problem api.Problem, code int, id string) int {
	problem = diagnosticProblem(problem)
	if jsonOutput {
		_ = json.NewEncoder(osStderr).Encode(struct {
			api.Problem
			Recovery  *invocationWaitRecovery `json:"recovery,omitempty"`
			Category  string                  `json:"category"`
			ExitCode  int                     `json:"exit_code"`
			RequestID string                  `json:"request_id,omitempty"`
		}{Problem: problem, Recovery: newInvocationWaitRecovery(id), Category: exitCategory(code), ExitCode: code, RequestID: safeProblemRequestID(problem)})
	} else {
		renderAPIError(osStderr, &APIError{Problem: problem})
	}
	return code
}

func renderInvocationWaitError(err error, id string, accepted bool) int {
	if !jsonOutput {
		if accepted {
			err = fmt.Errorf("%w; %s", err, invocationResumeHint(id))
		}
		return printErr("Could not wait for invocation", err)
	}
	problem := api.Problem{Status: 400, Code: "invalid_request", Title: "Could not wait for invocation", Detail: err.Error()}
	code := 1
	if isTransportError(err) {
		problem, code = transportProblem(err), 3
	}
	if errors.Is(err, context.Canceled) {
		code = 130
	}
	if accepted {
		problem.Hint = strings.TrimSpace(problem.Hint + " " + invocationResumeHint(id))
	}
	return renderInvocationWaitProblem(problem, code, id)
}
