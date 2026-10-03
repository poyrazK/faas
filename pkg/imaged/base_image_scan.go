package imaged

// adr: 435

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func (h *Handler) ensureProducedBaseScan(ctx context.Context, be storage.StorageBackend, base state.BaseImageProducer, out string) (state.BaseImageScan, error) {
	if err := writeProducedBaseScanCompatibility(ctx, be, base.Input.Artifact.StorageKey, base.Input.SourceReference, out, state.BaseImageScan{}); err != nil {
		return state.BaseImageScan{}, err
	}
	value, err := h.loadOrRunProducedBaseScan(ctx, be, base)
	if err != nil {
		return state.BaseImageScan{}, err
	}
	if err := writeProducedBaseScanCompatibility(ctx, be, base.Input.Artifact.StorageKey, base.Input.SourceReference, out, value); err != nil {
		return state.BaseImageScan{}, err
	}
	return value, nil
}

func (h *Handler) loadOrRunProducedBaseScan(ctx context.Context, be storage.StorageBackend, base state.BaseImageProducer) (state.BaseImageScan, error) {
	store, ok := h.store.(state.BaseImageScanStore)
	if !ok {
		return state.BaseImageScan{}, fmt.Errorf("imaged: durable shared-base scan store unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, api.ApplicationStandardArtifactScanTimeout)
	defer cancel()
	expected := scanArtifactTarget{StorageKey: base.Input.Artifact.StorageKey, ArtifactIdentity: rootfs.ArtifactIdentity{Digest: base.Input.Artifact.Digest, Bytes: base.Input.Artifact.Bytes}}
	current, err := store.GetFreshBaseImageScan(ctx, base.ID, base.InputHash)
	if err == nil && !producedEvidenceRenewalDue(current.ScannedAt, current.ExpiresAt, time.Now().UTC()) && checkStoredScanArtifact(ctx, be, expected) == nil {
		current, err = store.GetFreshBaseImageScan(ctx, base.ID, base.InputHash)
		if err == nil {
			return current, nil
		}
	}
	if err != nil && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		return state.BaseImageScan{}, err
	}
	in := state.BaseImageScanInput{ID: uuid.NewString(), BaseProducerID: base.ID, BaseInputHash: base.InputHash, Artifact: base.Input.Artifact, SourceReference: base.Input.SourceReference}
	result, failure := h.readProducedArtifactScan(ctx, be, expected)
	if failure != "" {
		return h.publishBaseScanFailure(ctx, store, in, failure)
	}
	in.Status, in.ScannerName = "complete", result.ScannerName
	in.Report = artifactScanReport(result, state.DeploymentArtifactScanInput{ImageReference: in.SourceReference, ArtifactDigest: in.Artifact.Digest})
	value, err := store.PublishBaseImageScan(ctx, in)
	if err != nil {
		if errors.Is(err, state.ErrInvalidArgument) || errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
			return h.publishBaseScanFailure(ctx, store, in, "scanner_invalid")
		}
		return state.BaseImageScan{}, err
	}
	return value, nil
}

func (h *Handler) publishBaseScanFailure(ctx context.Context, store state.BaseImageScanStore, in state.BaseImageScanInput, failure string) (state.BaseImageScan, error) {
	in.Status, in.Failure, in.ScannerName, in.Report = "failed", failure, "", nil
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
	defer cancel()
	return store.PublishBaseImageScan(publishCtx, in)
}

func (h *Handler) checkProducedBaseScan(ctx context.Context, app state.App, dep state.Deployment, root state.DeploymentRegistryRootfs) error {
	store, ok := h.store.(state.BaseImageProducerStore)
	if !ok {
		return verifiedScanFailure(app.SecurityPolicy, "shared-base producer store unavailable")
	}
	base, err := store.GetBaseImageProducerByID(ctx, root.Input.BaseProducerID)
	if err != nil || base.InputHash != root.Input.BaseInputHash {
		return verifiedScanFailure(app.SecurityPolicy, "shared-base producer binding is invalid")
	}
	be, err := h.storageFor()
	if err != nil {
		return verifiedScanFailure(app.SecurityPolicy, "shared-base scan storage unavailable")
	}
	value, err := h.ensureProducedBaseScan(ctx, be, base, "")
	if err != nil {
		if producedEvidenceBusy(err) || ctx.Err() != nil {
			return err
		}
		return verifiedScanFailure(app.SecurityPolicy, "shared-base scan publication refused")
	}
	result, err := scanResultFromAPI(value.Result)
	if err != nil {
		return verifiedScanFailure(app.SecurityPolicy, "shared-base scan report invalid")
	}
	dep.ImageDigest = base.Input.SourceReference
	return checkVerifiedScanGate(app.SecurityPolicy, dep, value.Input.Status, result)
}

func scanResultFromAPI(report api.ScanResult) (*ScanResult, error) {
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	var value ScanResult
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

// Compatibility output comes only from the separately retained private scan.
// It is not a native receipt or company approval. Failed evidence retains the
// legacy refusal sentinel; those numbers are never private scanner findings.
func (h *Handler) writeProducedBaseScanSidecar(ctx context.Context, r verifiedBaseRequest, base state.BaseImageProducer) error {
	_, err := h.ensureProducedBaseScan(ctx, r.be, base, r.out)
	return err
}

// An empty value is a preflight refusal, not a scan attempt: its clock stays
// zero. Invalidate old compatibility output before rebuilding or refreshing;
// only a successfully retained private result can replace that refusal.
func writeProducedBaseScanCompatibility(ctx context.Context, be storage.StorageBackend, key, ref, out string, value state.BaseImageScan) error {
	findings := map[string]int{SeverityCritical: 9999, SeverityHigh: 9999, SeverityMedium: 9999, SeverityLow: 9999, SeverityUnknown: 0}
	fixAvailable := findings
	if value.Input.Status == "complete" {
		result, err := scanResultFromAPI(value.Result)
		if err != nil {
			return err
		}
		findings, fixAvailable = result.toMap(), result.fixAvailableToMap()
	}
	source, err := scanSourceForBase(be, key, out)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		Image                string         `json:"image"`
		Source               string         `json:"source"`
		Findings             map[string]int `json:"findings"`
		FixAvailableFindings map[string]int `json:"fix_available_findings"`
		ScannedAt            time.Time      `json:"scanned_at"`
	}{ref, source, findings, fixAvailable, value.ScannedAt})
	if err != nil {
		return err
	}
	return be.Put(ctx, wire.ScanKeyForBaseKey(key), bytes.NewReader(raw))
}
