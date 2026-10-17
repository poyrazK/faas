package imaged

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/runtimescan"
	"github.com/onebox-faas/faas/pkg/state"
)

type ProducedRuntimeScan struct {
	Inputs          state.DeploymentRuntimeProducerInputs
	Materialization runtimescan.Receipt
	Reports         map[string]*ScanResult
}

// ScanProducedRuntime scans actual native views and rechecks private producer
// freshness. Its return value must still pass durable approval publication;
// it never advances desired policy, observed adoption or runtime admission.
func (h *Handler) ScanProducedRuntime(ctx context.Context, accountID, appID, deploymentID string) (ProducedRuntimeScan, error) {
	store, ok := h.store.(state.DeploymentRuntimeProducerInputStore)
	if !ok {
		return ProducedRuntimeScan{}, runtimeadmission.ErrUnavailable
	}
	owner, ok := h.vmmClient.(RuntimeScanMaterializer)
	if !ok {
		return ProducedRuntimeScan{}, runtimeadmission.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, api.ApplicationStandardArtifactScanTimeout)
	defer cancel()
	inputs, err := store.GetFreshDeploymentRuntimeProducerInputs(ctx, accountID, appID, deploymentID)
	if err != nil {
		return ProducedRuntimeScan{}, err
	}
	if !runtimeScanScopeMatches(inputs, accountID, appID, deploymentID) {
		return ProducedRuntimeScan{}, runtimeadmission.ErrInvalid
	}
	parent := runtimescan.StagingRoot
	if h.runtimeScanParent != "" {
		parent = h.runtimeScanParent
	}
	return scanProducedRuntime(ctx, store, owner, h.runRuntimeGrype, inputs, parent)
}

func scanProducedRuntime(ctx context.Context, store state.DeploymentRuntimeProducerInputStore, owner RuntimeScanMaterializer, scan func(context.Context, string) (*ScanResult, error), inputs state.DeploymentRuntimeProducerInputs, parent string) (result ProducedRuntimeScan, err error) {
	target, err := os.MkdirTemp(parent, runtimescan.TargetPrefix)
	if err != nil {
		return result, err
	}
	defer func() {
		err = errors.Join(err, os.RemoveAll(target), ctx.Err())
		if err != nil {
			result = ProducedRuntimeScan{}
		}
	}()
	request := producedRuntimeScanRequest(inputs, target)
	if err := request.Validate(); err != nil {
		return result, err
	}
	receipt, err := owner.MaterializeRuntimeScan(ctx, request)
	if err != nil {
		return result, err
	}
	if err := receipt.Check(request); err != nil {
		return result, err
	}
	reports, err := scanRuntimeProjection(ctx, scan, receipt)
	if err != nil {
		return result, err
	}
	current, err := store.GetFreshDeploymentRuntimeProducerInputs(ctx, inputs.AccountID, inputs.AppID, inputs.DeploymentID)
	if err != nil {
		return result, err
	}
	if current.InputHash != inputs.InputHash {
		return result, runtimeadmission.ErrStale
	}
	if err := receipt.Check(producedRuntimeScanRequest(current, target)); err != nil {
		return result, err
	}
	return ProducedRuntimeScan{Inputs: current, Materialization: receipt, Reports: reports}, nil
}

func producedRuntimeScanRequest(inputs state.DeploymentRuntimeProducerInputs, target string) runtimescan.Request {
	request := runtimescan.Request{Version: runtimescan.Version, InputHash: inputs.InputHash, TargetDir: target}
	for _, artifact := range inputs.Artifacts {
		request.Sources = append(request.Sources, runtimeadmission.ArtifactSource{Kind: artifact.Kind, WorkloadName: artifact.WorkloadName, StorageKey: artifact.StorageKey, Digest: artifact.Digest, Bytes: artifact.Bytes})
	}
	return request
}

func scanRuntimeProjection(ctx context.Context, scan func(context.Context, string) (*ScanResult, error), receipt runtimescan.Receipt) (map[string]*ScanResult, error) {
	if scan == nil {
		return nil, runtimeadmission.ErrUnavailable
	}
	reports := make(map[string]*ScanResult, len(receipt.Views))
	for _, view := range receipt.Views {
		dir := filepath.Join(receipt.TargetDir, runtimescan.ViewDirectory(view.WorkloadName))
		if err := checkGrypeView(ctx, dir, view.ProjectionTree); err != nil {
			return nil, err
		}
		report, err := scan(ctx, dir)
		if err != nil || report == nil {
			return nil, errors.Join(runtimeadmission.ErrUnavailable, err)
		}
		if err := checkGrypeView(ctx, dir, view.ProjectionTree); err != nil {
			return nil, err
		}
		reports[view.WorkloadName] = report
	}
	return reports, ctx.Err()
}

func (h *Handler) WithRuntimeGrypeRun(run func(context.Context, string) (*ScanResult, error)) *Handler {
	h.runtimeGrypeRun = run
	return h
}

func (h *Handler) runRuntimeGrype(ctx context.Context, dir string) (*ScanResult, error) {
	if h.runtimeGrypeRun != nil {
		return h.runtimeGrypeRun(ctx, dir)
	}
	return runBoundedGrypeView(ctx, "grype", dir)
}

func RunRuntimeGrypeAt(ctx context.Context, bin, dir string) (*ScanResult, error) {
	return runBoundedGrypeView(ctx, bin, dir)
}

// MemStore may retain compact UUID spellings while producer identity hashes
// use canonical UUIDs. Compare parsed IDs and preserve cross-scope refusal.
func runtimeScanScopeMatches(inputs state.DeploymentRuntimeProducerInputs, accountID, appID, depID string) bool {
	for _, pair := range [][2]string{{inputs.AccountID, accountID}, {inputs.AppID, appID}, {inputs.DeploymentID, depID}} {
		actual, err := uuid.Parse(pair[0])
		if err != nil {
			return false
		}
		expected, err := uuid.Parse(pair[1])
		if err != nil || actual != expected {
			return false
		}
	}
	return true
}
