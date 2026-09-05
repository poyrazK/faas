package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sourcedelta"
)

type devSourceSyncState struct {
	manifest    sourcedelta.Manifest
	hasManifest bool
	unsupported bool
}

func deployDeveloperSource(client *Client, ctx context.Context, slug, fullPath, runtime, handler string, dockerfile bool, sourceRoot string, ann api.DeployAnnotations, state *devSourceSyncState) (api.DeploymentResponse, error) {
	if state.unsupported {
		return DeployTarballWithSourceRoot(client, ctx, slug, fullPath, runtime, handler, dockerfile, sourceRoot, ann)
	}
	limits := sourcedelta.Limits{MaxEntries: api.SourceArchiveMaxEntries}
	current, err := sourcedelta.Inspect(fullPath, limits)
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
		_ = deltaFile.Close()
		defer func() { _ = os.Remove(deltaPath) }()
		result, createErr := sourcedelta.Create(state.manifest, fullPath, deltaPath, limits)
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

	dep, err := DeployDevSourceTarball(client, ctx, slug, sourcePath, runtime, handler, dockerfile, sourceRoot, ann, baseRevision, current.Revision, deleted)
	if isDevSourceBaseMissing(err) {
		if !jsonOutput {
			PrintProgress(osStderr, "source cache missed; sending complete snapshot")
		}
		dep, err = DeployDevSourceTarball(client, ctx, slug, fullPath, runtime, handler, dockerfile, sourceRoot, ann, "", current.Revision, nil)
		changedFiles = current.RegularFiles
		reusedBytes = 0
		sentBytes = current.CompressedBytes
		deleted = nil
	}
	if isHTTPStatus(err, 404) {
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
		return api.DeploymentResponse{}, err
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
