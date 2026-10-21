package main

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/bindingrun"
)

type bindingCreationFailureReport struct {
	RunID           string `json:"run_id"`
	App             string `json:"app,omitempty"`
	Project         string `json:"project,omitempty"`
	Environment     string `json:"environment,omitempty"`
	Passed          bool   `json:"passed"`
	Status          string `json:"status"`
	CreateAttempted bool   `json:"create_attempted"`
	Error           string `json:"error"`
	Recovery        string `json:"recovery"`
}

func renderBindingCreationFailure(id, app, project, environment string, err error) int {
	var creation *bindingrun.CreationError
	attempted := errors.As(err, &creation) && creation.Attempted
	report := bindingCreationFailureReport{RunID: id, App: app, Project: project, Environment: environment, Status: "creation_failed", CreateAttempted: attempted, Error: redactErrorText(err.Error()), Recovery: "Inspect this run ID with verification-runs status; do not replace it after an uncertain create response."}
	if !attempted {
		report.Recovery = "Correct the review and options before creating this run ID."
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
		if errors.Is(err, context.Canceled) {
			return 130
		}
		var apiError *APIError
		if errors.As(err, &apiError) {
			return exitCodeForStatus(apiError.Problem.Status)
		}
		var exit *exitErr
		if errors.As(err, &exit) {
			return exit.code
		}
		if isTransportError(err) {
			return 3
		}
		return 1
	}
	return printErr("Could not create verification run", err)
}
