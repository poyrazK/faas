package imaged

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (h *Handler) WithExecutionEnvironments(enabled bool) *Handler {
	h.executionEnvironmentsEnabled = enabled
	return h
}

// MaterializeExecutionEnvironments always services retained cleanup obligations;
// the flag controls new image claims only. Each attempt's keys were recorded
// before either owner wrote bytes, so a crash cannot lose GC ownership.
func (h *Handler) MaterializeExecutionEnvironments(ctx context.Context) error {
	store, ok := h.store.(state.ExecutionEnvironmentStore)
	if !ok {
		return nil
	}
	backend, err := h.storageFor()
	if err != nil {
		return err
	}
	rows, err := store.CollectableExecutionEnvironments(ctx)
	if err != nil {
		return err
	}
	var gcErr error
	for _, row := range rows {
		if err := collectExecutionEnvironment(ctx, store, backend, row); err != nil {
			gcErr = errors.Join(gcErr, err)
		}
	}
	if !h.executionEnvironmentsEnabled {
		return gcErr
	}
	claim, err := store.ClaimExecutionEnvironment(ctx, "image")
	if errors.Is(err, state.ErrNotFound) {
		return gcErr
	}
	if err != nil {
		return err
	}
	imageCtx, cancel := context.WithTimeout(ctx, api.ExecutionEnvironmentImageTimeoutSeconds*time.Second)
	defer cancel()
	_ = environmentImageDiagnostic(imageCtx, h.store, claim, "verify", "progress", "Verifying the exported OCI image and bounded filesystem layers.")
	if err := h.materializeExecutionEnvironment(imageCtx, store, backend, claim); err != nil {
		cleanup, c := context.WithTimeout(context.WithoutCancel(ctx), api.ExecutionEnvironmentCleanupSeconds*time.Second)
		defer c()
		code, message := "image_failed", "Image verification or publication failed. Check the image-size limit and contact support if the recipe is valid."
		if errors.Is(err, errEnvironmentScanUnavailable) {
			code = "scan_unavailable"
			message = "The vulnerability scanner could not produce a complete verdict. Retry after scanner recovery or contact support."
		} else if errors.Is(err, errEnvironmentScanRejected) {
			code = "scan_rejected"
			message = "The scanner found fixable critical vulnerabilities. Update pinned dependencies or request a patched installer image."
		}
		_ = environmentImageDiagnostic(cleanup, h.store, claim, "verify", code, message)
		_ = store.FailExecutionEnvironment(cleanup, claim, "image_failed")
		return fmt.Errorf("imaged: Run environment image_failed: %w", err)
	}
	return gcErr
}

func collectExecutionEnvironment(ctx context.Context, store state.ExecutionEnvironmentStore, backend storage.StorageBackend, row state.ExecutionEnvironment) error {
	// Validate the whole obligation before deleting anything. A corrupt row
	// cannot delete another tenant's object or block cleanup for its peers.
	for _, key := range row.ArtifactKeys {
		if !environmentOwnedKey(row, key) {
			return errors.New("imaged: invalid environment artifact ownership")
		}
	}
	for _, key := range row.ArtifactKeys {
		if err := backend.Delete(ctx, key); err != nil && !storage.IsNotFound(err) {
			return err
		}
	}
	return store.DeleteExecutionEnvironmentArtifacts(ctx, row.ID)
}

func environmentOwnedKey(row state.ExecutionEnvironment, key string) bool {
	if strings.HasPrefix(key, "scans/run-environments/") {
		key = strings.TrimPrefix(key, "scans/")
		if !strings.HasSuffix(key, "base.ext4.scan.json") {
			return false
		}
		key = strings.TrimSuffix(key, ".scan.json")
	}
	stem := "run-environments/" + row.AccountID + "/" + row.ID + "/"
	if !strings.HasPrefix(key, stem) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(key, stem), "/")
	if len(parts) != 2 || !runenv.ValidID(parts[0]) {
		return false
	}
	return parts[1] == "image.tar" || parts[1] == "base.ext4" || parts[1] == "layer.ext4" || parts[1] == "base.ext4.digest" || parts[1] == "base.ext4.content"
}

func (h *Handler) materializeExecutionEnvironment(ctx context.Context, store state.ExecutionEnvironmentStore, backend storage.StorageBackend, row state.ExecutionEnvironment) error {
	acct, err := h.store.AccountByID(ctx, row.AccountID)
	if err != nil || !acct.Active() {
		return state.ErrExecutionEnvironmentUnavailable
	}
	cap := api.ExecutionEnvironmentBytesByPlan[acct.Plan.PlanIndex()]
	if cap <= 0 || !environmentOwnedKey(row, row.ExportKey) || !environmentOwnedKey(row, row.BaseKey) {
		return state.ErrExecutionInvalid
	}
	archive, err := storage.GetCanonical(ctx, backend, row.ExportKey)
	if err != nil {
		return err
	}
	defer func() { _ = archive.Close() }()
	tmp, err := os.CreateTemp("", "run-environment-*.tar")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(archive, cap+1))
	if err != nil || size > cap || hex.EncodeToString(hash.Sum(nil)) != row.ExportDigest {
		return errors.New("environment export integrity failed")
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	var proof localOCIProvenance
	_, layers, cleanup, err := loadLocalOCIArchiveWithProvenance(ctx, tmp.Name(), &proof)
	if err != nil {
		return err
	}
	defer cleanup()
	readers := make([]io.Reader, len(layers))
	for i := range layers {
		readers[i] = layers[i]
	}
	staging, err := rootfs.MkdirBaseStaging()
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := rootfs.ApplyLayersGzWithin(staging, readers, cap-api.ExecutionEnvironmentImageHeadroomBytes); err != nil {
		return err
	}
	marker, _ := json.Marshal(map[string]any{"kind": "execution", "version": 1, "environment_id": row.ID})
	// Replace the marker with platform-authored bytes after safe extraction.
	root, err := os.OpenRoot(staging)
	if err != nil {
		return err
	}
	if err := root.MkdirAll("etc/faas", 0755); err != nil {
		_ = root.Close()
		return err
	}
	markerFile, err := root.OpenFile("etc/faas/execution.json", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		_ = root.Close()
		return err
	}
	_, writeErr := markerFile.Write(marker)
	closeErr := markerFile.Close()
	_ = root.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	base, err := h.builder.BuildBaseFromStaging(ctx, staging, rootfs.BaseBuildInput{Storage: backend, StorageKey: row.BaseKey, GuestInitPath: h.guestInitPath})
	if err != nil {
		return err
	}
	layerRoot, err := rootfs.MkdirBaseStaging()
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(layerRoot) }()
	if err := os.MkdirAll(filepath.Join(layerRoot, "etc/faas"), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(layerRoot, "etc/faas/execution.json"), marker, 0644); err != nil {
		return err
	}
	layer, err := h.builder.BuildBaseFromStaging(ctx, layerRoot, rootfs.BaseBuildInput{Storage: backend, StorageKey: state.ExecutionEnvironmentLayerKey(row)})
	if err != nil {
		return err
	}
	if base.ArtifactBytes+layer.ArtifactBytes > cap {
		return errors.New("environment artifacts exceed plan")
	}
	_ = environmentImageDiagnostic(ctx, h.store, row, "scan", "progress", "Scanning the exact produced image bytes; publication requires a complete accepted verdict.")
	if err := h.publishExecutionEnvironmentBase(ctx, backend, row, proof, base); err != nil {
		return err
	}
	_ = environmentImageDiagnostic(ctx, h.store, row, "publish", "progress", "Image identity, producer proof and vulnerability scan verified; publishing the immutable environment.")
	if err := store.PublishExecutionEnvironmentImage(ctx, row, strings.TrimPrefix(proof.Image.Config.Digest, "sha256:"), strings.TrimPrefix(base.GuestInitDigest, "sha256:"), base.ArtifactBytes+layer.ArtifactBytes); err != nil {
		return err
	}
	// The immutable ready base and layer remain; all attempt keys stay in the
	// catalog so retirement can retry cleanup even after a lost acknowledgement.
	for _, key := range row.ArtifactKeys {
		if key != row.BaseKey && key != state.ExecutionEnvironmentLayerKey(row) && key != row.BaseKey+".digest" && key != row.BaseKey+".content" && key != wire.ScanKeyForBaseKey(row.BaseKey) {
			_ = backend.Delete(ctx, key)
		}
	}
	return nil
}

func environmentImageDiagnostic(ctx context.Context, store any, row state.ExecutionEnvironment, stage, code, message string) error {
	s, ok := store.(state.ExecutionEnvironmentDiagnosticStore)
	if !ok {
		return nil
	}
	writeCtx, cancel := context.WithTimeout(ctx, api.ExecutionEnvironmentDiagnosticWriteTimeoutSeconds*time.Second)
	defer cancel()
	return s.AppendExecutionEnvironmentEvent(writeCtx, row, api.ExecutionEnvironmentEvent{Stage: stage, Code: code, Message: message})
}
