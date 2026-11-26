package scheddgrpc

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbqueryerrors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) ExchangeDatabaseErrorHistory(ctx context.Context, req *scheddpb.ExchangeDatabaseErrorHistoryRequest) (*scheddpb.ExchangeDatabaseErrorHistoryResponse, error) {
	d, err := s.databaseScheduler(ctx, callerPgproxy)
	if err != nil {
		return nil, err
	}
	scheduler, ok := d.(interface {
		ExchangeErrorHistory(context.Context, []string, []dbqueryerrors.Event) ([]dbqueryerrors.Policy, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "database error history is unavailable")
	}
	if len(req.GetEvents()) > dbqueryerrors.MaxBatch || len(req.GetRuntimeDatabaseIds()) > dbqueryerrors.MaxDatabaseIDs {
		return nil, status.Error(codes.InvalidArgument, "oversized database error report")
	}
	events := make([]dbqueryerrors.Event, 0, len(req.GetEvents()))
	for _, event := range req.GetEvents() {
		events = append(events, dbqueryerrors.Decode(event))
	}
	policies, err := scheduler.ExchangeErrorHistory(ctx, req.GetRuntimeDatabaseIds(), events)
	if err != nil {
		return nil, s.databaseStatus("exchange database error history", err)
	}
	out := &scheddpb.ExchangeDatabaseErrorHistoryResponse{}
	for _, policy := range policies {
		out.Policies = append(out.Policies, &scheddpb.DatabaseErrorHistoryPolicy{RuntimeDatabaseId: policy.RuntimeID, Epoch: policy.Epoch})
	}
	return out, nil
}
