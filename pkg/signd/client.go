package signd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	signdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/signd/v1"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/storage"
)

// LayerSigner is pkg/rootfs.Signer: sign the published layer at layerKey
// into sigKey.
type LayerSigner interface {
	Sign(ctx context.Context, layerKey, sigKey string) error
}

// Client is imaged's signer when signd holds the key. It hashes each
// published app layer and asks signd to sign it; signd writes the
// signature. Other keys (runtime bases, the agent layer) go to local, the
// node's own signer, while the node still has the key.
type Client struct {
	rpc     signdpb.SigndClient
	objects storage.StorageBackend
	local   LayerSigner
}

// NewClient returns a signer over rpc. objects is the artifacts store the
// layers are read from. local may be nil.
func NewClient(rpc signdpb.SigndClient, objects storage.StorageBackend, local LayerSigner) *Client {
	return &Client{rpc: rpc, objects: objects, local: local}
}

// Sign implements pkg/rootfs.Signer.
func (c *Client) Sign(ctx context.Context, layerKey, sigKey string) error {
	deploymentID, ok := DeploymentOfLayerKey(layerKey)
	if !ok {
		if c.local == nil {
			return fmt.Errorf("signd: %q is not an app layer and this node has no local signer", layerKey)
		}
		return c.local.Sign(ctx, layerKey, sigKey)
	}
	if sigKey != cosign.SigKeyFor(layerKey) {
		return fmt.Errorf("signd: signd writes %q's signature to %q, not %q", layerKey, cosign.SigKeyFor(layerKey), sigKey)
	}
	digest, n, err := cosign.DigestObject(ctx, c.objects, layerKey, nil)
	if err != nil {
		return err
	}
	if _, err := c.rpc.SignArtifact(ctx, &signdpb.SignArtifactRequest{DeploymentId: deploymentID,
		StorageKey: layerKey, Digest: digest, Bytes: n}); err != nil {
		return fmt.Errorf("signd: sign %q: %w", layerKey, err)
	}
	return nil
}

var errNotAppLayer = errors.New("not an app layer key")

// DeploymentOfLayerKey returns the deployment an app or sidecar layer key
// names: apps/<slug>/<deployment id>.ext4 or
// apps/<slug>/<deployment id>-<sidecar>.ext4.
func DeploymentOfLayerKey(key string) (string, bool) {
	id, err := deploymentOfLayerKey(key)
	return id, err == nil
}

func deploymentOfLayerKey(key string) (string, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "apps" || parts[1] == "" || !strings.HasSuffix(parts[2], ".ext4") {
		return "", errNotAppLayer
	}
	name := strings.TrimSuffix(parts[2], ".ext4")
	const idLen = 36
	if len(name) < idLen || (len(name) > idLen && name[idLen] != '-') {
		return "", errNotAppLayer
	}
	id, err := uuid.Parse(name[:idLen])
	if err != nil {
		return "", errNotAppLayer
	}
	return id.String(), nil
}
