package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sourcedelta"
)

type devSourceSyncState struct {
	manifest    sourcedelta.Manifest
	hasManifest bool
	unsupported bool
}

func deployDeveloperSource(client *Client, ctx context.Context, slug, fullPath, runtime, handler string, dockerfile bool, sourceRoot string, ann api.DeployAnnotations, state *devSourceSyncState) (dep api.DeploymentResponse, err error) {
	if state.unsupported {
		return DeployTarballWithSourceRoot(client, ctx, slug, fullPath, runtime, handler, dockerfile, sourceRoot, ann)
	}
	// Inspection and delta creation happen before any submission request.
	submitting := false
	defer func() {
		if err != nil && !submitting {
			err = submissionError(deploySubmissionRejected, err)
		}
	}()
	limits := sourcedelta.Limits{MaxEntries: api.SourceArchiveMaxEntries}
	fullFile, err := openCustomerFile(fullPath)
	if err != nil {
		return api.DeploymentResponse{}, fmt.Errorf("open developer source: %w", err)
	}
	defer func() { _ = fullFile.Close() }()
	current, err := sourcedelta.Inspect(fullFile, limits)
	if err != nil {
		return api.DeploymentResponse{}, fmt.Errorf("inspect developer source: %w", err)
	}

	sourcePath := fullPath
	baseRevision := ""
	deleted := []string(nil)
	changedFiles := current.RegularFiles
	reusedBytes := int64(0)
	sentBytes := current.CompressedBytes
	if state.hasManifest {
		deltaFile, createErr := os.CreateTemp("", "gregale-dev-delta-*.tar.gz")
		if createErr != nil {
			return api.DeploymentResponse{}, fmt.Errorf("create developer source delta: %w", createErr)
		}
		deltaPath := deltaFile.Name()
		defer func() { _ = deltaFile.Close() }()
		defer func() { _ = os.Remove(deltaPath) }()
		result, createErr := sourcedelta.Create(state.manifest, fullFile, deltaFile, limits)
		if createErr != nil {
			return api.DeploymentResponse{}, fmt.Errorf("create developer source delta: %w", createErr)
		}
		if result.DeltaBytes < current.CompressedBytes {
			sourcePath = deltaPath
			baseRevision = state.manifest.Revision
			deleted = result.Deleted
			changedFiles = result.ChangedFiles
			reusedBytes = result.ReusedBytes
			sentBytes = result.DeltaBytes
		}
	}

	submitting = true
	dep, err = submitDevSource(ctx, client, slug, sourcePath, runtime, handler, dockerfile, sourceRoot, ann, baseRevision, current.Revision, deleted)
	if isDevSourceBaseMissing(err) {
		if !jsonOutput {
			PrintProgress(osStderr, "source cache missed; sending complete snapshot")
		}
		dep, err = submitDevSource(ctx, client, slug, fullPath, runtime, handler, dockerfile, sourceRoot, ann, "", current.Revision, nil)
		changedFiles = current.RegularFiles
		reusedBytes = 0
		sentBytes = current.CompressedBytes
		deleted = nil
	}
	if isDevSourceRouteMissing(err) {
		// Compatibility with an apid deployed before the distinct dev-source
		// route. Never send a delta to the ordinary deploy endpoint.
		state.unsupported = true
		dep, err = DeployTarballWithSourceRoot(client, ctx, slug, fullPath, runtime, handler, dockerfile, sourceRoot, ann)
		changedFiles = current.RegularFiles
		reusedBytes = 0
		sentBytes = current.CompressedBytes
		deleted = nil
	}
	if err != nil {
		return dep, err
	}
	state.manifest = current
	state.hasManifest = true
	if !jsonOutput {
		PrintProgress(osStderr, "source sync: %d changed, %d deleted · %s sent · %s reused", changedFiles, len(deleted), formatDevSourceBytes(sentBytes), formatDevSourceBytes(reusedBytes))
	}
	return dep, nil
}

func isDevSourceBaseMissing(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Code == api.CodeDevSourceBaseMissing
}

func isHTTPStatus(err error, status int) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Status == status
}

// isDevSourceRouteMissing reports the bare 404 of an apid that predates the
// dev-source route. A problem with a code (no such app, no such developer
// environment) is a real answer from a current apid and must not turn deltas
// off for the rest of the session.
func isDevSourceRouteMissing(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Status == 404 && apiErr.Problem.Code == ""
}

// devSubmissionRecoveryAttempts bounds how long a sync whose upload failed in
// transit looks for the deployment the server may still have accepted.
const devSubmissionRecoveryAttempts = 5

// submitDevSource uploads one developer source request under its own
// deployment attempt identity. When the response is lost (a timeout or
// dropped connection after the server accepted the upload), the attempt's
// receipt names the deployment, so the watcher can follow, supersede and
// cancel it instead of leaving an unknown build running beside the next one.
func submitDevSource(ctx context.Context, client *Client, slug, sourcePath, runtime, handler string, dockerfile bool, sourceRoot string, ann api.DeployAnnotations, baseRevision, targetRevision string, deleted []string) (api.DeploymentResponse, error) {
	attemptID, idErr := newDevAttemptID()
	if idErr == nil {
		ctx = api.ContextWithDeploymentAttempt(ctx, attemptID)
	}
	dep, err := DeployDevSourceTarball(client, ctx, slug, sourcePath, runtime, handler, dockerfile, sourceRoot, ann, baseRevision, targetRevision, deleted)
	var apiErr *api.APIError
	if err == nil || idErr != nil || errors.As(err, &apiErr) || ctx.Err() != nil {
		return dep, err
	}
	for range devSubmissionRecoveryAttempts {
		lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		receipt, lookupErr := client.GetDeploymentSubmission(lookupCtx, slug, attemptID)
		if lookupErr == nil && receipt.DeploymentID != "" {
			recovered, getErr := client.GetDeployment(lookupCtx, receipt.DeploymentID)
			cancel()
			if getErr == nil {
				if !jsonOutput {
					PrintProgress(osStderr, "upload response was lost; following accepted deployment %s", recovered.ID)
				}
				return recovered, nil
			}
			return dep, err
		}
		cancel()
		if isHTTPStatus(lookupErr, 404) {
			return dep, err // never accepted
		}
		select {
		case <-ctx.Done():
			return dep, err
		case <-time.After(2 * time.Second):
		}
	}
	return dep, err
}

func newDevAttemptID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func formatDevSourceBytes(value int64) string {
	const (
		kib = int64(1024)
		mib = 1024 * kib
	)
	switch {
	case value >= mib:
		return fmt.Sprintf("%.1f MiB", float64(value)/float64(mib))
	case value >= kib:
		return fmt.Sprintf("%.1f KiB", float64(value)/float64(kib))
	default:
		return fmt.Sprintf("%d B", value)
	}
}
