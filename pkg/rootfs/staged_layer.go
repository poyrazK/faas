package rootfs

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/storage"
)

// PublishStagedLayer mkfs-es a populated staging tree into a sizeMB ext4
// labelled label, then publishes and signs it at key the way a base is
// published. Platform-owned layers use it, such as the agent run layer
// (ADR-20261027090010 §2). The caller owns staging, including its ownership.
func (b *Builder) PublishStagedLayer(ctx context.Context, be storage.StorageBackend, key, staging, label string, sizeMB int) (ArtifactIdentity, error) {
	if be == nil || key == "" || staging == "" || label == "" || sizeMB < MinLayerMB {
		return ArtifactIdentity{}, errors.New("rootfs: incomplete staged layer request")
	}
	tmpPath, err := createBaseTemp("faas-layer-mkfs-*.ext4")
	if err != nil {
		return ArtifactIdentity{}, err
	}
	defer func() { _ = os.Remove(tmpPath) }()
	argv := MkfsCommand(staging, tmpPath, sizeMB)
	argv[3] = label
	if err := b.run.Run(ctx, argv); err != nil {
		return ArtifactIdentity{}, fmt.Errorf("rootfs: layer mkfs: %w", err)
	}
	return b.publishBaseFile(ctx, be, key, tmpPath)
}
