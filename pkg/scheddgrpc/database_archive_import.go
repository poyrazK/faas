package scheddgrpc

import (
	"context"
	"errors"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbsched"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) StreamDatabaseArchiveImport(stream scheddpb.Schedd_StreamDatabaseArchiveImportServer) error {
	scheduler, err := s.databaseScheduler(stream.Context(), callerAPID)
	if err != nil {
		return err
	}
	importer, ok := scheduler.(interface {
		StreamDatabaseArchiveImport(context.Context, *vmmdpb.DatabaseArchiveImportFrame, func() (*vmmdpb.DatabaseArchiveImportFrame, error), func(*vmmdpb.DatabaseArchiveImportProgress) error) error
	})
	if !ok {
		return status.Error(codes.Unimplemented, "archive import is unavailable")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	err = importer.StreamDatabaseArchiveImport(stream.Context(), first, stream.Recv, stream.Send)
	if err == nil {
		return nil
	}
	if errors.Is(err, dbsched.ErrImportUnsupported) {
		return status.Error(codes.Unimplemented, "archive import is unavailable")
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return s.databaseStatus("archive import", err)
}
