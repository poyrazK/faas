// adr: 20261017201005
package gateway_test

import (
	"context"
	"errors"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
)

func (*fakeVmmdClient) RestoreRehearsalAppTask(context.Context, *vmmdpb.RestoreAppTaskRequest, ...grpc.CallOption) (*vmmdpb.RestoreAppTaskResponse, error) {
	return nil, errors.New("unexpected rehearsal restore in forwarding client")
}
