// adr: 20261022184951
package gateway_test

import (
	"context"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Forwarding fixtures refuse imaged's native snapshot retirement.
func (*fakeVmmdClient) RetireNativeRuntimeSnapshotObject(context.Context, *vmmdpb.RetireNativeRuntimeSnapshotObjectRequest, ...grpc.CallOption) (*vmmdpb.RetireNativeRuntimeSnapshotObjectResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding does not retire native runtime snapshots")
}
