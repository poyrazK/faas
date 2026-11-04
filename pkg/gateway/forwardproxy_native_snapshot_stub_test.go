// adr: 20261022184951
package gateway

import (
	"context"
	"errors"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
)

// Only imaged retires native runtime snapshot objects. A forwarding client
// that reaches this has crossed an owner, so it fails instead of a no-op.
func (*stubVmmdClient) RetireNativeRuntimeSnapshotObject(context.Context, *vmmdpb.RetireNativeRuntimeSnapshotObjectRequest, ...grpc.CallOption) (*vmmdpb.RetireNativeRuntimeSnapshotObjectResponse, error) {
	return nil, errors.New("unexpected native runtime snapshot retirement in forwarding client")
}
