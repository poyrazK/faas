package imaged

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

var errInvalidBuildExport = errors.New("imaged: invalid build export")

func (h *Handler) acceptBuildNotification(ctx context.Context, p snapshotBootPayload) (bool, error) {
	if handlesSnapshotBoot(h.nodeName, p.NodeID) {
		return true, nil
	}
	dep, err := h.store.DeploymentByID(ctx, p.DeploymentID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("imaged: inspect regional build notification: %w", err)
	}
	// Producer identity remains authoritative for historical local exports.
	// Remote exports are admitted against the persisted app and worker below.
	return dep.BuildArtifactKey != "", nil
}

func (h *Handler) handleRegionalBuildExport(ctx context.Context, id string) error {
	if _, ok := h.store.(state.DeploymentImagePreparationStore); ok {
		return h.handleSnapshotBoot(ctx, snapshotBootPayload{DeploymentID: id, NodeID: h.nodeName})
	}
	locker, ok := h.store.(state.BuildImageMaterializationLocker)
	if !ok {
		return errors.New("imaged: regional image materialization requires a fleet-wide lock")
	}
	release, err := locker.AcquireBuildImageMaterializationLock(ctx, id)
	if err != nil {
		return fmt.Errorf("imaged: lock regional image materialization: %w", err)
	}
	defer release(ctx)
	return h.consumeRegionalBuildExport(ctx, id)
}

func (h *Handler) consumeRegionalBuildExport(ctx context.Context, id string) error {
	dep, err := h.store.DeploymentByID(ctx, id)
	if err != nil {
		return err
	}
	if dep.Status != state.DeployPending && dep.Status != state.DeployBuilding && dep.Status != state.DeployImaging && dep.Status != state.DeploySnapshotting {
		return nil
	}
	app, err := h.store.AppByID(ctx, dep.AppID)
	if err != nil {
		return err
	}
	eligible, err := h.regionalImageWorkerEligible(ctx, app)
	if err != nil || !eligible {
		return err
	}
	stage, err := regionalBuildStage(dep)
	if err != nil {
		return err
	}
	if dep.Status == state.DeploySnapshotting {
		// Snapshot preparation is scheduler-owned. Notification failures are
		// retryable; terminal or live deployments were excluded above.
		return h.handoffSnapshotPrime(ctx, app, dep)
	}
	if stage != state.StageSecurityScan {
		acct, err := h.store.AccountByID(ctx, app.AccountID)
		if err != nil {
			return err
		}
		if err := h.materializeRegionalBuild(ctx, app, dep, acct, stage); err != nil {
			return err
		}
	}
	return h.finishRegionalBuildImage(ctx, app, id)
}

func (h *Handler) finishRegionalBuildImage(ctx context.Context, app state.App, id string) error {
	// Reload the final layer metadata after conversion or process recovery.
	dep, err := h.store.DeploymentByID(ctx, id)
	if err != nil {
		return err
	}
	if dep.Status != state.DeployImaging {
		return nil
	}
	if err := h.ensureDeploymentRuntimeBase(ctx, app); err != nil {
		return err
	}
	if err := h.runDeployScan(ctx, app, dep); err != nil {
		_ = h.markDeployFailed(ctx, id, err, "verified image scan gate")
		return err
	}
	if err := h.advanceRegionalImage(ctx, dep, state.DeploySnapshotting, state.StageSecurityScan, state.StageSnapshotPrepare, app); err != nil {
		return err
	}
	return h.handoffSnapshotPrime(ctx, app, dep)
}

func (h *Handler) regionalImageWorkerEligible(ctx context.Context, app state.App) (bool, error) {
	if h.nodeName == "" || app.ComputeRegion == "" || app.Status == state.AppDeleted {
		return false, nil
	}
	node, err := h.store.ComputeNodeByName(ctx, h.nodeName)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("imaged: inspect regional image worker: %w", err)
	}
	return node.Active && node.Region != nil && *node.Region == app.ComputeRegion, nil
}

func regionalBuildStage(dep state.Deployment) (state.StageName, error) {
	var ss state.StageState
	if len(dep.StageState) > 0 {
		if err := json.Unmarshal(dep.StageState, &ss); err != nil {
			return "", fmt.Errorf("imaged: regional image stage: %w", err)
		}
	}
	if dep.Status == state.DeploySnapshotting {
		return ss.Current, nil
	}
	if ss.Current == "" {
		ss.Current = state.StageSourceDownload
	}
	switch ss.Current {
	case state.StageSourceDownload, state.StageDependencyRestore, state.StageImageBuild:
		return ss.Current, nil
	case state.StageSecurityScan:
		if dep.Status == state.DeployImaging {
			return ss.Current, nil
		}
	}
	return "", fmt.Errorf("imaged: unsupported regional image stage %q in %q", ss.Current, dep.Status)
}

func (h *Handler) materializeRegionalBuild(ctx context.Context, app state.App, dep state.Deployment, acct state.Account, stage state.StageName) error {
	path, cleanup, err := h.downloadRegionalBuildExport(ctx, app, dep, acct)
	if err != nil {
		if errors.Is(err, errInvalidBuildExport) {
			_ = h.markDeployFailed(ctx, dep.ID, err, "verify builder export")
		}
		return err
	}
	defer cleanup()
	if err := h.advanceRegionalImage(ctx, dep, state.DeployImaging, stage, state.StageImageBuild, app); err != nil {
		return err
	}
	dep.Status, dep.RootfsPath = state.DeployImaging, path
	if err := h.assembleBuiltDeployment(ctx, app, dep, acct); err != nil {
		if ctx.Err() == nil {
			_ = h.markDeployFailed(ctx, dep.ID, err, "materialize builder export")
		}
		return err
	}
	return h.advanceRegionalImage(ctx, dep, state.DeployImaging, state.StageImageBuild, state.StageSecurityScan, app)
}

func (h *Handler) advanceRegionalImage(ctx context.Context, dep state.Deployment, to state.DeploymentStatus, fromStage, toStage state.StageName, app state.App) error {
	s, ok := h.store.(state.BuildImageStateStore)
	if !ok {
		return errors.New("imaged: regional image state transitions required")
	}
	if err := s.AdvanceBuildImageStatus(ctx, dep.ID, dep.Status, to); err != nil {
		return fmt.Errorf("imaged: advance regional image: %w", err)
	}
	if fromStage == toStage {
		return nil
	}
	return h.appendImageStage(ctx, dep.ID, fromStage, toStage, hostingFlowForApp(app))
}

func (h *Handler) downloadRegionalBuildExport(ctx context.Context, app state.App, dep state.Deployment, acct state.Account) (string, func(), error) {
	ref := dep.BuildArtifactReference()
	lim, known := api.LimitsFor(acct.Plan)
	if !ref.ValidFor(app.ComputeRegion) || !known || ref.Bytes > int64(lim.AppLayerMaxMB)<<20 {
		return "", nil, fmt.Errorf("%w: descriptor or plan limit", errInvalidBuildExport)
	}
	be, err := h.storageFor()
	if err != nil {
		return "", nil, err
	}
	if err := storage.ValidateArtifactRoute(be, ref.Key); err != nil {
		return "", nil, fmt.Errorf("imaged: build export route: %w", err)
	}
	r, err := be.Get(ctx, ref.Key)
	if err != nil {
		return "", nil, fmt.Errorf("imaged: fetch build export: %w", err)
	}
	defer func() { _ = r.Close() }()
	dir, err := os.MkdirTemp("", "gregale-build-export-")
	if err != nil {
		return "", nil, fmt.Errorf("imaged: stage build export: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	success := false
	defer func() {
		if !success {
			cleanup()
		}
	}()
	path := filepath.Join(dir, "image.oci.tar")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", nil, fmt.Errorf("imaged: create build export: %w", err)
	}
	defer func() { _ = f.Close() }()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(r, ref.Bytes+1))
	if err != nil {
		return "", nil, fmt.Errorf("imaged: download build export: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if n != ref.Bytes || hex.EncodeToString(hash.Sum(nil)) != ref.SHA256 {
		return "", nil, fmt.Errorf("%w: size or SHA-256 mismatch", errInvalidBuildExport)
	}
	if err := f.Sync(); err != nil {
		return "", nil, fmt.Errorf("imaged: sync build export: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", nil, fmt.Errorf("imaged: close build export: %w", err)
	}
	success = true
	return path, cleanup, nil
}
