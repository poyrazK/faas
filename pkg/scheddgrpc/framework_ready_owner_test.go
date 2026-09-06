package scheddgrpc

import (
	"context"
	"testing"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFrameworkReadyRejectsOtherOwner(t *testing.T) {
	res := &fakeResolver{insts: map[string]state.Instance{"instance": {ID: "instance", AppID: "app"}}, apps: map[string]state.App{"app": {ID: "app", NodeID: "other"}}}
	s := &Server{owner: "owner", resolver: res}
	if _, err := s.ReportFrameworkReady(context.Background(), &scheddpb.FrameworkReadyReport{InstanceId: "instance"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("wrong owner: %v", err)
	}
}
