package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/gateway"
)

type instanceRouteClient interface {
	AddInstanceRouteMinutes(ctx context.Context, req *apidpb.AddInstanceRouteMinutesRequest) (*apidpb.AddInstanceRouteMinutesResponse, error)
}

// newInstanceRouteShipper sends per-instance route minutes to apid
// (ADR-20261121170000) over a client of its own, dialled on first use.
func newInstanceRouteShipper(target string, tlsCfg *tls.Config, tlsErr error, nodeID string) gateway.InstanceRouteShipFn {
	var mu sync.Mutex
	var cli *apidgrpc.RequestTelemetryClientImpl
	return func(ctx context.Context, rows []gateway.InstanceRouteMinute) error {
		if tlsErr != nil {
			return tlsErr
		}
		mu.Lock()
		if cli == nil {
			c, err := apidgrpc.DialRequestTelemetry(ctx, target, tlsCfg)
			if err != nil {
				mu.Unlock()
				return fmt.Errorf("open request_telemetry client: %w", err)
			}
			cli = c
		}
		c := cli
		mu.Unlock()
		return shipInstanceRouteMinutes(ctx, c, nodeID, rows)
	}
}

func shipInstanceRouteMinutes(ctx context.Context, cli instanceRouteClient, nodeID string, rows []gateway.InstanceRouteMinute) error {
	req := &apidpb.AddInstanceRouteMinutesRequest{NodeId: nodeID, Rows: make([]*apidpb.InstanceRouteMinute, 0, len(rows))}
	for _, r := range rows {
		req.Rows = append(req.Rows, &apidpb.InstanceRouteMinute{
			AccountId: r.AccountID.String(), AppId: r.AppID.String(), InstanceId: r.InstanceID,
			MinuteUnix: r.Minute.Unix(), Route: r.Route, Method: r.Method,
			Requests: r.Requests, BusyMs: r.BusyMS, MaxLatencyMs: int32(min(r.MaxLatencyMS, 1<<31-1)),
		})
	}
	_, err := cli.AddInstanceRouteMinutes(ctx, req)
	return err
}
