package vmmdgrpc_test

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUpdateAppCPULimitRoutesAndValidates(t *testing.T) {
	var gotApp string
	var gotRevision int64
	var gotCPU int
	fake := &fakeVMM{updateCPULimitFn: func(_ context.Context, appID string, revision int64, cpuMillicores int) error {
		gotApp, gotRevision, gotCPU = appID, revision, cpuMillicores
		return nil
	}}
	client, _ := newServer(t, fake)

	ack, err := client.UpdateAppCPULimit(context.Background(), &vmmdpb.UpdateAppCPULimitRequest{
		AppId: "app-123", CpuMillicores: 500, Revision: 7,
	})
	if err != nil {
		t.Fatalf("UpdateAppCPULimit: %v", err)
	}
	if ack == nil || gotApp != "app-123" || gotRevision != 7 || gotCPU != 500 {
		t.Fatalf("ack=%+v routed app=%q revision=%d cpu=%d; want app-123 revision 7 at 500m", ack, gotApp, gotRevision, gotCPU)
	}

	for _, request := range []*vmmdpb.UpdateAppCPULimitRequest{
		{CpuMillicores: 500},
		{AppId: "app-123", CpuMillicores: 333},
		{AppId: "app-123", CpuMillicores: 500, Revision: -1},
		{AppId: "app-123", CpuMillicores: 500, Revision: 0},
	} {
		if _, err := client.UpdateAppCPULimit(context.Background(), request); status.Code(err) != codes.InvalidArgument {
			t.Errorf("UpdateAppCPULimit(%+v) code=%s, want %s", request, status.Code(err), codes.InvalidArgument)
		}
	}
}
