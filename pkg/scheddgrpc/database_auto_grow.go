package scheddgrpc

import (
	"context"
	"errors"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbsched"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) AutoGrowDatabaseStorage(ctx context.Context, req *scheddpb.AutoGrowDatabaseStorageRequest) (*scheddpb.DatabaseRuntime, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	grower, ok := d.(interface {
		AutoGrowStorage(context.Context, string, string, int64) (dbsched.VM, error)
	})
	if !ok || req.GetResizeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "automatic storage resize requires a journal ID")
	}
	vm, err := grower.AutoGrowStorage(ctx, req.GetDatabaseId(), req.GetResizeId(), req.GetVolumeBytes())
	if errors.Is(err, dbsched.ErrConflict) {
		return nil, status.Error(codes.FailedPrecondition, "database is no longer eligible for this automatic resize")
	}
	if err != nil {
		return nil, s.databaseStatus("auto grow", err)
	}
	return databaseRuntime(vm), nil
}
