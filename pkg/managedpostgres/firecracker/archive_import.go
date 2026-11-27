package firecracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgarchive"
	"github.com/onebox-faas/faas/pkg/pgexport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (p *Provider) ImportArchive(ctx context.Context, id, operation string, manifest pgarchive.Manifest, src io.Reader) (managedpostgres.ImportStatus, error) {
	scheduler, ok := p.scheduler.(interface {
		StreamDatabaseArchiveImport(context.Context, ...grpc.CallOption) (scheddpb.Schedd_StreamDatabaseArchiveImportClient, error)
	})
	if !ok {
		return managedpostgres.ImportStatus{}, managedpostgres.ErrUnsupported
	}
	request := pgarchive.Request{TargetDatabase: p.cfg.Database, OperationID: operation, Manifest: manifest}
	if !request.Valid() || id == "" || src == nil {
		return managedpostgres.ImportStatus{}, managedpostgres.ErrInvalid
	}
	raw, err := json.Marshal(request)
	if err != nil || len(raw) >= pgarchive.ManifestLimit {
		return managedpostgres.ImportStatus{}, managedpostgres.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, pgarchive.UploadTimeout)
	defer cancel()
	stream, err := scheduler.StreamDatabaseArchiveImport(ctx)
	if err != nil {
		return managedpostgres.ImportStatus{}, providerError(err)
	}
	target := &vmmdpb.DatabaseArchiveImportTarget{DatabaseId: resourceUUID(id), Request: raw}
	if err := stream.Send(&vmmdpb.DatabaseArchiveImportFrame{Content: &vmmdpb.DatabaseArchiveImportFrame_Target{Target: target}}); err != nil {
		return managedpostgres.ImportStatus{}, archiveStreamError(stream, err)
	}
	first, err := stream.Recv()
	if err != nil {
		return managedpostgres.ImportStatus{}, archiveStreamError(stream, err)
	}
	if !first.GetReadyForUpload() {
		return archiveImportResult(first, operation)
	}
	// The VM verifies the digest. Bound the outbound stream and wait for
	// EOF on the archive part before half-closing the request.
	chunk := make([]byte, pgexport.Chunk)
	limited := io.LimitReader(src, manifest.SizeBytes+1)
	for {
		n, err := limited.Read(chunk)
		if n != 0 {
			frame := &vmmdpb.DatabaseArchiveImportFrame{Content: &vmmdpb.DatabaseArchiveImportFrame_Dump{Dump: bytes.Clone(chunk[:n])}}
			if sendErr := stream.Send(frame); sendErr != nil {
				return managedpostgres.ImportStatus{}, archiveStreamError(stream, sendErr)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return managedpostgres.ImportStatus{}, errors.Join(managedpostgres.ErrInvalid, pgarchive.ErrArchive)
		}
	}
	if err := stream.CloseSend(); err != nil {
		return managedpostgres.ImportStatus{}, archiveStreamError(stream, err)
	}
	result, err := stream.Recv()
	if err != nil {
		return managedpostgres.ImportStatus{}, archiveStreamError(stream, err)
	}
	return archiveImportResult(result, operation)
}

func archiveStreamError(stream scheddpb.Schedd_StreamDatabaseArchiveImportClient, err error) error {
	if errors.Is(err, io.EOF) {
		if _, statusErr := stream.Recv(); statusErr != nil {
			err = statusErr
		}
	}
	if status.Code(err) == codes.InvalidArgument && status.Convert(err).Message() == dbguest.ErrCodeInvalidArchive {
		return errors.Join(managedpostgres.ErrInvalid, pgarchive.ErrArchive)
	}
	if status.Code(err) == codes.Unimplemented {
		return managedpostgres.ErrUnsupported
	}
	return providerError(err)
}

func archiveImportResult(reply *vmmdpb.DatabaseArchiveImportProgress, operation string) (managedpostgres.ImportStatus, error) {
	var result dbguest.Response
	if reply.GetReadyForUpload() || len(reply.GetResult()) > pgarchive.ManifestLimit || json.Unmarshal(reply.GetResult(), &result) != nil {
		return managedpostgres.ImportStatus{}, managedpostgres.ErrUnavailable
	}
	switch result.Error {
	case dbguest.ErrCodeImportOperationMismatch:
		return managedpostgres.ImportStatus{}, managedpostgres.ErrConflict
	case dbguest.ErrCodeInvalidArchive:
		return managedpostgres.ImportStatus{}, errors.Join(managedpostgres.ErrInvalid, pgarchive.ErrArchive)
	case "":
	default:
		return managedpostgres.ImportStatus{}, managedpostgres.ErrUnavailable
	}
	status := result.Import
	if status == nil || status.OperationID != operation {
		return managedpostgres.ImportStatus{}, managedpostgres.ErrUnavailable
	}
	switch status.State {
	case dbguest.ImportDone, dbguest.ImportRunning, dbguest.ImportFailed:
	default:
		return managedpostgres.ImportStatus{}, managedpostgres.ErrUnavailable
	}
	return managedpostgres.ImportStatus{
		State: managedpostgres.ImportState(status.State), OperationID: operation, ErrorCode: status.Error,
		SourceMajor: status.SourceMajor, RequiredVolumeBytes: max(status.RequiredVolumeBytes, 0),
		IncompatibleExtensions: status.IncompatibleExtensions, MissingExtensions: dbguest.ReportableExtensions(status.MissingExtensions),
		StartedAt: status.StartedAt, FinishedAt: status.FinishedAt,
	}, nil
}
