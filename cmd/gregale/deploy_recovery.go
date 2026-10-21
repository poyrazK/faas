package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Recovery commands use shell quoting and retain the selected connection.
var recoverySafeArgument = regexp.MustCompile(`^[a-zA-Z0-9_.:/-]+$`)

func recoveryArgument(value string) string {
	if recoverySafeArgument.MatchString(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
func recoveryCLI() string {
	if profile := currentProfile(); profile != "default" || profileOverride != "" {
		return "gregale --profile " + recoveryArgument(profile)
	}
	return "gregale"
}

type deployRecovery struct {
	Configuration     *api.DeploymentConfigurationStatus `json:"configuration,omitempty"`
	SessionID         string                             `json:"session_id,omitempty"`
	SubmissionOutcome deploySubmissionOutcome            `json:"submission_outcome,omitempty"`
	Stage             string                             `json:"stage"`
	App               string                             `json:"app,omitempty"`
	DeploymentID      string                             `json:"deployment_id,omitempty"`
	IdempotencyKey    string                             `json:"idempotency_key,omitempty"`
	InspectCommand    string                             `json:"inspect_command"`
	ResumeCommand     string                             `json:"resume_command,omitempty"`
	RetryFlag         string                             `json:"retry_flag,omitempty"`
	Hint              string                             `json:"hint"`
	NextCommand       string                             `json:"next_command,omitempty"`
}

func newDeployRecovery(stage, slug, deploymentID, key string, timeout time.Duration, rollout bool) *deployRecovery {
	r := &deployRecovery{Stage: stage, App: slug, DeploymentID: deploymentID, IdempotencyKey: key}
	r.InspectCommand = recoveryCLI() + " deployments --app " + recoveryArgument(slug)
	if deploymentID != "" {
		r.SubmissionOutcome = deploySubmissionAccepted
		r.InspectCommand = recoveryCLI() + " deployment " + recoveryArgument(deploymentID)
		r.ResumeCommand = deploymentWaitResumeCommandWithRollout(deploymentID, timeout, rollout)
		r.Hint = "The deployment was accepted. Inspect it or resume waiting; do not submit another deployment to resume the wait."
	} else {
		r.Hint = "No deployment ID was confirmed. Inspect deployment history before retrying; the server may have accepted the submission."
		if stage == "upload" {
			r.Hint = "Source upload did not complete. Retry the same archive and options; an existing resumable upload can be recovered when its saved state is available."
		}
		if key != "" {
			r.RetryFlag = "--idempotency-key=" + recoveryArgument(key)
			r.Hint += " Retry the original command with this retry flag, unchanged source and options, and the same profile, endpoint, and account. Server replay retention limits still apply."
		}
	}
	r.NextCommand = r.InspectCommand
	if r.ResumeCommand != "" {
		r.NextCommand = r.ResumeCommand
	}
	return r
}

func failedDeploymentRecovery(slug string, dep api.DeploymentResponse) *deployRecovery {
	r := newDeployRecovery("deployment", slug, dep.ID, "", 0, false)
	r.ResumeCommand = ""
	r.NextCommand = recoveryCLI() + " logs " + recoveryArgument(slug) + " --deployment " + recoveryArgument(dep.ID) + " --explain"
	r.Hint = "The deployment reached a failed terminal state. Read its evidence before submitting a corrected deployment."
	return r
}

type deployPhaseError struct {
	stage string
	err   error
}

func (e *deployPhaseError) Error() string { return e.err.Error() }
func (e *deployPhaseError) Unwrap() error { return e.err }

type deployRecoveryError struct {
	err      error
	recovery *deployRecovery
	exit     int
}

func (e *deployRecoveryError) Error() string { return e.err.Error() }
func (e *deployRecoveryError) Unwrap() error { return e.err }

func printDeploySubmissionError(title string, err error, slug, key, stage, deploymentID string) int {
	var phase *deployPhaseError
	if errors.As(err, &phase) {
		stage = phase.stage
	}
	recovery := newDeployRecovery(stage, slug, deploymentID, key, 0, false)
	recovery.SubmissionOutcome = submissionOutcome(api.DeploymentResponse{ID: deploymentID}, err)
	switch recovery.SubmissionOutcome {
	case deploySubmissionAccepted:
		if deploymentID == "" {
			recovery.Hint = "The server confirmed deployment acceptance without a deployment ID. Inspect deployment history to find it. Staged deployment configuration is preserved."
		} else {
			recovery.Hint += " Staged deployment configuration is preserved."
		}
	case deploySubmissionUnknown:
		recovery.Hint = "Deployment acceptance is unknown. Staged deployment configuration is preserved. Inspect deployment history before retrying."
		if recovery.RetryFlag != "" {
			recovery.Hint += " Retry the original command with this retry flag, unchanged source and options, and the same profile, endpoint, and account. Server replay retention limits still apply."
		}
	case deploySubmissionRejected:
		recovery.Hint = "The deployment submission was rejected or stopped before submission. Correct the error before retrying."
	}
	return printErr(title, &deployRecoveryError{err: err, recovery: recovery})
}

func renderDeployRecoveryError(title string, e *deployRecoveryError) int {
	p := api.Problem{Status: 400, Code: "invalid_request", Title: title, Detail: e.err.Error()}
	code := 1
	var remote *APIError
	var explicit *exitErr
	switch {
	case errors.As(e.err, &remote):
		p = remote.Problem
		code = exitCodeForStatus(p.Status)
	case errors.As(e.err, &explicit):
		code = explicit.code
		if code == 2 {
			p.Status, p.Code, p.Hint = 401, "unauthorized", authenticationHint()
		}
	case isTransportError(e.err):
		p = transportProblem(e.err)
		code = 3
	}
	if errors.Is(e.err, context.Canceled) {
		code = 130
	}
	if e.exit != 0 {
		code = e.exit
		if e.exit == 3 && p.Code == "invalid_request" {
			p.Status, p.Code = 503, "deployment_wait_stopped"
		}
	}
	r := *e.recovery
	if r.DeploymentID == "" && remote != nil && submissionRejected(remote.Problem.Status) {
		// The server answered with a definitive rejection: nothing was
		// accepted, so "the server may have accepted the submission" and
		// an idempotent retry flag were wrong advice (production-us
		// hunt #9: a secret-scan 422). The server's own hint stands.
		r.Hint, r.RetryFlag = "The server rejected this submission; nothing was deployed. Fix the reported problem and deploy again.", ""
		r.NextCommand = ""
	}
	redact := errorTextRedactor()
	r.App, r.DeploymentID, r.IdempotencyKey = redact(r.App), redact(r.DeploymentID), redact(r.IdempotencyKey)
	r.InspectCommand, r.ResumeCommand, r.RetryFlag, r.Hint = redact(r.InspectCommand), redact(r.ResumeCommand), redact(r.RetryFlag), redact(r.Hint)
	r.NextCommand = redact(r.NextCommand)
	p.Hint = strings.TrimSpace(p.Hint + " " + r.Hint)
	p = diagnosticProblem(p)
	if jsonOutput {
		// Keep a single Problem envelope; recovery is a client extension.
		_ = json.NewEncoder(os.Stderr).Encode(struct {
			api.Problem
			Recovery *deployRecovery `json:"recovery"`
			Category string          `json:"category"`
			ExitCode int             `json:"exit_code"`
		}{p, &r, exitCategory(code), code})
	} else {
		renderAPIError(osStderr, &APIError{Problem: p})
		PrintWarn(osStderr, "Stage: %s; inspect: %s", r.Stage, r.InspectCommand)
		if r.DeploymentID != "" && r.ResumeCommand != "" {
			PrintWarn(osStderr, "Deployment: %s; resume: %s", r.DeploymentID, r.ResumeCommand)
		} else if r.ResumeCommand != "" {
			PrintWarn(osStderr, "Resume: %s", r.ResumeCommand)
		}
		if r.RetryFlag != "" {
			PrintWarn(osStderr, "Retry flag for the original command: %s", r.RetryFlag)
		}
		if r.NextCommand != "" {
			_, _ = fmt.Fprintf(osStderr, "Next step (POSIX shells):\n  %s\n", r.NextCommand)
		}
	}
	return code
}

// submissionRejected reports a 4xx that proves the submission was refused
// rather than possibly accepted: everything except timeouts, conflicts with
// an in-flight submission, and rate limiting.
func submissionRejected(status int) bool {
	return status >= 400 && status < 500 && status != 408 && status != 409 && status != 429
}

func printDeploymentWaitRecovery(err error, dep api.DeploymentResponse, slug, stage string, timeout time.Duration, rollout bool, exit int) int {
	return printErr(fmt.Sprintf("Deployment %s stopped during %s", dep.ID, stage), &deployRecoveryError{err: err, recovery: newDeployRecovery(stage, slug, dep.ID, "", timeout, rollout), exit: exit})
}
