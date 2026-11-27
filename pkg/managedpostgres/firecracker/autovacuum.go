package firecracker

import (
	"context"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbguest/diagnosticspb"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (p *Provider) Autovacuum(ctx context.Context, resourceID string, profile dbguest.AutovacuumProfile) (dbguest.AutovacuumStatus, error) {
	client, ok := p.scheduler.(interface {
		DatabaseAutovacuum(context.Context, *scheddpb.DatabaseAutovacuumRequest, ...grpc.CallOption) (*scheddpb.DatabaseAutovacuumResponse, error)
	})
	if !ok {
		return dbguest.AutovacuumStatus{}, managedpostgres.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 16*time.Second)
	defer cancel()
	resp, err := client.DatabaseAutovacuum(ctx, &scheddpb.DatabaseAutovacuumRequest{
		DatabaseId: resourceUUID(resourceID), Profile: string(profile),
	})
	if status.Code(err) == codes.Unimplemented {
		return dbguest.AutovacuumStatus{}, managedpostgres.ErrUnsupported
	}
	if err != nil {
		return dbguest.AutovacuumStatus{}, providerError(err)
	}
	selected := dbguest.AutovacuumProfile(resp.GetProfile())
	if _, ok := selected.Settings(); !ok {
		return dbguest.AutovacuumStatus{}, managedpostgres.ErrUnavailable
	}
	return dbguest.AutovacuumStatus{
		Profile: selected, State: resp.GetState(),
		Effective: diagnosticspb.DecodeAutovacuum(resp.GetEffective()), ReplicaEffective: diagnosticspb.DecodeAutovacuum(resp.GetReplicaEffective()),
	}, nil
}
