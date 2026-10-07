package imaged

// adr: 435

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/ociref"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

var errScanArtifactMismatch = errors.New("scan artifact differs from producer")

type scanArtifactTarget struct {
	StorageKey string
	rootfs.ArtifactIdentity
}

func (t scanArtifactTarget) valid() bool {
	return t.StorageKey != "" && len(t.StorageKey) <= api.ApplicationStandardBaseMaxStorageKeyBytes && !path.IsAbs(t.StorageKey) && path.Clean(t.StorageKey) == t.StorageKey && t.StorageKey != ".." && !strings.HasPrefix(t.StorageKey, "../") && !strings.ContainsAny(t.StorageKey, "\r\n\x00\\") && ociref.ValidateDigest(t.Digest) == nil && t.Bytes > 0 && t.Bytes <= api.ApplicationStandardBaseMaxArtifactBytes
}

// Only the rich producer path can publish private scan evidence. Legacy scans
// remain readable but cannot acquire producer or native admission authority.
func (h *Handler) routeProducedDeploymentScan(ctx context.Context, app state.App, dep state.Deployment) (bool, error) {
	if h.store == nil || h.log == nil {
		return false, nil
	}
	present, err := producedRuntimePresent(ctx, h.store, app, dep)
	if present || err != nil {
		if err != nil {
			return true, runtimeScanPipelineFailure(ctx, err)
		}
		return true, h.runProducedRuntimeScanGate(ctx, app, dep)
	}
	if _, rich := h.oci.(oci.ImageResolver); rich && dep.Kind == state.DeploymentKindImage && (app.RequireSigned || app.SecurityPolicy == api.AppSecurityPolicyEnforce) {
		return true, verifiedScanFailure(api.AppSecurityPolicyEnforce, "verified runtime producer is missing")
	}
	return false, nil
}

// Separate component fact collection; deployment/renewal routing uses composed scans.
func (h *Handler) collectProducedComponentScans(ctx context.Context, app state.App, dep state.Deployment, main state.DeploymentRegistryRootfs) error {
	var sidecars api.Sidecars
	if len(dep.Sidecars) > 0 && json.Unmarshal(dep.Sidecars, &sidecars) != nil {
		return verifiedScanFailure(app.SecurityPolicy, "scan sidecar declaration is invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, api.ApplicationStandardArtifactScanTimeout)
	defer cancel()
	if main.Input.Kind == "app-layer" {
		if err := h.checkProducedBaseScan(ctx, app, dep, main); err != nil {
			return err
		}
	}
	if err := h.runProducedComponentScan(ctx, app, dep, main, dep.ImageDigest); err != nil {
		return err
	}
	store := h.store.(state.DeploymentRegistryRootfsStore)
	for _, sc := range sidecars {
		root, err := store.GetCurrentDeploymentRegistryRootfs(ctx, app.AccountID, app.ID, dep.ID, sc.Name)
		if err != nil {
			return verifiedScanFailure(app.SecurityPolicy, "sidecar scan producer is missing")
		}
		if err := h.runProducedComponentScan(ctx, app, dep, root, sc.Image); err != nil {
			return err
		}
	}
	return nil
}

func artifactScanInput(app state.App, dep state.Deployment, root state.DeploymentRegistryRootfs, image string) state.DeploymentArtifactScanInput {
	return state.DeploymentArtifactScanInput{ID: uuid.NewString(), RootfsProducerID: root.ID, RootfsInputHash: root.InputHash,
		AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, WorkloadName: root.Input.WorkloadName,
		Scope: dep.Scope, ImageReference: image, ArtifactDigest: root.Input.ArtifactDigest, ArtifactBytes: root.Input.ArtifactBytes}
}

func (h *Handler) runProducedComponentScan(ctx context.Context, app state.App, dep state.Deployment, root state.DeploymentRegistryRootfs, image string) error {
	start, status := time.Now(), "failed"
	defer func() { h.ops.ObserveDeployScanDuration(app.Slug, status, time.Since(start)) }()
	in := artifactScanInput(app, dep, root, image)
	approval, err := h.renewProducedSignature(ctx, app, dep, root)
	if err != nil {
		if producedEvidenceBusy(err) || ctx.Err() != nil {
			return err
		}
		failure := "publisher_unavailable"
		if errors.Is(err, cosign.ErrSignatureInvalid) || errors.Is(err, cosign.ErrSignatureMissing) {
			failure = "publisher_invalid"
		}
		return h.publishArtifactScanFailure(ctx, app, in, failure)
	}
	in.RegistryVerificationID, in.RegistryInputHash = approval.ID, approval.InputHash
	be, err := h.storageFor()
	if err != nil {
		return h.publishArtifactScanFailure(ctx, app, in, "artifact_read")
	}
	expected := scanArtifactTarget{StorageKey: root.Input.StorageKey, ArtifactIdentity: rootfs.ArtifactIdentity{Digest: in.ArtifactDigest, Bytes: in.ArtifactBytes}}
	result, failure := h.readProducedArtifactScan(ctx, be, expected)
	if failure != "" {
		return h.publishArtifactScanFailure(ctx, app, in, failure)
	}
	published, err := h.publishCompleteArtifactScan(ctx, app, dep, in, result)
	if published {
		status = "complete"
	}
	return err
}

func (h *Handler) readProducedArtifactScan(ctx context.Context, be storage.StorageBackend, expected scanArtifactTarget) (*ScanResult, string) {
	path, cleanup, err := stageProducedScanArtifact(ctx, be, h.appsRoot, expected)
	if err != nil {
		if errors.Is(err, errScanArtifactMismatch) {
			return nil, "artifact_mismatch"
		}
		return nil, "artifact_read"
	}
	defer cleanup()
	result, err := h.runGrype(ctx, path)
	if err != nil {
		return nil, "scanner_unavailable"
	}
	if result == nil || result.ScannerName != "grype" || result.Error != "" || result.ScannedAt != "" {
		return nil, "scanner_invalid"
	}
	if checkStagedScanArtifact(ctx, path, expected) != nil || checkStoredScanArtifact(ctx, be, expected) != nil {
		return nil, "artifact_mismatch"
	}
	return result, ""
}

func (h *Handler) publishCompleteArtifactScan(ctx context.Context, app state.App, dep state.Deployment, in state.DeploymentArtifactScanInput, result *ScanResult) (bool, error) {
	store, ok := h.store.(state.DeploymentArtifactScanStore)
	if !ok {
		return false, verifiedScanFailure(app.SecurityPolicy, "durable artifact scan store is unavailable")
	}
	in.Status, in.ScannerName, in.Report = "complete", result.ScannerName, artifactScanReport(result, in)
	value, err := store.PublishDeploymentArtifactScan(ctx, in)
	if err != nil {
		if producedEvidenceBusy(err) || ctx.Err() != nil {
			return false, err
		}
		return false, h.publishArtifactScanFailure(ctx, app, in, "scanner_invalid")
	}
	h.ops.ObserveDeployScanTotal(app.Slug, "complete")
	for severity, count := range result.toMap() {
		h.ops.ObserveDeployScanVulns(app.Slug, severity, count)
	}
	h.log.Info("imaged: produced component scan published", "deployment", dep.ID, "workload", in.WorkloadName, "scan_id", value.ID)
	result.ImageDigest, result.ArtifactDigest, result.ScannedAt = value.Result.ImageDigest, value.Result.ArtifactDigest, value.Result.ScannedAt
	return true, nil // Findings are retained facts, not composed-runtime admission.
}

func (h *Handler) publishArtifactScanFailure(ctx context.Context, app state.App, in state.DeploymentArtifactScanInput, failure string) error {
	in.Status, in.Failure, in.ScannerName, in.Report = "failed", failure, "", nil
	store, ok := h.store.(state.DeploymentArtifactScanStore)
	if ok {
		publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
		defer cancel()
		if _, err := store.PublishDeploymentArtifactScan(publishCtx, in); err != nil {
			h.log.Warn("imaged: failed component scan publication refused", "deployment", in.DeploymentID, "workload", in.WorkloadName)
		}
	}
	h.ops.ObserveDeployScanTotal(app.Slug, "failed")
	return verifiedScanFailure(app.SecurityPolicy, "component scan failed: "+failure)
}

func artifactScanReport(result *ScanResult, in state.DeploymentArtifactScanInput) *api.ScanResult {
	report := &api.ScanResult{ImageDigest: in.ImageReference, ArtifactDigest: in.ArtifactDigest,
		ScannerVersion: result.ScannerVersion, ScannerDBStatus: result.ScannerDBStatus, ScannerDBVersion: result.ScannerDBVersion,
		ScannerDBBuiltAt: result.ScannerDBBuiltAt, SeverityCounts: api.SeverityCounts(result.SeverityCounts)}
	if result.Vulnerabilities != nil {
		report.Vulnerabilities = make([]api.Vulnerability, len(result.Vulnerabilities))
	}
	for i, v := range result.Vulnerabilities {
		report.Vulnerabilities[i] = api.Vulnerability{ID: v.ID, Severity: v.Severity, Package: v.Package, Version: v.Version, FixedIn: v.FixedIn, Paths: append([]string(nil), v.Paths...)}
	}
	return report
}

// Always copy, including local backends: the scanner must never receive the
// mutable canonical path. Hash exactly the bounded bytes written to 0600 scratch.
func stageProducedScanArtifact(ctx context.Context, be storage.StorageBackend, scratch string, expected scanArtifactTarget) (path string, cleanup func(), err error) {
	cleanup = func() {}
	if !expected.valid() {
		return "", cleanup, errScanArtifactMismatch
	}
	rc, err := be.Get(ctx, expected.StorageKey)
	if err != nil {
		return "", cleanup, err
	}
	defer func() {
		err = errors.Join(err, rc.Close())
		if err != nil {
			cleanup()
		}
	}()
	stage, err := os.MkdirTemp(scratch, "imaged-produced-scan-")
	if err != nil {
		return "", cleanup, err
	}
	cleanup = func() { _ = os.RemoveAll(stage) }
	path = filepath.Join(stage, "rootfs.ext4")
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", cleanup, err
	}
	identity, readErr := rootfs.ReadArtifactIdentity(ctx, io.TeeReader(io.LimitReader(rc, expected.Bytes+1), out))
	err = errors.Join(readErr, out.Sync(), out.Close())
	if err != nil {
		return "", cleanup, err
	}
	if identity.Digest != expected.Digest || identity.Bytes != expected.Bytes {
		return "", cleanup, errScanArtifactMismatch
	}
	return path, cleanup, nil
}

func checkStagedScanArtifact(ctx context.Context, path string, expected scanArtifactTarget) error {
	f, err := openStagedScanArtifact(path)
	if err != nil {
		return err
	}
	identity, readErr := rootfs.ReadArtifactIdentity(ctx, io.LimitReader(f, expected.Bytes+1))
	if err := errors.Join(readErr, f.Close()); err != nil {
		return err
	}
	if identity.Digest != expected.Digest || identity.Bytes != expected.Bytes {
		return errScanArtifactMismatch
	}
	return nil
}

func checkStoredScanArtifact(ctx context.Context, be storage.StorageBackend, expected scanArtifactTarget) error {
	rc, err := be.Get(ctx, expected.StorageKey)
	if err != nil {
		return err
	}
	identity, readErr := rootfs.ReadArtifactIdentity(ctx, io.LimitReader(rc, expected.Bytes+1))
	if err := errors.Join(readErr, rc.Close()); err != nil {
		return err
	}
	if identity != expected.ArtifactIdentity {
		return errScanArtifactMismatch
	}
	return nil
}
