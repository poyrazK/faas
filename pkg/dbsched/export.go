package dbsched

import (
	"context"
	"errors"
	"io"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pgexport"
)

func (s *Scheduler) StreamDatabaseExport(ctx context.Context, req *vmmdpb.DatabaseExportRequest, send func(*vmmdpb.DatabaseExportFrame) error) error {
	major := int(req.GetPostgresMajor())
	if !pgexport.ValidDatabase(req.GetDatabase()) || major < dbguest.MinMajor || major > dbguest.MaxMajor {
		return ErrInvalid
	}
	vmm, ok := s.vmm.(interface {
		streamDatabaseExport(context.Context, Node, *vmmdpb.DatabaseExportRequest, func(*vmmdpb.DatabaseExportFrame) error) error
	})
	if !ok {
		return ErrNodeUnavailable
	}
	id := req.GetDatabaseId()
	vm, err := s.Wake(ctx, id)
	if err != nil {
		return err
	}
	if vm.Major != major {
		return ErrConflict
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, pgexport.Timeout)
	defer cancel()
	if err := s.Touch(ctx, id); err != nil {
		return err
	}
	go func() {
		ticker := time.NewTicker(max(time.Millisecond, min(5*time.Second, s.cfg.IdleTimeout/3)))
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Touch(ctx, id); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return vmm.streamDatabaseExport(ctx, node, req, send)
}

func (g *GRPCVMM) streamDatabaseExport(ctx context.Context, node Node, req *vmmdpb.DatabaseExportRequest, send func(*vmmdpb.DatabaseExportFrame) error) error {
	client, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	stream, err := client.StreamDatabaseExport(ctx, req)
	if err != nil {
		return err
	}
	for {
		frame, err := stream.Recv()
		switch {
		case errors.Is(err, io.EOF):
			return nil
		case err != nil:
			return err
		}
		if err := send(frame); err != nil {
			return err
		}
	}
}
