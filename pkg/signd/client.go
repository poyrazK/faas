package signd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
	// wait replaces the retry backoff sleep in tests; nil sleeps.
	wait func(context.Context, time.Duration) error
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
	req := &signdpb.SignArtifactRequest{DeploymentId: deploymentID, StorageKey: layerKey, Digest: digest, Bytes: n}
	for attempt := 0; ; attempt++ {
		_, err = c.rpc.SignArtifact(ctx, req)
		if err == nil {
			return nil
		}
		// signd answers Unavailable for a store it could not reach and asks
		// for a retry; gRPC answers it while signd restarts. A release rolls
		// signd, and a deploy that published its layer then failed terminally
		// as deploy_failed (prod 2026-11-20 18:18, two acceptance deploys).
		// Signing the same digest again is idempotent.
		if status.Code(err) != codes.Unavailable || attempt >= len(signRetryBackoff) {
			return fmt.Errorf("signd: sign %q: %w", layerKey, err)
		}
		if waitErr := c.waitRetry(ctx, signRetryBackoff[attempt]); waitErr != nil {
			return fmt.Errorf("signd: sign %q: %w", layerKey, errors.Join(err, waitErr))
		}
	}
}

// signRetryBackoff spaces the retries of an Unavailable sign: about 30 s in
// all, longer than a signd restart.
var signRetryBackoff = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second}

func (c *Client) waitRetry(ctx context.Context, d time.Duration) error {
	if c.wait != nil {
		return c.wait(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
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
