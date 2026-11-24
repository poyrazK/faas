package imaged

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// SandboxDesktopImageEnv names the digest-pinned linux/amd64 sandbox desktop
// image (images/sandbox-desktop.Dockerfile). The agent pool's imaged stages it
// once as the platform desktop drive every account's desktop boots, and apid
// reads the same variable (ADR-20261124075005 §3a).
const SandboxDesktopImageEnv = "FAAS_SANDBOX_DESKTOP_IMAGE"

// sandboxDesktopPlan sizes the desktop drive: the image is the platform's,
// so it takes the largest full-rootfs cap, not a customer plan's.
const sandboxDesktopPlan = api.PlanScale

// EnsureSandboxDesktopDrive publishes the platform desktop drive when
// SandboxDesktopImageEnv is set and the shared store does not hold it yet.
// The key names the image's manifest digest, so an existing publication is
// current and a new image takes a new key. It returns the key ("" when the
// variable is unset) and whether it was already published.
func (h *Handler) EnsureSandboxDesktopDrive(ctx context.Context, arch string, envLookup func(string) string) (string, bool, error) {
	if envLookup == nil {
		return "", false, errors.New("imaged: sandbox desktop environment lookup is required")
	}
	ref := strings.TrimSpace(envLookup(SandboxDesktopImageEnv))
	if ref == "" {
		return "", false, nil
	}
	if arch != "amd64" {
		return "", false, errors.New("imaged: the sandbox desktop image requires amd64")
	}
	key := storagekey.SandboxDesktopImageForRef(ref)
	if key == "" {
		return "", false, fmt.Errorf("imaged: %s must be pinned by sha256 digest; got %q", SandboxDesktopImageEnv, ref)
	}
	be, err := h.storageFor()
	if err != nil {
		return "", false, err
	}
	exists, supported, err := storage.Exists(ctx, be, key)
	if err != nil {
		return "", false, fmt.Errorf("imaged: check sandbox desktop drive %s: %w", key, err)
	}
	if !supported {
		if rc, getErr := be.Get(ctx, key); getErr == nil {
			_ = rc.Close()
			exists = true
		}
	}
	if exists {
		return key, true, nil
	}
	if err := h.buildSandboxDesktopDrive(ctx, be, ref, key); err != nil {
		return "", false, err
	}
	return key, false, nil
}

// buildSandboxDesktopDrive pulls ref by its digest and publishes it at key
// as a full-rootfs drive, as a job image is materialized.
func (h *Handler) buildSandboxDesktopDrive(ctx context.Context, be storage.StorageBackend, ref, key string) error {
	mp, ok := h.oci.(oci.ManifestPuller)
	if !ok {
		return fmt.Errorf("imaged: OCI puller %T cannot pull the sandbox desktop image", h.oci)
	}
	_, want, _ := strings.Cut(ref, "@")
	digest, err := pullDigestWithAuth(ctx, h.oci, ref, nil)
	if err != nil {
		return fmt.Errorf("imaged: resolve sandbox desktop image: %w", err)
	}
	if digest != want {
		return fmt.Errorf("imaged: sandbox desktop image resolved to %s, not its pinned %s", digest, want)
	}
	manifest, err := pullManifestWithAuth(ctx, mp, ref, nil)
	if err != nil {
		return fmt.Errorf("imaged: pull sandbox desktop manifest: %w", err)
	}
	config, err := pullImageConfigWithAuth(ctx, h.oci, ref, nil)
	if err != nil {
		return fmt.Errorf("imaged: pull sandbox desktop config: %w", err)
	}
	appManifest, err := manifestFromImageConfig(config)
	if err != nil {
		return fmt.Errorf("imaged: sandbox desktop image config: %w", err)
	}
	repo := repoWithHost(ref)
	if repo == "" {
		return errors.New("imaged: cannot derive the sandbox desktop image's repository")
	}
	readers := make([]io.Reader, 0, len(manifest.Layers))
	closers := make([]io.Closer, 0, len(manifest.Layers))
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()
	for _, layer := range manifest.Layers {
		rc, err := pullBlobWithAuth(ctx, mp, repo, layer.Digest, nil)
		if err != nil {
			return fmt.Errorf("imaged: pull sandbox desktop layer %s: %w", layer.Digest, err)
		}
		closers = append(closers, rc)
		readers = append(readers, rc)
	}
	res, err := h.builder.BuildFullRootfs(ctx, rootfs.BuildFullRootfsInput{Layers: readers, Manifest: appManifest,
		GuestInitPath: h.guestInitPath, Plan: sandboxDesktopPlan, Storage: be, StorageKey: key})
	if err != nil {
		h.cleanupJobMaterializationArtifact(ctx, be, key)
		return fmt.Errorf("imaged: build sandbox desktop drive: %w", err)
	}
	h.log.Info("imaged sandbox desktop drive published", "key", key, "digest", digest, "bytes", res.ArtifactBytes)
	return nil
}
