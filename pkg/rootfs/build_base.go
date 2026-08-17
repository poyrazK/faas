package rootfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/storage"
)

// BaseBuildInput is one base-image provisioning run.
//
// imaged startup calls BuildBase once per box lifetime to convert
// ghcr.io/poyrazK/builder-base:latest (or the configured override) into
// /srv/fc/base/builder-base.ext4 — the read-only drive0 used by builder
// microVMs (spec §4.6, two-drive scheme). The base image already contains
// guest-init + /usr/local/bin/railpack + buildkit, baked into its own
// layers; BuildBase must therefore NOT re-inject guest-init/app.json over
// those layers. It's the inverse of Builder.Build: every layer pulled, no
// contract injection, no plan cap.
//
// Like BuildInput, the produced ext4 is published via Storage + StorageKey
// (production) or OutImage (legacy / integration test). Exactly one must
// be set — see Builder.validateOutputTarget for the validation rules.
type BaseBuildInput struct {
	// Layers are ALL layers of the base OCI image, bottom-to-top,
	// gzip-compressed (same wire shape as Builders expect from
	// oci.RegistryClient.PullBlob). Empty when BuildBaseFromStaging
	// is invoked with a pre-populated staging dir (ADR-053 parent-ref
	// path: the staging dir is filled by imaged's vmmd-mounted parent
	// + delta layer apply loop, not by BuildBase itself).
	Layers []io.Reader
	// Storage is the artifact backend the produced ext4 is Put into.
	// Mutually exclusive with OutImage.
	Storage storage.StorageBackend
	// StorageKey is the key the produced ext4 is published under, e.g.
	// "base/<runtime>.ext4". Mutually exclusive with OutImage.
	StorageKey string
	// OutImage is the legacy on-disk target, e.g.
	// "/srv/fc/base/builder-base.ext4". Kept for the integration test;
	// production wiring uses Storage + StorageKey.
	OutImage string
}

// BaseBuildResult reports the produced base image.
type BaseBuildResult struct {
	ImageKey  string // set when Storage + StorageKey was used
	ImagePath string // set when OutImage was used
	SizeBytes int64
}

// MkdirBaseStaging creates a fresh temp dir for a base-image staging
// tree. Exported (ADR-053) so pkg/imaged's parent-ref path can
// pre-create the dir, populate it via cp -a from the vmmd-mounted
// parent ext4 + a delta layer apply loop, then hand it to
// BuildBaseFromStaging for mkfs. The dir is NOT auto-removed: callers
// running BuildBaseFromStaging rely on this function's sibling-temp
// pattern (mirrors publishExt4 / publishBaseExt4). Callers that want
// automatic cleanup should defer os.RemoveAll on the returned path.
//
// Staging root: when FAAS_BASE_STAGING_ROOT is set, the staging dir
// is created under that root (MkdirAll'd with 0755 so a fresh
// /dev/shm/faas-base-staging works on a reboot); otherwise the
// default `os.MkdirTemp("", ...)` is used. The cd-controlplane EX44
// ships the env var pointing at /dev/shm/faas-base-staging in
// deploy/systemd/faas-imaged.service — host /tmp is ext4 there and the
// kernel rejects overlay mounts whose upper fs doesn't support
// tmpfile with `overlayfs: upper fs does not support tmpfile`
// (parent-ref overlay mount crash-loop, 2026-08-04 → 2026-08-05;
// every cd-controlplane deploy failed for 24h). Tests stay portable
// on macOS dev units by leaving the env var unset.
func MkdirBaseStaging() (string, error) {
	return mkdirBaseTemp("FAAS_BASE_STAGING_ROOT")
}

func MkdirBaseExtraction() (string, error) {
	return mkdirBaseTemp("FAAS_BASE_EXTRACT_ROOT")
}

func mkdirBaseTemp(envKey string) (string, error) {
	root := os.Getenv(envKey)
	if root == "" {
		return os.MkdirTemp("", "faas-base-*")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("rootfs: staging root %s: %w", root, err)
	}
	return os.MkdirTemp(root, "faas-base-*")
}

// BuildBase assembles a base-image ext4 from the supplied OCI layers.
//
// Differences from Builder.Build (this is the inverse per §4.6):
//
//   - ALL layers are applied (no LayersAboveBase filter). The base is the
//     root, not a delta.
//   - No /etc/faas/app.json is injected (a base has no app).
//   - No guest-init is re-injected (the base image already has its own).
//   - No plan / app-layer cap (the base is shared, not per-app).
//
// On error the staging dir is removed before returning. The caller owns
// the layer readers and is responsible for closing them — mirroring the
// Builder.Build contract (cmd/imaged closes them above the call).
func (b *Builder) BuildBase(ctx context.Context, in BaseBuildInput) (BaseBuildResult, error) {
	if err := validateBaseOutputTarget(in); err != nil {
		return BaseBuildResult{}, err
	}
	if len(in.Layers) == 0 {
		return BaseBuildResult{}, fmt.Errorf("rootfs: BuildBase: no layers")
	}

	staging, err := MkdirBaseExtraction()
	if err != nil {
		return BaseBuildResult{}, fmt.Errorf("rootfs: staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	for i, layer := range in.Layers {
		if err := ApplyLayerGz(staging, layer); err != nil {
			return BaseBuildResult{}, fmt.Errorf("rootfs: apply base layer %d: %w", i, err)
		}
	}

	return b.buildBaseFromStaging(ctx, staging, in)
}

// BuildBaseFromStaging (ADR-053) is the seam used by the parent-ref
// staging path: imaged pre-populates `staging` with the parent's tree
// (cp -a from a vmmd loopback mount of the parent ext4) + the delta
// OCI layers, then calls BuildBaseFromStaging to mkfs + publish.
//
// `staging` MUST be a path returned by MkdirBaseStaging (or otherwise
// outside the published ext4's directory, to avoid mkfs's -d flag
// copying its own growing output back into itself — see
// publishBaseExt4's sibling-temp pattern, this comment is mirrored
// there).
//
// `in.Storage` / `in.StorageKey` / `in.OutImage` follow the same
// exclusive-or rules as BaseBuildInput. The function does NOT touch
// in.Layers (those are zero in the parent-ref path).
//
// Cleanup of `staging` is the caller's responsibility: BuildBaseFromStaging
// does not RemoveAll it (matches the "caller owns the staging tree"
// contract the parent-ref path needs — imaged cleans up after publishing).
func (b *Builder) BuildBaseFromStaging(ctx context.Context, staging string, in BaseBuildInput) (BaseBuildResult, error) {
	if err := validateBaseOutputTarget(in); err != nil {
		return BaseBuildResult{}, err
	}
	if staging == "" {
		return BaseBuildResult{}, fmt.Errorf("rootfs: BuildBaseFromStaging: empty staging dir")
	}
	if _, err := os.Stat(staging); err != nil {
		return BaseBuildResult{}, fmt.Errorf("rootfs: BuildBaseFromStaging: stat staging: %w", err)
	}
	return b.buildBaseFromStaging(ctx, staging, in)
}

// buildBaseFromStaging is the shared tail of BuildBase (apply-all
// path) and BuildBaseFromStaging (ADR-053 parent-ref path): measure,
// mkfs, publish. Extracted so both paths share the same padding +
// signing + storage Put semantics.
//
// Sizing uses BasePaddedSizeMB (small-file-ratio-aware) rather than
// PaddedSizeMB because base images — particularly the Debian 12-slim
// parent (ADR-053) — are dominated by tiny files like
// /usr/share/zoneinfo/*. The 10 % slack PaddedSizeMB adds is far too
// tight for that shape; the previous formula left imaged's
// base-debian-parent staging ENOSPC-failing inside mkfs.ext4 -d (EX44
// run 30656504195, 2026-07-31). InspectStaging walks the tree once;
// the cost is dwarfed by the mkfs that follows.
func (b *Builder) buildBaseFromStaging(ctx context.Context, staging string, in BaseBuildInput) (BaseBuildResult, error) {
	stats, err := InspectStaging(staging)
	if err != nil {
		return BaseBuildResult{}, err
	}

	if err := b.publishBaseExt4(ctx, in, staging, BasePaddedSizeMB(stats.ContentBytes, stats.SmallRatio)); err != nil {
		return BaseBuildResult{}, err
	}

	res := BaseBuildResult{SizeBytes: stats.ContentBytes}
	if in.OutImage != "" {
		res.ImagePath = in.OutImage
	} else {
		res.ImageKey = in.StorageKey
	}
	return res, nil
}

// publishBaseExt4 mirrors Builder.publishExt4 but writes via the base
// path: mkfs into a tmp file, Put under StorageKey. The legacy OutImage
// path mkfs-es directly into OutImage (matches the pre-#96 behaviour).
func (b *Builder) publishBaseExt4(ctx context.Context, in BaseBuildInput, staging string, sizeMB int) error {
	if in.OutImage != "" {
		if err := b.run.Run(ctx, MkfsCommand(staging, in.OutImage, sizeMB)); err != nil {
			return fmt.Errorf("rootfs: base mkfs: %w", err)
		}
		return nil
	}
	// mkfs's -d flag populates the new image from `staging`; the output file
	// must therefore live outside that tree and its tmpfs staging budget.
	tmpRoot := os.Getenv("FAAS_BASE_TMP_ROOT")
	if tmpRoot == "" {
		tmpRoot = os.TempDir()
	}
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return fmt.Errorf("rootfs: create base tmp root: %w", err)
	}
	tmp, err := os.CreateTemp(tmpRoot, "faas-base-mkfs-*.ext4")
	if err != nil {
		return fmt.Errorf("rootfs: create base tmp ext4: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rootfs: close base tmp ext4: %w", err)
	}
	if err := b.run.Run(ctx, MkfsCommand(staging, tmpPath, sizeMB)); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rootfs: base mkfs: %w", err)
	}
	// nolint:forbidigo // tmpPath is from os.MkdirTemp at the top of
	// this function — a daemon-internal scratch file the builder just
	// wrote via MkfsCommand. Not a customer path.
	f, err := os.Open(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rootfs: open base mkfs output: %w", err)
	}
	defer func() { _ = f.Close(); _ = os.Remove(tmpPath) }()
	if err := in.Storage.Put(ctx, in.StorageKey, f); err != nil {
		return fmt.Errorf("rootfs: publish base %q: %w", in.StorageKey, err)
	}
	// ADR-038: mirror publishExt4's sign call. The base sig lives
	// at sigs/<StorageKey>.sig — same convention as the app-layer
	// path; schedd's verify side derives the sig key from the
	// layer key the same way (sigs/<layerKey>.sig). imaged's
	// startup restages + signs the base on first run if no sig
	// exists (see pkg/imaged/base_stage.go); app layers re-sign on
	// the next deploy.
	if b.signer != nil {
		sigKey := "sigs/" + in.StorageKey + ".sig"
		if err := b.signer.Sign(ctx, in.StorageKey, sigKey); err != nil {
			return fmt.Errorf("rootfs: sign base %q: %w", in.StorageKey, err)
		}
	}
	return nil
}

// validateBaseOutputTarget enforces the same exclusive-or rule as
// BuildInput.validateOutputTarget. The two helpers live separately
// because the input structs differ and a single generic helper would
// need a shared interface that adds nothing for the call sites.
func validateBaseOutputTarget(in BaseBuildInput) error {
	hasStorage := in.Storage != nil && in.StorageKey != ""
	hasOut := in.OutImage != ""
	switch {
	case hasStorage && hasOut:
		return errors.New("rootfs: BaseBuildInput has both Storage and OutImage set; pick one")
	case !hasStorage && !hasOut:
		return errors.New("rootfs: BaseBuildInput has neither Storage nor OutImage set")
	case hasStorage && in.StorageKey == "":
		return errors.New("rootfs: BaseBuildInput has Storage but empty StorageKey")
	case hasStorage && in.Storage == nil:
		return errors.New("rootfs: BaseBuildInput has StorageKey but nil Storage")
	}
	return nil
}
