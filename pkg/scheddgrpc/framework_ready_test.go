package scheddgrpc_test

import (
	"context"
	"errors"
	"testing"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReportFrameworkReady(t *testing.T) {
	var got string
	cli := newServer(t, &fakeEngine{frameworkReadyFn: func(_ context.Context, id string) error { got = id; return nil }})
	if _, err := cli.ReportFrameworkReady(context.Background(), &scheddpb.FrameworkReadyReport{InstanceId: "i-ready"}); err != nil || got != "i-ready" {
		t.Fatalf("id=%q err=%v", got, err)
	}
	if _, err := cli.ReportFrameworkReady(context.Background(), &scheddpb.FrameworkReadyReport{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty id: %v", err)
	}
	failing := newServer(t, &fakeEngine{frameworkReadyFn: func(context.Context, string) error { return errors.New("persist failed") }})
	if _, err := failing.ReportFrameworkReady(context.Background(), &scheddpb.FrameworkReadyReport{InstanceId: "i-ready"}); err == nil {
		t.Fatal("lost persistence error")
	}
}
