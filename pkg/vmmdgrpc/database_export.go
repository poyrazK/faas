package vmmdgrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/pgexport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) StreamDatabaseExport(req *vmmdpb.DatabaseExportRequest, stream vmmdpb.Vmmd_StreamDatabaseExportServer) error {
	rt, err := s.databaseRuntime(stream.Context(), databaseCallerSchedd)
	if err != nil {
		return err
	}
	exporter, ok := rt.(interface {
		ExportDatabase(context.Context, string, string, int, io.Writer) (pgexport.Source, error)
	})
	if !ok {
		return status.Error(codes.Unimplemented, "database exports are unavailable")
	}
	source, err := exporter.ExportDatabase(stream.Context(), req.GetDatabaseId(), req.GetDatabase(), int(req.GetPostgresMajor()), exportStreamWriter{stream})
	if err != nil {
		return s.databaseStatus("export", err)
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return s.databaseStatus("export", err)
	}
	return stream.Send(&vmmdpb.DatabaseExportFrame{Content: &vmmdpb.DatabaseExportFrame_Source{Source: raw}})
}

type exportStreamWriter struct {
	vmmdpb.Vmmd_StreamDatabaseExportServer
}

func (w exportStreamWriter) Write(p []byte) (int, error) {
	if err := w.Send(&vmmdpb.DatabaseExportFrame{Content: &vmmdpb.DatabaseExportFrame_Dump{Dump: bytes.Clone(p)}}); err != nil {
		return 0, err
	}
	return len(p), nil
}
