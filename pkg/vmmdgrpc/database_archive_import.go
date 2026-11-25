package vmmdgrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pgarchive"
	"github.com/onebox-faas/faas/pkg/pgexport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) StreamDatabaseArchiveImport(stream vmmdpb.Vmmd_StreamDatabaseArchiveImportServer) error {
	rt, err := s.databaseRuntime(stream.Context(), databaseCallerSchedd)
	if err != nil {
		return err
	}
	importer, ok := rt.(interface {
		ImportArchive(context.Context, string, pgarchive.Request, io.Reader, func() error) (dbguest.Response, error)
	})
	if !ok {
		return status.Error(codes.Unimplemented, "archive import is unavailable")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	target := first.GetTarget()
	var request pgarchive.Request
	if target == nil || len(target.GetRequest()) > pgarchive.ManifestLimit || json.Unmarshal(target.GetRequest(), &request) != nil || !request.Valid() {
		return status.Error(codes.InvalidArgument, "invalid archive import request")
	}
	result, err := importer.ImportArchive(stream.Context(), target.GetDatabaseId(), request, &archiveChunks{stream: stream}, func() error {
		return stream.Send(&vmmdpb.DatabaseArchiveImportProgress{ReadyForUpload: true})
	})
	if errors.Is(err, pgarchive.ErrArchive) {
		return status.Error(codes.InvalidArgument, dbguest.ErrCodeInvalidArchive)
	}
	if err != nil {
		return s.databaseStatus("archive import", err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return s.databaseStatus("archive import", err)
	}
	return stream.Send(&vmmdpb.DatabaseArchiveImportProgress{Result: raw})
}

type archiveChunks struct {
	stream    vmmdpb.Vmmd_StreamDatabaseArchiveImportServer
	remaining []byte
}

func (r *archiveChunks) Read(p []byte) (int, error) {
	if len(r.remaining) == 0 {
		frame, err := r.stream.Recv()
		if err != nil {
			return 0, err
		}
		r.remaining = frame.GetDump()
		if len(r.remaining) == 0 || len(r.remaining) > pgexport.Chunk {
			return 0, pgarchive.ErrArchive
		}
	}
	n := copy(p, r.remaining)
	r.remaining = r.remaining[n:]
	return n, nil
}
