package imaged

// adr: 435

import (
	"context"
	"errors"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrVerifiedParentUnsupported reports a serving vmmd built before the
// verified parent RPC. A rolling pre-stage runs the candidate imaged against
// the node's still-serving vmmd, so the first release that needs the RPC meets
// a vmmd without it (H10-95). Callers never fall back to an unverified copy.
var ErrVerifiedParentUnsupported = errors.New("imaged: serving vmmd lacks verified parent materialization")

// VerifiedParentMaterializer is additive. A legacy client cannot acknowledge
// the verified capability by falling back to an unverified copy.
type VerifiedParentMaterializer interface {
	MaterializeVerifiedParentExt4(context.Context, imagechain.ParentMaterialization) (imagechain.ParentMaterialization, error)
}

var _ VerifiedParentMaterializer = (*VMMClient)(nil)

func (c *VMMClient) MaterializeVerifiedParentExt4(ctx context.Context, expected imagechain.ParentMaterialization) (imagechain.ParentMaterialization, error) {
	if c == nil || !expected.Valid() {
		return imagechain.ParentMaterialization{}, fmt.Errorf("imaged: invalid verified parent materialization")
	}
	cli, err := c.dial(ctx)
	if err != nil {
		return imagechain.ParentMaterialization{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultVMMDialTimeout)
	defer cancel()
	res, err := cli.MaterializeVerifiedParentExt4(callCtx, &vmmdpb.MaterializeVerifiedParentExt4Request{StorageKey: expected.Artifact.StorageKey, TargetDir: expected.TargetDir, ArtifactDigest: expected.Artifact.Digest, ArtifactBytes: expected.Artifact.Bytes})
	if status.Code(err) == codes.Unimplemented {
		return imagechain.ParentMaterialization{}, fmt.Errorf("%w: %w", ErrVerifiedParentUnsupported, err)
	}
	if err != nil {
		return imagechain.ParentMaterialization{}, fmt.Errorf("imaged: verified parent materialization: %w", err)
	}
	if res == nil || len(res.ProtoReflect().GetUnknown()) != 0 {
		return imagechain.ParentMaterialization{}, fmt.Errorf("imaged: unsupported parent receipt")
	}
	actual := imagechain.ParentMaterialization{Artifact: imagechain.BaseArtifact{StorageKey: res.StorageKey, Digest: res.ArtifactDigest, Bytes: res.ArtifactBytes}, TargetDir: res.TargetDir}
	if actual != expected || callCtx.Err() != nil {
		return imagechain.ParentMaterialization{}, fmt.Errorf("imaged: parent materialization receipt mismatch")
	}
	return actual, nil
}
