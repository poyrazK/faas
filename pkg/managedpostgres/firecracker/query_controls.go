package firecracker

import (
	"context"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbguest/diagnosticspb"
	"github.com/onebox-faas/faas/pkg/dbquerycontrol"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type queryControlsScheduler interface {
	ListDatabaseSessions(context.Context, *scheddpb.ListDatabaseSessionsRequest, ...grpc.CallOption) (*scheddpb.ListDatabaseSessionsResponse, error)
	CancelDatabaseQuery(context.Context, *scheddpb.CancelDatabaseQueryRequest, ...grpc.CallOption) (*scheddpb.CancelDatabaseQueryResponse, error)
}

func (p *Provider) ListSessions(ctx context.Context, resourceID string) (dbguest.Sessions, error) {
	client, ok := p.scheduler.(queryControlsScheduler)
	if !ok {
		return dbguest.Sessions{}, managedpostgres.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	response, err := client.ListDatabaseSessions(ctx, &scheddpb.ListDatabaseSessionsRequest{DatabaseId: resourceUUID(resourceID), DatabaseName: p.cfg.Database})
	if status.Code(err) == codes.Unimplemented {
		return dbguest.Sessions{}, managedpostgres.ErrUnsupported
	}
	if err != nil {
		return dbguest.Sessions{}, providerError(err)
	}
	switch response.GetState() {
	case dbguest.DiagnosticsParked:
		return dbguest.Sessions{State: dbguest.DiagnosticsParked}, nil
	case dbguest.DiagnosticsAvailable:
		if sample := diagnosticspb.DecodeSessions(response.GetSample()); sample != nil {
			return dbguest.Sessions{State: dbguest.DiagnosticsAvailable, Sample: sample}, nil
		}
	}
	return dbguest.Sessions{State: dbguest.DiagnosticsUnavailable}, nil
}

func (p *Provider) CancelQuery(ctx context.Context, resourceID, targetID string) (string, error) {
	client, ok := p.scheduler.(queryControlsScheduler)
	if !ok {
		return "", managedpostgres.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 9*time.Second)
	defer cancel()
	response, err := client.CancelDatabaseQuery(ctx, &scheddpb.CancelDatabaseQueryRequest{DatabaseId: resourceUUID(resourceID), TargetId: targetID})
	if status.Code(err) == codes.Unimplemented {
		return "", managedpostgres.ErrUnsupported
	}
	if err != nil {
		return "", providerError(err)
	}
	if outcome := response.GetOutcome(); dbquerycontrol.ValidOutcome(outcome) {
		return outcome, nil
	}
	return dbquerycontrol.Unconfirmed, nil
}
