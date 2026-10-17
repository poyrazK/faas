package gateway

import (
	"context"
	"errors"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
)

// Gateway forwarding never restores app-task VMs. Keep these test clients
// explicit so accidental ownership bypasses fail instead of becoming no-ops.
func (*stubVmmdClient) RestoreRehearsalAppTask(context.Context, *vmmdpb.RestoreAppTaskRequest, ...grpc.CallOption) (*vmmdpb.RestoreAppTaskResponse, error) {
	return nil, errors.New("unexpected rehearsal restore in forwarding client")
}

func (*activityPanicClient) RestoreRehearsalAppTask(context.Context, *vmmdpb.RestoreAppTaskRequest, ...grpc.CallOption) (*vmmdpb.RestoreAppTaskResponse, error) {
	return nil, errors.New("unexpected rehearsal restore in activity client")
}

func (*upgradeWireClient) RestoreRehearsalAppTask(context.Context, *vmmdpb.RestoreAppTaskRequest, ...grpc.CallOption) (*vmmdpb.RestoreAppTaskResponse, error) {
	return nil, errors.New("unexpected rehearsal restore in upgrade client")
}
