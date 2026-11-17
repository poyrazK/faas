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

// Sign implements pkg/rootfs.Signer. It hashes the stored layer; SignDigest
// takes the digest the publisher already computed.
func (c *Client) Sign(ctx context.Context, layerKey, sigKey string) error {
	return c.sign(ctx, layerKey, sigKey, func() (string, int64, error) {
		return cosign.DigestObject(ctx, c.objects, layerKey, nil)
	})
}

// SignDigest signs the layer at layerKey given the hex SHA-256 and length of
// the bytes its publisher streamed to storage, without reading it back.
func (c *Client) SignDigest(ctx context.Context, layerKey, sigKey, digestHex string, bytes int64) error {
	if _, ok := DeploymentOfLayerKey(layerKey); !ok {
		if ds, ok := c.local.(localDigestSigner); ok {
			return ds.SignDigest(ctx, layerKey, sigKey, digestHex, bytes)
		}
	}
	return c.sign(ctx, layerKey, sigKey, func() (string, int64, error) { return digestHex, bytes, nil })
}

// localDigestSigner is a local signer that also signs a known digest.
type localDigestSigner interface {
	SignDigest(ctx context.Context, layerKey, sigKey, digestHex string, bytes int64) error
}

func (c *Client) sign(ctx context.Context, layerKey, sigKey string, digestOf func() (string, int64, error)) error {
	deploymentID, ok := DeploymentOfLayerKey(layerKey)
	if !ok {
		// Runtime bases and the agent layer: no verifier checks their
		// signature, so a node without the signing key publishes them
		// unsigned (ADR-20261109170000 slice 3).
		if c.local == nil {
			return nil
		}
		return c.local.Sign(ctx, layerKey, sigKey)
	}
	if sigKey != cosign.SigKeyFor(layerKey) {
		return fmt.Errorf("signd: signd writes %q's signature to %q, not %q", layerKey, cosign.SigKeyFor(layerKey), sigKey)
	}
	digest, n, err := digestOf()
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
