// adr: 463 — resume image preparation from private durable checkpoints.
package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildexport"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

var errImagePreparationRecovery = errors.New("imaged: image preparation remains recoverable")

type imagePublicationKey struct{}
type imagePreparationClaimKey struct{}
type imagePreparationClaim struct {
	id, token string
	store     state.DeploymentImagePreparationStore
}
type imagePublication struct {
	deploymentID, path, key string
	bytes                   int64
}

// Defer the rootfs stamp until the entire layer assembly (including sidecars
// and its secret gate) succeeds. A crash before atomic publication leaves the
// original export referenced and available for another conversion attempt.
func captureImagePublication(ctx context.Context, id, path, key string, bytes int64) bool {
	p, ok := ctx.Value(imagePublicationKey{}).(*imagePublication)
	if !ok || p.deploymentID != id {
		return false
	}
	p.path, p.key, p.bytes = path, key, bytes
	return true
}

func imageRecoveryError(err error) error {
	return fmt.Errorf("%w: %w", errImagePreparationRecovery, err)
}

func (h *Handler) handleSnapshotBoot(ctx context.Context, payload snapshotBootPayload) (err error) {
	images, ok := h.store.(state.DeploymentImagePreparationStore)
	if !ok {
		return h.handleSnapshotBootLegacy(ctx, payload)
	}
	locker, ok := h.store.(state.DeploymentActivationLocker)
	if !ok {
		return errors.New("imaged: image preparation store cannot serialize deployment work")
	}
	if payload.DeploymentID == "" {
		return errors.New("imaged: snapshot_boot missing deployment_id")
	}
	release, err := locker.AcquireDeploymentActivationLock(ctx, payload.DeploymentID)
	if err != nil {
		return imageRecoveryError(err)
	}
	defer release(ctx)
	dep, err := h.store.DeploymentByID(ctx, payload.DeploymentID)
	if err != nil {
		return err
	}
	if dep.Status.IsTerminal() || dep.Status == state.DeployLive {
		return nil
	}
	if dep.RootfsPath == "" && dep.Kind != state.DeploymentKindImage && (dep.Status == state.DeployPending || dep.Status == state.DeployBuilding) {
		return nil
	}
	node := strings.TrimSpace(payload.NodeID)
	if node == "" {
		node = strings.TrimSpace(h.nodeName)
	}
	p, err := images.BeginImagePreparation(ctx, dep.ID, node)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	} // legacy in-flight or already handed off
	if errors.Is(err, state.ErrImagePreparationNotOwned) {
		return db.ErrNotificationNotOwned
	}
	if err != nil {
		return imageRecoveryError(err)
	}
	defer func() {
		if ctx.Err() == nil && !errors.Is(err, errImagePreparationRecovery) &&
			!errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrInvalidStateTransition) {
			h.markFailedOnUnhandledError(ctx, dep.ID, &err)
		}
	}()
	app, err := h.store.AppByID(ctx, dep.AppID)
	if err != nil {
		return err
	}
	app, err = state.AppForDeploymentRuntime(app, dep)
	if err != nil {
		return err
	}
	workCtx := context.WithValue(ctx, imagePreparationClaimKey{}, imagePreparationClaim{id: dep.ID, token: p.ClaimToken, store: images})
	return h.resumeImagePreparation(workCtx, images, app, dep, p)
}

func (h *Handler) resumeImagePreparation(ctx context.Context, images state.DeploymentImagePreparationStore, app state.App, dep state.Deployment, p state.ImagePreparation) error {
	if p.Phase == state.ImagePreparing {
		if err := h.prepareImageLayer(ctx, images, app, dep, p); err != nil {
			return err
		}
		p.Phase = state.ImageLayerPublished
	}
	// Always reload the final artifact and current status. The original input
	// exists only in the private checkpoint and must never be scanned as ext4.
	current, err := h.store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		return imageRecoveryError(err)
	}
	if current.Status.IsTerminal() || current.Status == state.DeployLive {
		return nil
	}
	if p.Phase == state.ImageLayerPublished {
		if err := h.prepareImageScan(ctx, images, app, current, p); err != nil {
			return err
		}
		p.Phase = state.ImageScanComplete
	}
	if p.Phase != state.ImageScanComplete {
		return errors.New("imaged: unsupported image preparation checkpoint")
	}
	return h.finishImageHandoff(ctx, images, current, p)
}

func (h *Handler) prepareImageLayer(ctx context.Context, images state.DeploymentImagePreparationStore, app state.App, dep state.Deployment, p state.ImagePreparation) error {
	input := dep
	input.RootfsPath, input.RootfsKey, input.RootfsBytes = p.InputPath, p.InputKey, p.InputBytes
	lease, exported, err := buildexport.AcquireArtifact(input.RootfsPath)
	if errors.Is(err, buildexport.ErrBusy) {
		return imageRecoveryError(err)
	}
	if err != nil {
		return fmt.Errorf("imaged: acquire image preparation input: %w", err)
	}
	if exported {
		defer func() {
			if err := lease.Close(); err != nil {
				h.log.Warn("imaged: release preparation input", "deployment", dep.ID, "err", err)
			}
		}()
	}
	defer h.releaseBuildCacheLease(ctx, input)
	if err := h.openImagePreparationStage(ctx, app, dep); err != nil {
		return err
	}
	acct, err := h.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return err
	}
	publication := &imagePublication{deploymentID: dep.ID}
	buildCtx := context.WithValue(ctx, imagePublicationKey{}, publication)
	if err := h.buildSnapshotBootLayer(buildCtx, app, input, acct); err != nil {
		return err
	}
	if publication.path == "" || publication.key == "" {
		return errors.New("imaged: image assembly did not produce a rootfs publication")
	}
	if err := images.PublishImagePreparationLayer(ctx, dep.ID, p.ClaimToken, publication.path, publication.key, publication.bytes); err != nil {
		return imageRecoveryError(err)
	}
	return nil
}

func (h *Handler) openImagePreparationStage(ctx context.Context, app state.App, dep state.Deployment) error {
	var stages state.StageState
	if len(dep.StageState) > 0 {
		if err := json.Unmarshal(dep.StageState, &stages); err != nil {
			return err
		}
	}
	from := stages.Current
	if from == "" {
		from = state.StageDependencyRestore
	}
	if from != state.StageDependencyRestore && from != state.StageSourceDownload && from != state.StageImageBuild {
		return fmt.Errorf("imaged: unsupported image preparation stage %q", from)
	}
	if dep.Status == state.DeployImaging && from == state.StageImageBuild {
		return nil
	}
	if from == state.StageImageBuild {
		return h.transition(ctx, dep.ID, state.DeployImaging, "")
	}
	return h.transitionWithStage(ctx, dep.ID, from, state.StageImageBuild, state.DeployImaging, "", hostingFlowForApp(app))
}

func (h *Handler) buildSnapshotBootLayer(ctx context.Context, app state.App, dep state.Deployment, acct state.Account) error {
	switch dep.Kind {
	case state.DeploymentKindImage:
		if app.Type == state.AppTypeFunction {
			return h.buildFunctionLayer(ctx, app, dep, acct)
		}
		return h.buildImageLayer(ctx, app, dep, acct)
	case state.DeploymentKindTarball, state.DeploymentKindDockerfile, state.DeploymentKindGitHub, state.DeploymentKindPreview:
		if app.Type == state.AppTypeFunction || app.Runtime != "" {
			return h.buildFunctionLayer(ctx, app, dep, acct)
		}
		return h.buildLocalOCIAppLayer(ctx, app, &dep, acct)
	default:
		return fmt.Errorf("imaged: snapshot_boot: unknown deployment kind %q", dep.Kind)
	}
}

func (h *Handler) prepareImageScan(ctx context.Context, images state.DeploymentImagePreparationStore, app state.App, dep state.Deployment, p state.ImagePreparation) error {
	// Replication and shared-base staging are idempotent and can be repeated
	// after a crash. Never repeat the customer source build or app conversion.
	if err := h.replicateLayer(ctx, dep.RootfsKey); err != nil {
		return imageRecoveryError(err)
	}
	if err := h.ensureDeploymentRuntimeBaseForDeployment(ctx, app, dep); err != nil {
		return err
	}
	var stages state.StageState
	if len(dep.StageState) > 0 {
		if err := json.Unmarshal(dep.StageState, &stages); err != nil {
			return err
		}
	}
	if stages.Current != state.StageSecurityScan {
		if err := h.transitionWithStage(ctx, dep.ID, state.StageImageBuild, state.StageSecurityScan, state.DeployImaging, "", hostingFlowForApp(app)); err != nil {
			return err
		}
	}
	if err := h.runDeployScan(ctx, app, dep); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := images.AdvanceImagePreparation(ctx, dep.ID, p.ClaimToken, state.ImageLayerPublished, state.ImageScanComplete); err != nil {
		return imageRecoveryError(err)
	}
	return nil
}

func (h *Handler) finishImageHandoff(ctx context.Context, images state.DeploymentImagePreparationStore, dep state.Deployment, p state.ImagePreparation) error {
	current, err := h.store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		return imageRecoveryError(err)
	}
	if current.Status.IsTerminal() || current.Status == state.DeployLive {
		return nil
	}
	dep = current
	app, err := h.store.AppByID(ctx, dep.AppID)
	if err != nil {
		return imageRecoveryError(err)
	}
	app, err = state.AppForDeploymentRuntime(app, dep)
	if err != nil {
		return err
	}
	if err := h.validatePreparedImageScan(ctx, app, dep); err != nil {
		return err
	}

	var stages state.StageState
	if len(dep.StageState) > 0 {
		if err := json.Unmarshal(dep.StageState, &stages); err != nil {
			return err
		}
	}
	// Repair the status-to-stage crash window without replaying a completed
	// stage or moving an already-running readiness check backwards.
	if dep.Status != state.DeploySnapshotting || stages.Current == state.StageSecurityScan {
		if err := h.transitionWithStage(ctx, dep.ID, state.StageSecurityScan, state.StageSnapshotPrepare, state.DeploySnapshotting, "", hostingFlowForApp(app)); err != nil {
			return imageRecoveryError(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// A successful release-task admission is also a durable handoff; its
	// existing terminal outbox notification owns the later snapshot_prime.
	if err := h.handoffSnapshotPrime(ctx, app, dep); err != nil {
		return imageRecoveryError(err)
	}
	current, err = h.store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		return imageRecoveryError(err)
	}
	if current.Status.IsTerminal() || current.Status == state.DeployLive {
		return nil
	}
	if err := images.AdvanceImagePreparation(ctx, dep.ID, p.ClaimToken, state.ImageScanComplete, state.ImageHandedOff); err != nil {
		return imageRecoveryError(err)
	}
	return nil
}

func transitionImagePreparation(ctx context.Context, id string, status state.DeploymentStatus) (bool, error) {
	claim, ok := ctx.Value(imagePreparationClaimKey{}).(imagePreparationClaim)
	if !ok || claim.id != id {
		return false, nil
	}
	return true, claim.store.TransitionImagePreparation(ctx, id, claim.token, status)
}

// Direct OCI deployments enter the same durable preparation pipeline. A
// checkpoint records the input reference before the first imaging transition.
func (h *Handler) handleDeployment(ctx context.Context, p deploymentChangedPayload) error {
	if p.Kind != string(state.DeploymentKindImage) {
		return nil
	}
	if _, ok := h.store.(state.DeploymentImagePreparationStore); !ok {
		return h.handleDeploymentLegacy(ctx, p)
	}
	return h.handleSnapshotBoot(ctx, snapshotBootPayload{AppID: p.AppID, DeploymentID: p.To, NodeID: h.nodeName})
}

// A completed checkpoint must not bypass a tightened policy or the existing
// five-minute freshness gate after a long restart. Reuse only matching fresh
// evidence about the bytes still stored; otherwise refresh the scan alone.
func (h *Handler) validatePreparedImageScan(ctx context.Context, app state.App, dep state.Deployment) error {
	if app.SecurityPolicy != api.AppSecurityPolicyEnforce {
		return nil
	}
	var result ScanResult
	if json.Unmarshal(dep.ScanResult, &result) == nil && checkVerifiedScanGate(app.SecurityPolicy, dep, dep.ScanStatus, &result) == nil {
		be, err := h.storageFor()
		if err != nil {
			return err
		}
		dir, cleanup, err := h.stageScanExt4(ctx, be, app, dep)
		if err == nil {
			defer cleanup()
			digest, hashErr := digestScanArtifact(dir)
			if hashErr == nil && digest == result.ArtifactDigest {
				return nil
			}
		}
	}
	return h.runDeployScan(ctx, app, dep)
}
