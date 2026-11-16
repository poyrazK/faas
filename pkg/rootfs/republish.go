package rootfs

// adr: 20261112170911

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/storage"
)

// PublishedLayer is an app layer this builder published earlier, held in a
// local file, with the identity computed when it was published.
type PublishedLayer struct {
	Path   string
	Digest string
	Bytes  int64
}

// Republish publishes the bytes of src under key the way a fresh build
// publishes its ext4: storage takes them through the identity reader and the
// builder signs key over the digest that reader computed. It refuses to sign,
// and removes what it put, when the bytes are not src's recorded identity, so
// a stale or corrupt source can only cost a rebuild.
func (b *Builder) Republish(ctx context.Context, be storage.StorageBackend, key string, src PublishedLayer) (ArtifactIdentity, error) {
	if be == nil || key == "" || src.Path == "" || src.Digest == "" || src.Bytes <= 0 {
		return ArtifactIdentity{}, errors.New("rootfs: republish needs storage, a key and a recorded source identity")
	}
	//nolint:forbidigo // src.Path is the storage backend's own local file for a key imaged published.
	f, err := os.Open(src.Path)
	if err != nil {
		return ArtifactIdentity{}, fmt.Errorf("rootfs: open republish source: %w", err)
	}
	defer func() { _ = f.Close() }()
	identity, err := publishArtifactIdentity(ctx, be, key, f)
	if err != nil {
		return ArtifactIdentity{}, fmt.Errorf("rootfs: republish %q: %w", key, err)
	}
	if identity != (ArtifactIdentity{Digest: src.Digest, Bytes: src.Bytes}) {
		_ = be.Delete(ctx, key)
		return ArtifactIdentity{}, fmt.Errorf("rootfs: republish %q: source is %s (%d bytes), recorded %s (%d bytes)",
			key, identity.Digest, identity.Bytes, src.Digest, src.Bytes)
	}
	if err := b.signPublished(ctx, key, identity); err != nil {
		return ArtifactIdentity{}, fmt.Errorf("rootfs: sign %q: %w", key, err)
	}
	return identity, nil
}
