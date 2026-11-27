package firecracker

import (
	"context"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (p *Provider) RestoreDrill(ctx context.Context, resourceID, key string, start bool) (api.ManagedPostgresRestoreDrill, error) {
	client, ok := p.scheduler.(interface {
		RequestDatabaseRestoreDrill(context.Context, *scheddpb.RequestDatabaseRestoreDrillRequest, ...grpc.CallOption) (*scheddpb.DatabaseRestoreDrill, error)
		GetDatabaseRestoreDrill(context.Context, *scheddpb.GetDatabaseRestoreDrillRequest, ...grpc.CallOption) (*scheddpb.DatabaseRestoreDrill, error)
	})
	if !ok {
		return api.ManagedPostgresRestoreDrill{}, managedpostgres.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var r *scheddpb.DatabaseRestoreDrill
	var err error
	if start {
		r, err = client.RequestDatabaseRestoreDrill(ctx, &scheddpb.RequestDatabaseRestoreDrillRequest{DatabaseId: resourceUUID(resourceID), RequestId: key})
	} else {
		r, err = client.GetDatabaseRestoreDrill(ctx, &scheddpb.GetDatabaseRestoreDrillRequest{DatabaseId: resourceUUID(resourceID), DrillId: key})
	}
	if status.Code(err) == codes.Unimplemented {
		return api.ManagedPostgresRestoreDrill{}, managedpostgres.ErrUnsupported
	}
	if err != nil {
		return api.ManagedPostgresRestoreDrill{}, providerError(err)
	}
	if r.GetDrillId() == "" || r.GetRequestId() == "" || r.GetStartedAt().CheckValid() != nil {
		return api.ManagedPostgresRestoreDrill{}, managedpostgres.ErrUnavailable
	}
	outcome := r.GetOutcome()
	if outcome == "ok" {
		outcome = "succeeded"
	}
	out := api.ManagedPostgresRestoreDrill{RequestID: r.GetRequestId(), ManagedPostgresRestoreDrillStatus: api.ManagedPostgresRestoreDrillStatus{
		DrillID: r.GetDrillId(), Outcome: outcome, StartedAt: r.GetStartedAt().AsTime(), ErrorCode: r.GetErrorCode(),
		FinishedAt: restoreDrillTime(r.GetFinishedAt()), RecoveredTo: restoreDrillTime(r.GetRecoveredTo()),
	}}
	if target := restoreDrillTime(r.GetTargetAt()); target != nil {
		out.TargetAt = *target
	}
	out.SetDuration()
	return out, nil
}

func restoreDrillTime(value *timestamppb.Timestamp) *time.Time {
	if value.CheckValid() != nil {
		return nil
	}
	t := value.AsTime()
	return &t
}
