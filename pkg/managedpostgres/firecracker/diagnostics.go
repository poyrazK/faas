package firecracker

import (
	"context"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbguest/diagnosticspb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (p *Provider) Diagnose(ctx context.Context, resourceID string) (dbguest.Diagnostics, error) {
	unavailable := dbguest.Diagnostics{State: dbguest.DiagnosticsUnavailable}
	r, err := p.readDiagnostics(ctx, resourceID, nil)
	if err != nil {
		return dbguest.Diagnostics{}, err
	}
	switch r.GetState() {
	case dbguest.DiagnosticsParked:
		return dbguest.Diagnostics{State: dbguest.DiagnosticsParked}, nil
	case dbguest.DiagnosticsAvailable:
		if sample := diagnosticspb.Decode(r.GetActivity()); sample != nil {
			return dbguest.Diagnostics{State: dbguest.DiagnosticsAvailable, Sample: sample}, nil
		}
	}
	return unavailable, nil
}

func (p *Provider) TableHealth(ctx context.Context, resourceID string, options dbguest.TableHealthOptions) (dbguest.TableHealth, error) {
	r, err := p.readDiagnostics(ctx, resourceID, &options)
	if err != nil {
		return dbguest.TableHealth{}, err
	}
	switch r.GetState() {
	case dbguest.DiagnosticsParked:
		return dbguest.TableHealth{State: dbguest.DiagnosticsParked}, nil
	case dbguest.DiagnosticsAvailable:
		if sample := diagnosticspb.DecodeTableHealth(r.GetTableHealth()); sample != nil && len(sample.Tables) <= options.Limit {
			return dbguest.TableHealth{State: dbguest.DiagnosticsAvailable, Sample: sample}, nil
		}
	}
	return dbguest.TableHealth{State: dbguest.DiagnosticsUnavailable}, nil
}

func (p *Provider) readDiagnostics(ctx context.Context, resourceID string, options *dbguest.TableHealthOptions) (*scheddpb.DatabaseDiagnosticsResponse, error) {
	scheduler, ok := p.scheduler.(interface {
		GetDatabaseDiagnostics(context.Context, *scheddpb.DatabaseDiagnosticsRequest, ...grpc.CallOption) (*scheddpb.DatabaseDiagnosticsResponse, error)
	})
	if !ok {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	r, err := scheduler.GetDatabaseDiagnostics(ctx, &scheddpb.DatabaseDiagnosticsRequest{
		DatabaseId: resourceUUID(resourceID), DatabaseName: p.cfg.Database,
		TableHealthOptions: diagnosticspb.EncodeTableHealthOptions(options),
	})
	switch status.Code(err) {
	case codes.Unimplemented, codes.Unavailable, codes.DeadlineExceeded:
		return nil, nil
	case codes.OK:
		return r, nil
	default:
		return nil, providerError(err)
	}
}
