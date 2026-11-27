package scheddgrpc

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest/extensionpb"
	"github.com/onebox-faas/faas/pkg/pgextension"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) DatabaseExtensions(ctx context.Context, req *scheddpb.DatabaseExtensionsRequest) (*scheddpb.DatabaseExtensionsResponse, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	scheduler, ok := d.(interface {
		Extensions(context.Context, string, string, string, int) (pgextension.Catalog, []pgextension.Installed, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "database extension management is unavailable")
	}
	catalog, blockers, err := scheduler.Extensions(ctx, req.GetDatabaseId(), req.GetDatabaseName(), req.GetEnableExtension(), int(req.GetTargetMajor()))
	if err != nil {
		return nil, s.databaseStatus("extensions", err)
	}
	return &scheddpb.DatabaseExtensionsResponse{Catalog: extensionpb.Encode(&catalog), IncompatibleExtensions: extensionpb.EncodeInstalled(blockers)}, nil
}
