package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
)

// slowRequestReportingClient is the optional schedd client method behind
// automatic stack captures of slow requests (ADR-1155). Clients without it
// (older schedd builds, test doubles) silently skip the report.
type slowRequestReportingClient interface {
	ReportSlowRequest(ctx context.Context, instanceID, requestID, traceID, route string, inflight time.Duration) (bool, string, error)
}

type instanceScheddRouter interface {
	ScheddForInstance(ctx context.Context, instanceID string) (scheddgrpc.ScheddClient, error)
}

// slowRequestReporter sends a slow-request report to the schedd that owns
// the serving instance.
func slowRequestReporter(router instanceScheddRouter) func(context.Context, gateway.SlowRequestReport) error {
	return func(ctx context.Context, r gateway.SlowRequestReport) error {
		cli, err := router.ScheddForInstance(ctx, r.InstanceID)
		if err != nil {
			return err
		}
		reporter, ok := cli.(slowRequestReportingClient)
		if !ok {
			return nil
		}
		_, _, err = reporter.ReportSlowRequest(ctx, r.InstanceID, r.RequestID, r.TraceID, r.Route, r.Inflight)
		return err
	}
}
