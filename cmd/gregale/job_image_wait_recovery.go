package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Initial standalone reads keep printErr's existing exit classification;
// failures after readiness polling starts retain the wait's exit-1 contract.
func jobImageInitialReadCode(err error) int {
	if errors.Is(err, context.Canceled) {
		return 130
	}
	var remote *APIError
	if errors.As(err, &remote) {
		return exitCodeForStatus(remote.Problem.Status)
	}
	if isTransportError(err) {
		return 3
	}
	return 1
}

var (
	errJobImageIdentityChanged   = errors.New("job identity or selected image changed; inspect the Job and start a new wait")
	errJobImageDigestChanged     = errors.New("resolved image digest changed")
	errJobImagePreparationFailed = errors.New("image preparation failed")
)

type jobImageWaitRecovery struct {
	Job                 string `json:"job"`
	Phase               string `json:"phase"`
	Submitted           bool   `json:"submitted"`
	JobID               string `json:"job_id,omitempty"`
	ImageRef            string `json:"image_ref,omitempty"`
	ImageResolvedDigest string `json:"image_resolved_digest,omitempty"`
	InspectCommand      string `json:"inspect_command"`
	ResumeCommand       string `json:"resume_command,omitempty"`
}

func jobImageSelectionError(pin, got api.JobResponse) error {
	if got.ID != pin.ID || got.Name != pin.Name || got.AccountID != pin.AccountID || got.ImageRef != pin.ImageRef {
		return errJobImageIdentityChanged
	}
	if pin.ImageResolvedDigest != "" && got.ImageResolvedDigest != pin.ImageResolvedDigest {
		return errJobImageDigestChanged
	}
	return nil
}

func newJobImageWaitRecovery(name string, selected api.JobResponse, err error, timeout, interval time.Duration) *jobImageWaitRecovery {
	if !jobSlugPattern.MatchString(name) {
		return nil
	}
	redact := errorTextRedactor()
	recovery := &jobImageWaitRecovery{Job: redact(name), Phase: "image_wait",
		InspectCommand: redact(savedNameSuggestionCommand([]string{"job", "get", name}))}
	if selected.Name != name || !jobRunIDPattern.MatchString(selected.ID) || selected.AccountID == "" || selected.ImageRef == "" {
		return recovery
	}
	recovery.JobID, recovery.ImageRef = redact(selected.ID), redact(selected.ImageRef)
	recovery.ImageResolvedDigest = redact(selected.ImageResolvedDigest)
	if !errors.Is(err, errJobImageIdentityChanged) && !errors.Is(err, errJobImageDigestChanged) && timeout > 0 && interval > 0 &&
		(selected.ImageMaterializationStatus == "pending" || selected.ImageMaterializationStatus == "verifying_legacy") {
		recovery.ResumeCommand = redact(savedNameSuggestionCommand([]string{"job", "get", name, "--wait-ready", "--timeout", timeout.String(), "--poll-interval", interval.String()}))
	}
	return recovery
}

func renderJobImageWaitError(err error, code int, name string, selected api.JobResponse, timeout, interval time.Duration) int {
	problem := jobWaitProblem(err, code)
	switch {
	case code == 124:
		problem.Code, problem.Title = "job_image_wait_timeout", "Job image wait timed out"
		problem.Detail = "The local image-readiness wait timed out; image preparation may still continue."
	case code == 130:
		problem.Code, problem.Title = "job_image_wait_interrupted", "Job image wait interrupted"
		problem.Detail = "The local image-readiness wait was interrupted; image preparation may still continue."
	case errors.Is(err, errJobImageIdentityChanged), errors.Is(err, errJobImageDigestChanged):
		problem.Status, problem.Code, problem.Title = http.StatusConflict, "job_image_changed", "Selected Job image changed"
	case errors.Is(err, errJobImagePreparationFailed):
		problem.Status, problem.Code, problem.Title = http.StatusConflict, "job_image_preparation_failed", "Job image preparation failed"
	}
	if code == 124 || code == 130 {
		problem.Hint = "Stopping the CLI wait leaves image preparation unchanged."
	}
	problem.Hint = strings.TrimSpace(problem.Hint + " No Job run was submitted. Inspect the Job and verify its selected image before starting another readiness wait.")
	problem = diagnosticProblem(problem)
	_ = writeJSONWaitProblem(osStderr, problem, code, newJobImageWaitRecovery(name, selected, err, timeout, interval))
	return code
}
