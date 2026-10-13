package vmmdgrpc

import (
	"context"
	"errors"
	"io"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbvm"
)

// ExportDatabaseVolume streams a stopped database's volume to schedd for a
// move: the header, each data extent zstd-compressed, then the digest.
func (s *Server) ExportDatabaseVolume(req *vmmdpb.ExportDatabaseVolumeRequest, stream vmmdpb.Vmmd_ExportDatabaseVolumeServer) error {
	ctx := stream.Context()
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
	if err != nil {
		return err
	}
	ex, err := rt.ExportVolume(req.GetDatabaseId())
	if err != nil {
		return s.databaseStatus("export volume", err)
	}
	defer func() { _ = ex.Close() }()
	if err := stream.Send(&vmmdpb.DatabaseVolumeChunk{DatabaseId: req.GetDatabaseId(), SizeBytes: ex.Size()}); err != nil {
		return err
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return s.databaseStatus("export volume", err)
	}
	defer func() { _ = enc.Close() }()
	for {
		offset, data, err := ex.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return s.databaseStatus("export volume", err)
		}
		if err := stream.Send(&vmmdpb.DatabaseVolumeChunk{Offset: offset, ZstdData: enc.EncodeAll(data, nil)}); err != nil {
			return err
		}
	}
	return stream.Send(&vmmdpb.DatabaseVolumeChunk{Sha256: ex.Sum()})
}

// ImportDatabaseVolume installs a volume schedd relays from another node's
// ExportDatabaseVolume, only if its digest matches.
func (s *Server) ImportDatabaseVolume(stream vmmdpb.Vmmd_ImportDatabaseVolumeServer) error {
	rt, err := s.databaseRuntime(stream.Context(), databaseCallerSchedd)
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	im, err := rt.ImportVolume(first.GetDatabaseId(), first.GetSizeBytes())
	if err != nil {
		return s.databaseStatus("import volume", err)
	}
	done := false
	defer func() {
		if !done {
			im.Abort()
		}
	}()
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(2*dbvm.TransferChunk))
	if err != nil {
		return s.databaseStatus("import volume", err)
	}
	defer dec.Close()
	buf := make([]byte, 0, dbvm.TransferChunk)
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return status.Error(codes.InvalidArgument, "volume transfer ended without its digest")
		}
		if err != nil {
			return err
		}
		if digest := msg.GetSha256(); len(digest) > 0 {
			done = true
			if err := im.Commit(digest); err != nil {
				return s.databaseStatus("import volume", err)
			}
			return stream.SendAndClose(&vmmdpb.ImportDatabaseVolumeResponse{})
		}
		data, err := dec.DecodeAll(msg.GetZstdData(), buf[:0])
		if err != nil || len(data) > dbvm.TransferChunk {
			return status.Error(codes.InvalidArgument, "volume transfer extent does not decode")
		}
		if err := im.Write(msg.GetOffset(), data); err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
	}
}

// SyncDatabaseArchive ships a database's archive files on this node to the
// shared bucket before it moves away.
func (s *Server) SyncDatabaseArchive(ctx context.Context, req *vmmdpb.SyncDatabaseArchiveRequest) (*vmmdpb.SyncDatabaseArchiveResponse, error) {
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
	if err != nil {
		return nil, err
	}
	n, err := rt.SyncArchive(ctx, req.GetDatabaseId())
	if err != nil {
		return nil, s.databaseStatus("sync archive", err)
	}
	return &vmmdpb.SyncDatabaseArchiveResponse{Uploaded: n}, nil
}
