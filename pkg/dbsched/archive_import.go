package dbsched

import (
	"context"
	"errors"
	"io"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/pgarchive"
	"github.com/onebox-faas/faas/pkg/pgexport"
)

func (s *Scheduler) StreamDatabaseArchiveImport(ctx context.Context, first *vmmdpb.DatabaseArchiveImportFrame, receive func() (*vmmdpb.DatabaseArchiveImportFrame, error), send func(*vmmdpb.DatabaseArchiveImportProgress) error) error {
	target := first.GetTarget()
	if target == nil || target.GetDatabaseId() == "" || len(target.GetRequest()) == 0 || len(target.GetRequest()) > pgarchive.ManifestLimit {
		return ErrInvalid
	}
	transport, ok := s.vmm.(interface {
		streamDatabaseArchiveImport(context.Context, Node, *vmmdpb.DatabaseArchiveImportFrame, func() (*vmmdpb.DatabaseArchiveImportFrame, error), func(*vmmdpb.DatabaseArchiveImportProgress) error) error
	})
	if !ok {
		return ErrImportUnsupported
	}
	vm, err := s.Wake(ctx, target.GetDatabaseId())
	if err != nil {
		return err
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, pgarchive.UploadTimeout)
	defer cancel()
	if err := s.Touch(ctx, vm.DatabaseID); err != nil {
		return err
	}
	return transport.streamDatabaseArchiveImport(ctx, node, first, receive, send)
}

func (g *GRPCVMM) streamDatabaseArchiveImport(ctx context.Context, node Node, target *vmmdpb.DatabaseArchiveImportFrame, receive func() (*vmmdpb.DatabaseArchiveImportFrame, error), send func(*vmmdpb.DatabaseArchiveImportProgress) error) error {
	client, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	stream, err := client.StreamDatabaseArchiveImport(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(target); err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if err := send(first); err != nil || !first.GetReadyForUpload() {
		return err
	}
	for {
		frame, err := receive()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if length := len(frame.GetDump()); length == 0 || length > pgexport.Chunk {
			return ErrInvalid
		}
		if err := stream.Send(frame); err != nil {
			if errors.Is(err, io.EOF) {
				_, err = stream.Recv()
			}
			return err
		}
	}
	if err := stream.CloseSend(); err != nil {
		return err
	}
	result, err := stream.Recv()
	if err != nil {
		return err
	}
	return send(result)
}
