package imaged

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// baseGuestInitPatcher is implemented by *rootfs.Builder. Builders without
// it (most test doubles) always take the full rebuild.
type baseGuestInitPatcher interface {
	PatchBaseGuestInit(ctx context.Context, in rootfs.BasePatchInput) (rootfs.BaseBuildResult, error)
}

// The production builder must keep the fast path; without it every release
// silently rebuilds every base again.
var _ baseGuestInitPatcher = (*rootfs.Builder)(nil)

var guestInitDigestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// parseBaseDigestSidecarFields reads a digest sidecar without requiring a
// particular guest-init. guestInit and sourceRef are empty when the sidecar
// predates them.
func parseBaseDigestSidecarFields(have string) (configDigest, guestInit, sourceRef string, ok bool) {
	lines := strings.Split(strings.TrimSpace(have), "\n")
	if len(lines) < 2 || lines[1] != baseLayoutVersion {
		return "", "", "", false
	}
	parsed, err := oci.ParseReference("local.invalid/base@" + lines[0])
	if err != nil || parsed.Digest != lines[0] {
		return "", "", "", false
	}
	rest := lines[2:]
	if len(rest) > 0 && strings.HasPrefix(rest[0], "guest-init-sha256=") {
		guestInit = strings.TrimPrefix(rest[0], "guest-init-sha256=")
		if !guestInitDigestRE.MatchString(guestInit) {
			return "", "", "", false
		}
		rest = rest[1:]
	}
	if len(rest) > 0 {
		if len(rest) != 1 || !strings.HasPrefix(rest[0], baseSourceRefPrefix) {
			return "", "", "", false
		}
		sourceRef = strings.TrimPrefix(rest[0], baseSourceRefPrefix)
		if sourceRef == "" || strings.ContainsAny(sourceRef, "\r\n\x00") {
			return "", "", "", false
		}
	}
	return lines[0], guestInit, sourceRef, true
}

// tryPatchBaseGuestInit refreshes PID 1 in the staged base instead of
// rebuilding it, when guest-init is the only input that changed.
//
// The OCI bases are pinned by digest and change rarely, but guest-init is
// rebuilt from this repository on every release, so its digest in the
// freshness sidecar invalidated every base on every rollout. A full rebuild
// re-pulls, re-extracts and re-mkfs-es each userland (about 230 s per node
// for the eight production bases) to change one file. The digest sidecar
// proves the staged artifact was built from wantDigest under the current
// layout, so swapping /sbin/init yields the artifact a rebuild would.
//
// Every failure returns false and the caller rebuilds from the layers. The
// patched artifact is published, validated, scanned and recorded exactly as
// a rebuilt one.
func (h *Handler) tryPatchBaseGuestInit(
	ctx context.Context,
	be storage.StorageBackend,
	ref, baseKey, digestKey, outImage, wantDigest, guestInitDigest, sidecar string,
) (BaseStageResult, bool) {
	patcher, ok := h.builder.(baseGuestInitPatcher)
	if !ok || guestInitDigest == "" || sidecar == "" {
		return BaseStageResult{}, false
	}
	configDigest, previousInit, sourceRef, ok := parseBaseDigestSidecarFields(sidecar)
	if !ok || configDigest != wantDigest || previousInit == "" || previousInit == guestInitDigest ||
		(sourceRef != "" && sourceRef != ref) {
		return BaseStageResult{}, false
	}
	resolver, ok := be.(storage.LocalPathResolver)
	if !ok {
		return BaseStageResult{}, false
	}
	source, local, err := resolver.LocalPath(baseKey)
	if err != nil || !local || source == "" {
		return BaseStageResult{}, false
	}
	if err := h.validateExistingBaseArtifact(ctx, be, baseKey); err != nil {
		h.log.Warn("imaged: staged base failed validation; rebuilding instead of patching guest-init",
			"key", baseKey, "err", err)
		return BaseStageResult{}, false
	}

	start := time.Now()
	res, err := patcher.PatchBaseGuestInit(ctx, rootfs.BasePatchInput{
		SourceImage:     source,
		GuestInitPath:   h.guestInitPath,
		GuestInitSHA256: guestInitDigest,
		Storage:         be,
		StorageKey:      baseKey,
	})
	if err != nil {
		h.log.Warn("imaged: guest-init patch declined; rebuilding base",
			"ref", ref, "key", baseKey, "err", err)
		return BaseStageResult{}, false
	}
	if err := h.validateBaseArtifact(ctx, be, baseKey); err != nil {
		// The digest sidecar still names the previous guest-init, so the
		// rebuild below replaces this artifact.
		h.log.Warn("imaged: patched base failed validation; rebuilding base",
			"ref", ref, "key", baseKey, "err", err)
		return BaseStageResult{}, false
	}
	if err := h.writeBaseContentSidecar(ctx, be, baseKey); err != nil {
		h.log.Warn("imaged: write base content sidecar", "key", baseKey, "err", err)
	}
	if err := h.writeBaseDigestSidecar(ctx, be, digestKey, wantDigest, guestInitDigest, ref); err != nil {
		h.log.Warn("imaged: write base digest sidecar", "err", err)
	}
	if err := h.writeScanSidecar(ctx, baseKey, digestKey, ref, outImage); err != nil {
		h.log.Warn("imaged: write grype scan sidecar",
			"key", wire.ScanKeyForBaseKey(baseKey), "err", err)
	}
	h.log.Info("imaged: patched base guest-init",
		"ref", ref, "key", res.ImageKey, "size_bytes", res.SizeBytes,
		"digest", wantDigest, "previous_guest_init", previousInit, "guest_init", guestInitDigest,
		"duration_ms", time.Since(start).Milliseconds())
	return BaseStageResult{
		OutImage:     outImage,
		StorageKey:   res.ImageKey,
		ConfigDigest: wantDigest,
	}, true
}
