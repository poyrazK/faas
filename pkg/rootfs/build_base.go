package rootfs

import (
	"context"
	"fmt"
	"io"
	"os"
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
type BaseBuildInput struct {
	// Layers are ALL layers of the base OCI image, bottom-to-top,
	// gzip-compressed (same wire shape as Builders expect from
	// oci.RegistryClient.PullBlob).
	Layers []io.Reader
	// OutImage is the resulting ext4 — e.g. /srv/fc/base/builder-base.ext4.
	OutImage string
}

// BaseBuildResult reports the produced base image.
type BaseBuildResult struct {
	ImagePath string
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
	if in.OutImage == "" {
		return BaseBuildResult{}, fmt.Errorf("rootfs: BuildBase: empty OutImage")
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

	if err := b.run.Run(ctx, MkfsCommand(staging, in.OutImage, PaddedSizeMB(content))); err != nil {
		return BaseBuildResult{}, fmt.Errorf("rootfs: base mkfs: %w", err)
	}
	return BaseBuildResult{ImagePath: in.OutImage, SizeBytes: content}, nil
}
