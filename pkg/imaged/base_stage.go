package imaged

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
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
// existing artifact matched the remote digest and was left untouched.
type BaseStageResult struct {
	// OutImage is the host-side path the staged ext4 lives at. Computed
	// from the routed StorageBackend's "snapshot" path so schedd's
	// drive0 lookup can pass it to vmmd verbatim (spec §4.6 two-drive
	// scheme). Empty when the LocalStorageBackend is not the canonical
	// /srv/fc root (a remote driver; callers downstream use the
	// StorageKey instead).
	OutImage string
	// StorageKey is the canonical key the staged ext4 was published
	// under, e.g. "base/runner-node22.ext4". Same value baseStageKey
	// took; reported back so callers don't have to recompute it.
	StorageKey   string
	ConfigDigest string // empty when Skip
	Skipped      bool
}

// EnsureBaseExt4 guarantees baseKey exists and reflects ref's current
// layers.
//
// ref is the OCI reference to pull the base image from (e.g. ghcr.io/onebox-
// faas/builder-base:latest). When ref's config digest matches the digest
// sidecar at digestKey, the existing artifact is left in place and Skipped
// is true. When ref has changed, the layers are pulled fresh and baseKey
// is republished via Storage.Put; storage.Put's internal temp+rename
// preserves the atomicity the legacy os.Rename provided.
//
// outImage is the resolved host path schedd hands to vmmd when cold-
// booting a builder against the local /srv/fc base. For a non-canonical
// storage root (a future remote driver) outImage is empty and schedd
// must read from baseKey via Get instead — handled by the cmd/vmmd
// caller.
//
// Requires the OCI puller to implement oci.ManifestPuller (registry v2
// streaming). Without it, EnsureBaseExt4 returns an error: M6+'s builderd
// only runs with full M6 wiring, and skipping silently would mask a real
// config error.
func (h *Handler) EnsureBaseExt4(ctx context.Context, ref, baseKey, digestKey, outImage string) (BaseStageResult, error) {
	if ref == "" {
		return BaseStageResult{}, errors.New("imaged: EnsureBaseExt4: empty ref")
	}
	if baseKey == "" {
		return BaseStageResult{}, errors.New("imaged: EnsureBaseExt4: empty baseKey")
	}
	if digestKey == "" {
		return BaseStageResult{}, errors.New("imaged: EnsureBaseExt4: empty digestKey")
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

	// Idempotency: digest sidecar at digestKey records the config digest
	// the staged ext4 was built from. When it matches, trust the
	// existing artifact — re-fetching tens of MB of layers on every daemon
	// restart would be wasteful and would also race the cold-boot path
	// if a build happened to land mid-stage.
	wantDigest := manifest.Config.Digest
	be := h.storageFor()
	if haveRC, err := be.Get(ctx, digestKey); err == nil {
		haveBytes, rerr := io.ReadAll(haveRC)
		_ = haveRC.Close()
		if rerr == nil && string(haveBytes) == wantDigest {
			if rc, err := be.Get(ctx, baseKey); err == nil {
				_, _ = io.Copy(io.Discard, rc)
				_ = rc.Close()
				return BaseStageResult{
					OutImage:     outImage,
					StorageKey:   baseKey,
					ConfigDigest: wantDigest,
					Skipped:      true,
				}, nil
			}
		}
	}

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
		Layers:     readers,
		Storage:    be,
		StorageKey: baseKey,
	})
	if err != nil {
		return BaseStageResult{}, fmt.Errorf("imaged: build base ext4: %w", err)
	}

	// Sidecar is a tiny text payload, but the storage backend is the
	// source of truth — Put under digestKey. Put's atomicity is per-key,
	// but since reads compare baseKey's existence first and digestKey
	// is only used as a decision oracle, a transient inconsistency
	// between the two is observable next run as "rebuild" rather than
	// "use half-published artifact".
	digestRC, err := openStringReader(wantDigest)
	if err != nil {
		return BaseStageResult{}, fmt.Errorf("imaged: open digest sidecar: %w", err)
	}
	if err := be.Put(ctx, digestKey, digestRC); err != nil {
		h.log.Warn("imaged: write base digest sidecar", "key", digestKey, "err", err)
	}
	h.log.Info("imaged: staged builder base",
		"ref", ref, "key", res.ImageKey, "size_bytes", res.SizeBytes,
		"digest", wantDigest)

	return BaseStageResult{
		OutImage:     outImage,
		StorageKey:   res.ImageKey,
		ConfigDigest: wantDigest,
	}, nil
}

// openStringReader returns an io.Reader for the supplied string. The
// helper exists so the digest sidecar Put has a content source without
// dragging in bytes.NewReader (which would also force a package-level
// bytes import that's only used here).
func openStringReader(s string) (io.Reader, error) {
	return stringReader(s), nil
}

type stringReaderImpl struct {
	s   string
	off int
}

func stringReader(s string) io.Reader { return &stringReaderImpl{s: s} }
func (r *stringReaderImpl) Read(p []byte) (int, error) {
	if r.off >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.off:])
	r.off += n
	return n, nil
}

// ensureBaseKeys computes the conventional (baseKey, digestKey) pair for
// a runtime. Lives alongside EnsureBaseExt4 so the cmd/imaged caller
// (and tests) don't have to repeat the storage key contract.
func ensureBaseKeys(runtime string) (baseKey, digestKey string) {
	return sched.BaseKey(runtime), sched.BaseDigestKey(runtime)
}
