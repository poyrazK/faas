package scheddgrpc

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) StreamDatabaseExport(req *vmmdpb.DatabaseExportRequest, stream scheddpb.Schedd_StreamDatabaseExportServer) error {
	scheduler, err := s.databaseScheduler(stream.Context(), callerAPID)
	if err != nil {
		return err
	}
	exporter, ok := scheduler.(interface {
		StreamDatabaseExport(context.Context, *vmmdpb.DatabaseExportRequest, func(*vmmdpb.DatabaseExportFrame) error) error
	})
	if !ok {
		return status.Error(codes.Unimplemented, "database exports are unavailable")
	}
	if err := exporter.StreamDatabaseExport(stream.Context(), req, stream.Send); err != nil {
		return s.databaseStatus("export", err)
	}
	return nil
}
