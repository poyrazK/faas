package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Pending submissions contain protocol identities only. Source contents,
// archive paths, options containing customer data, and secrets stay out of state.
type startPendingSubmission struct {
	Key       string `json:"key"`
	Transport string `json:"transport"`
	UploadID  string `json:"upload_id,omitempty"`
	TotalSize int64  `json:"total_size"`
}

func (r *startRunner) recordStartSubmission(submission deploySubmission) error {
	if submission.AppID == "" || submission.Key == "" || submission.TotalSize <= 0 || (r.session.AppID != "" && submission.AppID != r.session.AppID) {
		return errors.New("source submission did not identify the reviewed app and retry operation")
	}
	r.session.AppID = submission.AppID
	r.session.PendingSubmission = &startPendingSubmission{Key: submission.Key, Transport: submission.Transport, TotalSize: submission.TotalSize}
	return saveStartSession(r.statePath, r.session)
}

func (r *startRunner) recordStartUpload(upload deployUploadIdentity) error {
	pending := r.session.PendingSubmission
	if pending == nil || pending.Transport != "resumable" || upload.UploadID == "" || upload.TotalSize != pending.TotalSize {
		return errors.New("upload did not identify the reviewed source submission")
	}
	pending.UploadID = upload.UploadID
	return saveStartSession(r.statePath, r.session)
}

func (r *startRunner) recordStartSubmissionFailure(err error) {
	var phase *deployPhaseError
	// An upload-phase failure precedes commit. Once commit was attempted,
	// even a final 4xx can follow an earlier accepted request with a lost reply.
	if r.session.PendingSubmission != nil && errors.As(err, &phase) && phase.stage == "upload" {
		_ = r.clearUnacceptedSubmission("The source submission was not accepted.")
	}
}

func (r *startRunner) recoverStartSubmission() error {
	pending := r.session.PendingSubmission
	PrintProgress(osStdout, "Checking the pending source submission for %s...", r.session.AppSlug)
	if pending.Transport != "resumable" {
		return errors.New("the server used a legacy source upload whose reply was not confirmed. Inspect app deployment history before submitting again; the recovery record and retry key are preserved")
	}
	if pending.UploadID == "" {
		return r.clearUnacceptedSubmission("No upload was confirmed, so this launch could not commit a deployment.")
	}
	if err := r.verifyStartProject(r.ctx, r.client); err != nil {
		return err
	}
	app, err := r.getLaunchApp(r.ctx, r.client, r.session.AppSlug)
	if err != nil {
		return fmt.Errorf("verify pending app: %w", err)
	}
	if app.ID != r.session.AppID {
		return errors.New("the app identity changed; the pending submission must be inspected before continuing")
	}
	upload, err := r.client.GetUploadSession(r.ctx, pending.UploadID)
	if err != nil {
		return fmt.Errorf("discover pending upload; its outcome remains unconfirmed: %w", err)
	}
	if upload.UploadID != pending.UploadID || upload.AppSlug != r.session.AppSlug || upload.TotalSize != pending.TotalSize || upload.ReceivedBytes < 0 || upload.ReceivedBytes > upload.TotalSize {
		return errors.New("upload discovery did not identify the recorded app and source submission")
	}
	status := strings.ToLower(upload.Status)
	committed := status == "committed" || status == "complete" || status == "completed"
	if (!committed && upload.DeploymentID != nil && *upload.DeploymentID != "") || (committed && upload.ReceivedBytes != upload.TotalSize) {
		return errors.New("upload discovery returned contradictory acceptance evidence; the pending launch remains saved")
	}
	var dep api.DeploymentResponse
	switch status {
	case "committed", "complete", "completed":
		if upload.DeploymentID == nil || *upload.DeploymentID == "" {
			return errors.New("the committed upload did not identify its deployment; keep this recovery record for inspection")
		}
		dep, err = r.client.GetDeployment(r.ctx, *upload.DeploymentID)
		if err == nil && dep.ID != *upload.DeploymentID {
			return errors.New("the deployment response did not match the committed upload")
		}
	case "open":
		if upload.ReceivedBytes != upload.TotalSize || r.cancelPendingUpload {
			return r.cancelIncompleteStartUpload()
		}
		// Continue only this fully uploaded source. The server's atomic commit
		// recovers an in-flight or completed commit without another deployment.
		if err := r.requireStartCompatibility(r.ctx, r.client); err != nil {
			return err
		}
		dep, err = commitUploadWithRetry(api.ContextWithIdempotencyKey(r.ctx, pending.Key), r.client, pending.UploadID)
	case "cancelled", "expired":
		return r.clearUnacceptedSubmission("The source upload ended without a committed deployment.")
	default:
		return errors.New("the upload outcome is not recognized; the pending launch remains saved")
	}
	if err != nil {
		return fmt.Errorf("recover accepted deployment: %w", err)
	}
	if dep.ID == "" || dep.AppID != app.ID || !r.deploymentScopeMatches(dep) {
		return errors.New("the accepted deployment does not match the recorded app and environment")
	}
	recovered := r.session
	recovered.DeploymentID, recovered.Revision, recovered.Status = dep.ID, dep.Revision, dep.Status
	recovered.RequireDeploymentEvidence, recovered.PendingSubmission = true, nil
	if err := saveStartSession(r.statePath, recovered); err != nil {
		return fmt.Errorf("save recovered deployment: %w", err)
	}
	r.session = recovered
	PrintOK(osStdout, "Recovered accepted deployment %s", dep.ID)
	return nil
}

func (r *startRunner) clearUnacceptedSubmission(message string) error {
	previous := r.session.PendingSubmission
	r.session.PendingSubmission = nil
	if err := saveStartSession(r.statePath, r.session); err != nil {
		r.session.PendingSubmission = previous
		return err
	}
	return fmt.Errorf("%s Run gregale start again to review a launch", message)
}

func (r *startRunner) cancelIncompleteStartUpload() error {
	if !r.cancelPendingUpload {
		ok, err := r.prompt.confirm(r.ctx, "The upload is incomplete. Cancel it so source can be reviewed again")
		if err != nil || !ok {
			return errors.New("the unfinished upload and its recovery record are preserved")
		}
	}
	if err := r.client.CancelUpload(r.ctx, r.session.PendingSubmission.UploadID); err != nil {
		return fmt.Errorf("cancel unfinished upload; check this launch again before retrying: %w", err)
	}
	// Do not clear uncertainty merely because a DELETE returned success.
	upload, err := r.client.GetUploadSession(r.ctx, r.session.PendingSubmission.UploadID)
	if err != nil || upload.UploadID != r.session.PendingSubmission.UploadID || upload.AppSlug != r.session.AppSlug || upload.TotalSize != r.session.PendingSubmission.TotalSize || upload.Status != "cancelled" || (upload.DeploymentID != nil && *upload.DeploymentID != "") {
		return errors.New("upload cancellation was not confirmed; check this pending launch again")
	}
	return r.clearUnacceptedSubmission("The unfinished upload was cancelled.")
}
