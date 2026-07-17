package imaged

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
)

// Base stage — imaged startup provisions the shared read-only drive0 used by
// builder microVMs (spec §4.6, two-drive scheme). At runtime, schedd hands
// the path of a staged base ext4 to vmmd when cold-booting a builder; that
// path must exist on disk before the first build is admitted.
//
// The conversion runs once per box lifetime, pinned by digest: when the
// remote OCI image's config digest hasn't changed since the last stage, the
// existing ext4 is trusted as-is. When it has, the layers are re-pulled and
// the ext4 is rewritten atomically (write to <out>.tmp, fsync, rename).

// BaseStageResult reports what EnsureBaseExt4 did. Skip=true means the
// existing file matched the remote digest and was left untouched.
type BaseStageResult struct {
	OutImage     string
	ConfigDigest string // empty when Skip
	Skipped      bool
}

// EnsureBaseExt4 guarantees outImage exists and reflects ref's current layers.
//
// ref is the OCI reference to pull the base image from (e.g. ghcr.io/onebox-
// faas/builder-base:latest). When ref is unchanged (matching the digest
// sidecar next to outImage), the existing file is left in place and Skipped
// is true. When ref has changed, the layers are pulled fresh and outImage is
// rewritten via a tempfile + rename so a partial stage never blocks cold
// boot.
//
// Requires the OCI puller to implement oci.ManifestPuller (registry v2
// streaming). Without it, EnsureBaseExt4 returns an error: M6+'s builderd
// only runs with full M6 wiring, and skipping silently would mask a real
// config error.
func (h *Handler) EnsureBaseExt4(ctx context.Context, ref, outImage string) (BaseStageResult, error) {
	if ref == "" {
		return BaseStageResult{}, errors.New("imaged: EnsureBaseExt4: empty ref")
	}
	if outImage == "" {
		return BaseStageResult{}, errors.New("imaged: EnsureBaseExt4: empty outImage")
	}

	mp, ok := h.oci.(oci.ManifestPuller)
	if !ok {
		return BaseStageResult{}, fmt.Errorf(
			"imaged: EnsureBaseExt4: puller %T does not implement ManifestPuller", h.oci)
	}

	manifest, err := mp.PullManifest(ctx, ref)
	if err != nil {
		return BaseStageResult{}, fmt.Errorf("imaged: pull base manifest %s: %w", ref, err)
	}

	// Idempotency: sidecar file at <outImage>.digest records the config
	// digest the staged ext4 was built from. When it matches, trust the
	// existing file — re-fetching tens of MB of layers on every daemon
	// restart would be wasteful and would also race the cold-boot path if
	// a build happened to land mid-stage.
	wantDigest := manifest.Config.Digest
	if haveDigest, err := os.ReadFile(outImage + ".digest"); err == nil {
		if string(haveDigest) == wantDigest {
			// File may legitimately be absent (e.g. /srv/fc not yet
			// mounted after boot) — fall through to staging in that case.
			if _, statErr := os.Stat(outImage); statErr == nil {
				return BaseStageResult{OutImage: outImage, ConfigDigest: wantDigest, Skipped: true}, nil
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(outImage), 0o755); err != nil {
		return BaseStageResult{}, fmt.Errorf("imaged: mkdir base dir: %w", err)
	}
	tmpOut := outImage + ".tmp"
	// Best-effort cleanup of a stale tmp file left by a previous crash.
	_ = os.Remove(tmpOut)

	// Pre-allocate the readers slice + closers so a partial pull on layer N
	// still closes layers 0..N-1. PullBlob streams the gzipped tarball; we
	// hand it to Builder.BuildBase which copies it through ApplyLayerGz.
	//
	// PullBlob takes a repo like "ghcr.io/onebox-faas/builder-base" — the
	// host:port + path with no tag/digest suffix. ParseReference splits
	// the ref for us (same parser the registry client uses internally).
	ociRef, err := oci.ParseReference(ref)
	if err != nil {
		return BaseStageResult{}, fmt.Errorf("imaged: parse base ref %q: %w", ref, err)
	}
	readers := make([]io.Reader, 0, len(manifest.Layers))
	closers := make([]io.ReadCloser, 0, len(manifest.Layers))
	for _, l := range manifest.Layers {
		body, err := mp.PullBlob(ctx, ociRef.Registry+"/"+ociRef.Repository, l.Digest)
		if err != nil {
			for _, c := range closers {
				_ = c.Close()
			}
			_ = os.Remove(tmpOut)
			return BaseStageResult{}, fmt.Errorf("imaged: pull base layer %s: %w", l.Digest, err)
		}
		readers = append(readers, body)
		closers = append(closers, body)
	}
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()

	res, err := h.builder.BuildBase(ctx, rootfs.BaseBuildInput{
		Layers:   readers,
		OutImage: tmpOut,
	})
	if err != nil {
		_ = os.Remove(tmpOut)
		return BaseStageResult{}, fmt.Errorf("imaged: build base ext4: %w", err)
	}

	// Atomic publish: rename tmp into place, then write the digest sidecar.
	// os.Rename is atomic on the same filesystem (ext4 in /srv/fc/base).
	if err := os.Rename(tmpOut, outImage); err != nil {
		_ = os.Remove(tmpOut)
		return BaseStageResult{}, fmt.Errorf("imaged: publish base ext4: %w", err)
	}
	if err := os.WriteFile(outImage+".digest", []byte(wantDigest), 0o644); err != nil {
		// Non-fatal — next run will re-pull layers, costing MB but not
		// correctness. Log so an operator notices a fs that doesn't
		// allow sidecar files.
		h.log.Warn("imaged: write base digest sidecar", "path", outImage+".digest", "err", err)
	}
	h.log.Info("imaged: staged builder base",
		"ref", ref, "out", res.ImagePath, "size_bytes", res.SizeBytes,
		"digest", wantDigest)

	return BaseStageResult{OutImage: outImage, ConfigDigest: wantDigest}, nil
}
