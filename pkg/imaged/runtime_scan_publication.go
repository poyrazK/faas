package imaged

// adr: 435. This private job publishes facts; it never approves or admits them.

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeScanPublicationStore interface {
	state.DeploymentRuntimeProducerInputStore
	state.DeploymentRuntimeScanStore
}

// ScanAndPublishProducedRuntime is the composed scan publication job. The
// durable store rechecks the complete producer set under its own fences after
// native cleanup and scanning. Findings do not change desired or observed policy.
func (h *Handler) ScanAndPublishProducedRuntime(ctx context.Context, accountID, appID, deploymentID string) (state.DeploymentRuntimeScan, error) {
	store, ok := h.store.(runtimeScanPublicationStore)
	if !ok {
		return state.DeploymentRuntimeScan{}, runtimeadmission.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, api.ApplicationStandardArtifactScanTimeout)
	defer cancel()
	inputs, err := store.GetFreshDeploymentRuntimeProducerInputs(ctx, accountID, appID, deploymentID)
	if err != nil {
		return state.DeploymentRuntimeScan{}, err
	}
	result, err := h.ScanProducedRuntime(ctx, accountID, appID, deploymentID)
	if err == nil {
		var published state.DeploymentRuntimeScan
		published, err = publishProducedRuntimeScan(ctx, store, result)
		if err == nil {
			return published, nil
		}
	}
	if ctx.Err() != nil || producedEvidenceBusy(err) || errors.Is(err, state.ErrApplicationStandardRuntimeStale) || errors.Is(err, runtimeadmission.ErrStale) {
		return state.DeploymentRuntimeScan{}, err
	}
	return state.DeploymentRuntimeScan{}, errors.Join(err, publishRuntimeScanFailure(ctx, store, inputs, runtimeScanFailureCode(err)))
}

func publishProducedRuntimeScan(ctx context.Context, store state.DeploymentRuntimeScanStore, result ProducedRuntimeScan) (state.DeploymentRuntimeScan, error) {
	if len(result.Reports) != len(result.Materialization.Views) {
		return state.DeploymentRuntimeScan{}, runtimeadmission.ErrInvalid
	}
	reports := make([]state.DeploymentRuntimeScanReport, 0, len(result.Materialization.Views))
	for _, view := range result.Materialization.Views {
		scan, ok := result.Reports[view.WorkloadName]
		if !ok || scan == nil || scan.ScannerName != "grype" || scan.Error != "" || scan.ScannedAt != "" {
			return state.DeploymentRuntimeScan{}, runtimeadmission.ErrInvalid
		}
		report := artifactScanReport(scan, state.DeploymentArtifactScanInput{
			ImageReference: "sha256:" + view.SourceTree.Digest, ArtifactDigest: "sha256:" + result.Materialization.SourcesHash})
		reports = append(reports, state.DeploymentRuntimeScanReport{WorkloadName: view.WorkloadName, Report: *report})
	}
	in, err := state.NewDeploymentRuntimeScanInput(uuid.NewString(), result.Inputs, result.Materialization.Facts(), reports)
	if err != nil {
		return state.DeploymentRuntimeScan{}, err
	}
	return store.PublishDeploymentRuntimeScan(ctx, in)
}

func runtimeScanFailureCode(err error) string {
	if errors.Is(err, runtimeadmission.ErrInvalid) || errors.Is(err, state.ErrInvalidArgument) {
		return "scanner_invalid"
	}
	if errors.Is(err, runtimeadmission.ErrUnavailable) {
		return "scanner_unavailable"
	}
	return "artifact_read"
}

func publishRuntimeScanFailure(ctx context.Context, store state.DeploymentRuntimeScanStore, inputs state.DeploymentRuntimeProducerInputs, failure string) error {
	in, err := state.NewFailedDeploymentRuntimeScanInput(uuid.NewString(), inputs, failure)
	if err != nil {
		return err
	}
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
	defer cancel()
	_, err = store.PublishDeploymentRuntimeScan(publishCtx, in)
	return err
}
