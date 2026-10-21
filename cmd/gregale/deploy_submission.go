package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

type deploySubmissionOutcome string

const (
	deploySubmissionAccepted deploySubmissionOutcome = "accepted"
	deploySubmissionRejected deploySubmissionOutcome = "rejected"
	deploySubmissionUnknown  deploySubmissionOutcome = "unknown"
)

type deploySubmissionError struct {
	outcome deploySubmissionOutcome
	err     error
}

func (e *deploySubmissionError) Error() string { return e.err.Error() }
func (e *deploySubmissionError) Unwrap() error { return e.err }

func submissionError(outcome deploySubmissionOutcome, err error) error {
	if err == nil {
		return nil
	}
	return &deploySubmissionError{outcome: outcome, err: err}
}

// Acceptance evidence survives a failed status read. Explicit outcomes also
// retain uncertainty from earlier attempts when a later retry is rejected.
func submissionOutcome(dep api.DeploymentResponse, err error) deploySubmissionOutcome {
	if dep.ID != "" || err == nil {
		return deploySubmissionAccepted
	}
	var submission *deploySubmissionError
	if errors.As(err, &submission) {
		return submission.outcome
	}
	if isUploadProblem(err, uploadSessionAlreadyCommittedCode) {
		return deploySubmissionAccepted
	}
	if isUploadProblem(err, api.CodeDeploymentAttemptAccepted) || isUploadProblem(err, api.CodeDeploymentAttemptPending) {
		return deploySubmissionUnknown
	}
	var remote *api.APIError
	if errors.As(err, &remote) && remote.Problem.Status >= http.StatusBadRequest && remote.Problem.Status < http.StatusInternalServerError && remote.Problem.Status != http.StatusRequestTimeout {
		return deploySubmissionRejected
	}
	return deploySubmissionUnknown
}

// Guided workflows persist these identities before source can be submitted.
// The callback must succeed before the corresponding remote request proceeds.
type deploySubmission struct {
	Key, Transport, AppID string
	TotalSize             int64
}

type deployUploadIdentity struct {
	UploadID  string
	TotalSize int64
}

func (e deployExecution) preparingSubmission(key, transport, appID, archive string) error {
	if e.onSubmission == nil {
		return nil
	}
	info, err := os.Stat(archive)
	if err != nil {
		return fmt.Errorf("inspect source submission: %w", err)
	}
	return e.onSubmission(deploySubmission{Key: key, Transport: transport, AppID: appID, TotalSize: info.Size()})
}
