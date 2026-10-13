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
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/storage"
)

// executionLayerSizeMB bounds the payload-free drive1 layer. Run scratch is
// the guest /tmp tmpfs, so this ext4 only carries the marker plus the
// overlay upper/work directories guest-init creates at boot.
const executionLayerSizeMB = 16

// ExecutionLayerResult reports one staged execution layer.
type ExecutionLayerResult struct {
	Profile      api.ExecutionProfile
	StorageKey   string
	MarkerDigest string
	Skipped      bool
}

// executionLayerProfiles are the profiles a layer is staged for. Staging a
// profile layer is cheap (16 MiB sparse ext4); whether the profile can run is
// still decided by its base image being present.
var executionLayerProfiles = []api.ExecutionProfile{api.ExecutionProfileStandard, api.ExecutionProfilePythonDataV1}

// ExecutionMarker returns the exact /etc/faas/execution.json bytes for a
// profile. guest-init selects its one-shot execution branch from this file.
func ExecutionMarker(profile api.ExecutionProfile) ([]byte, error) {
	marker := struct {
		Kind    string               `json:"kind"`
		Version int                  `json:"version"`
		Profile api.ExecutionProfile `json:"profile,omitempty"`
	}{Kind: "execution", Version: 1}
	if normalized := profile.Normalized(); normalized != api.ExecutionProfileStandard {
		marker.Profile = normalized
	}
	data, err := json.Marshal(marker)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// EnsureExecutionLayers stages the drive1 layer every disposable Run boots
// with, one per dependency profile. It is idempotent: a layer whose digest
// sidecar names the current marker is left in place. run executes mkfs.ext4;
// workDir is scratch space for the staging tree and image.
func (h *Handler) EnsureExecutionLayers(ctx context.Context, arch, workDir string, run rootfs.Runner) ([]ExecutionLayerResult, error) {
	if run == nil {
		return nil, errors.New("imaged: execution layers need a command runner")
	}
	be, err := h.storageFor()
	if err != nil {
		return nil, fmt.Errorf("imaged: storage for execution layers: %w", err)
	}
	results := make([]ExecutionLayerResult, 0, len(executionLayerProfiles))
	for _, profile := range executionLayerProfiles {
		result, err := ensureExecutionLayer(ctx, be, profile, arch, workDir, run)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func ensureExecutionLayer(ctx context.Context, be storage.StorageBackend, profile api.ExecutionProfile, arch, workDir string, run rootfs.Runner) (ExecutionLayerResult, error) {
	marker, err := ExecutionMarker(profile)
	if err != nil {
		return ExecutionLayerResult{}, fmt.Errorf("imaged: execution marker %s: %w", profile, err)
	}
	sum := sha256.Sum256(marker)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := sched.ExecutionLayerKeyForArch(string(profile), arch)
	digestKey := sched.ExecutionLayerDigestKeyForArch(string(profile), arch)
	result := ExecutionLayerResult{Profile: profile, StorageKey: key, MarkerDigest: digest}

	if current, err := readExecutionLayerDigest(ctx, be, digestKey); err == nil && current == digest {
		if rc, err := be.Get(ctx, key); err == nil {
			_ = rc.Close()
			result.Skipped = true
			return result, nil
		}
	}

	staging, err := os.MkdirTemp(workDir, "faas-execution-layer-")
	if err != nil {
		return ExecutionLayerResult{}, fmt.Errorf("imaged: execution layer staging: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	tree := filepath.Join(staging, "tree")
	markerDir := filepath.Join(tree, "upper", "etc", "faas")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		return ExecutionLayerResult{}, fmt.Errorf("imaged: execution layer tree: %w", err)
	}
	for _, dir := range []string{"work", "merged"} {
		if err := os.MkdirAll(filepath.Join(tree, dir), 0o755); err != nil {
			return ExecutionLayerResult{}, fmt.Errorf("imaged: execution layer tree: %w", err)
		}
	}
	if err := os.WriteFile(filepath.Join(markerDir, "execution.json"), marker, 0o644); err != nil {
		return ExecutionLayerResult{}, fmt.Errorf("imaged: write execution marker: %w", err)
	}
	image := filepath.Join(staging, "layer.ext4")
	if err := run.Run(ctx, rootfs.MkfsCommand(tree, image, executionLayerSizeMB)); err != nil {
		return ExecutionLayerResult{}, fmt.Errorf("imaged: mkfs execution layer %s: %w", profile, err)
	}
	f, err := os.Open(image) //nolint:forbidigo // image is the mkfs output in our own temp dir.
	if err != nil {
		return ExecutionLayerResult{}, fmt.Errorf("imaged: open execution layer: %w", err)
	}
	putErr := be.Put(ctx, key, f)
	_ = f.Close()
	if putErr != nil {
		return ExecutionLayerResult{}, fmt.Errorf("imaged: publish execution layer %q: %w", key, putErr)
	}
	// The sidecar is written last so a crash mid-publish is re-staged.
	if err := be.Put(ctx, digestKey, strings.NewReader(digest+"\n")); err != nil {
		return ExecutionLayerResult{}, fmt.Errorf("imaged: write execution layer digest %q: %w", digestKey, err)
	}
	return result, nil
}

func readExecutionLayerDigest(ctx context.Context, be storage.StorageBackend, key string) (string, error) {
	rc, err := be.Get(ctx, key)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, 256))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
