package firecracker

import (
	"context"
	"errors"
	"io"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgexport"
	"google.golang.org/grpc"
)

func (p *Provider) ExportDatabase(ctx context.Context, resource string, major int, dst io.Writer) (pgexport.Source, error) {
	scheduler, ok := p.scheduler.(interface {
		StreamDatabaseExport(context.Context, *vmmdpb.DatabaseExportRequest, ...grpc.CallOption) (scheddpb.Schedd_StreamDatabaseExportClient, error)
	})
	if !ok {
		return pgexport.Source{}, managedpostgres.ErrUnsupported
	}
	if major < dbguest.MinMajor || major > dbguest.MaxMajor {
		return pgexport.Source{}, managedpostgres.ErrInvalid
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := scheduler.StreamDatabaseExport(ctx, &vmmdpb.DatabaseExportRequest{DatabaseId: resourceUUID(resource), Database: p.cfg.Database, PostgresMajor: int32(major)})
	if err != nil {
		return pgexport.Source{}, providerError(err)
	}
	body := &pgexport.BoundedWriter{Writer: dst}
	var source pgexport.Source
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if !source.Valid() || body.Bytes < 5 {
				return pgexport.Source{}, pgexport.ErrIncomplete
			}
			return source, nil
		}
		if err != nil {
			return pgexport.Source{}, providerError(err)
		}
		if !source.CapturedAt.IsZero() {
			return pgexport.Source{}, pgexport.ErrIncomplete
		}
		switch content := frame.GetContent().(type) {
		case *vmmdpb.DatabaseExportFrame_Dump:
			if len(content.Dump) == 0 || len(content.Dump) > pgexport.Chunk {
				return pgexport.Source{}, pgexport.ErrIncomplete
			}
			if n, err := body.Write(content.Dump); err != nil || n != len(content.Dump) {
				return pgexport.Source{}, errors.Join(io.ErrShortWrite, err)
			}
		case *vmmdpb.DatabaseExportFrame_Source:
			source, err = pgexport.DecodeSource(content.Source)
			if err != nil || source.Major != major {
				return pgexport.Source{}, pgexport.ErrIncomplete
			}
		default:
			return pgexport.Source{}, pgexport.ErrIncomplete
		}
	}
}
