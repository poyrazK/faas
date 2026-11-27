package firecracker

import (
	"context"
	"strings"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (p *Provider) autoGrowStorage(ctx context.Context, req managedpostgres.UpdateRequest) (managedpostgres.ObservedDatabase, error) {
	scheduler, ok := p.scheduler.(interface {
		AutoGrowDatabaseStorage(context.Context, *scheddpb.AutoGrowDatabaseStorageRequest, ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error)
	})
	if !ok {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnsupported
	}
	expected := req.PreviousSpec
	expected.StorageLimitBytes = req.Spec.StorageLimitBytes
	if expected != req.Spec || req.Spec.StorageLimitBytes <= req.PreviousSpec.StorageLimitBytes ||
		req.Spec.Availability != managedpostgres.AvailabilitySingleZone {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrInvalid
	}
	if err := p.supports(req.Spec); err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	rt, err := scheduler.AutoGrowDatabaseStorage(ctx, &scheddpb.AutoGrowDatabaseStorageRequest{
		DatabaseId: resourceUUID(req.ResourceID), ResizeId: strings.TrimPrefix(req.IdempotencyKey, "resize-"),
		VolumeBytes: req.Spec.StorageLimitBytes,
	})
	switch status.Code(err) {
	case codes.OK:
		return p.observed(rt), nil
	case codes.FailedPrecondition:
		reason := "awaiting_idle"
		if !req.Spec.ScaleToZero {
			reason = "maintenance_deferred"
		}
		return managedpostgres.ObservedDatabase{}, &managedpostgres.ChangeRejectedError{Code: reason}
	case codes.ResourceExhausted:
		return managedpostgres.ObservedDatabase{}, &managedpostgres.ChangeRejectedError{Code: "node_capacity"}
	case codes.Unimplemented:
		return managedpostgres.ObservedDatabase{}, &managedpostgres.ChangeRejectedError{Code: "auto_grow_unavailable"}
	default:
		return managedpostgres.ObservedDatabase{}, providerError(err)
	}
}
