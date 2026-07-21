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
// ghcr.io/onebox-faas/builder-base:latest (or the configured override) into
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
	// oci.RegistryClient.PullBlob).
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

	staging, err := os.MkdirTemp("", "faas-base-*")
	if err != nil {
		return BaseBuildResult{}, fmt.Errorf("rootfs: staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	for i, layer := range in.Layers {
		if err := ApplyLayerGz(staging, layer); err != nil {
			return BaseBuildResult{}, fmt.Errorf("rootfs: apply base layer %d: %w", i, err)
		}
	}

	content, err := DirSize(staging)
	if err != nil {
		return BaseBuildResult{}, err
	}

	if err := b.publishBaseExt4(ctx, in, staging, PaddedSizeMB(content)); err != nil {
		return BaseBuildResult{}, err
	}

	res := BaseBuildResult{SizeBytes: content}
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
	tmp, err := os.CreateTemp(staging, "faas-base-mkfs-*.ext4")
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
