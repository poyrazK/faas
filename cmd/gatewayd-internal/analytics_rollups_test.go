package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeAnalyticsRollupClient struct {
	err error
	got *apidpb.AddRequestAnalyticsRollupsRequest
}

func (c *fakeAnalyticsRollupClient) AddRequestAnalyticsRollups(_ context.Context, req *apidpb.AddRequestAnalyticsRollupsRequest) (*apidpb.AddRequestAnalyticsRollupsResponse, error) {
	c.got = req
	if c.err != nil {
		return nil, c.err
	}
	return &apidpb.AddRequestAnalyticsRollupsResponse{}, nil
}

func TestShipAnalyticsRollupsMapsApidAnswers(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"applied", nil, nil},
		{"unimplemented", status.Error(codes.Unimplemented, "old apid"), gateway.ErrRequestAnalyticsRollupsUnsupported},
		{"invalid", status.Error(codes.InvalidArgument, "bad row"), gateway.ErrRequestAnalyticsRollupBatchRefused},
		{"unavailable", status.Error(codes.Unavailable, "busy"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cli := &fakeAnalyticsRollupClient{err: tt.err}
			batch := uuid.New()
			row := state.RequestAnalyticsRollup{AppID: uuid.New(), AccountID: uuid.New(), BucketStart: time.Unix(1_800_000_000, 0),
				DimensionKind: "route", Dimension: "GET /", Method: "GET", Status: 200, Requests: 2}
			row.LatencyHist[3] = 2
			err := shipAnalyticsRollups(t.Context(), cli, batch, "node-a", []state.RequestAnalyticsRollup{row})
			switch {
			case tt.err == nil && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case tt.want != nil && !errors.Is(err, tt.want):
				t.Fatalf("err = %v, want %v", err, tt.want)
			case tt.err != nil && tt.want == nil && (err == nil || errors.Is(err, gateway.ErrRequestAnalyticsRollupsUnsupported) || errors.Is(err, gateway.ErrRequestAnalyticsRollupBatchRefused)):
				t.Fatalf("err = %v, want a retryable error", err)
			}
			got := cli.got
			if got.GetBatchId() != batch.String() || got.GetNodeId() != "node-a" || len(got.GetRows()) != 1 ||
				got.GetRows()[0].GetRequests() != 2 || got.GetRows()[0].GetLatencyHist()[3] != 2 || got.GetRows()[0].GetBucketStartUnix() != 1_800_000_000 {
				t.Fatalf("request = %+v", got)
			}
		})
	}
}
