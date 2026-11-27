package firecracker

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (p *Provider) ControlMigrationActivation(ctx context.Context, target string, op dbguest.Op, a dbmigration.Activation) error {
	client, ok := p.scheduler.(interface {
		ControlDatabaseMigrationActivation(context.Context, *scheddpb.DatabaseMigrationActivationRequest, ...grpc.CallOption) (*scheddpb.DatabaseRef, error)
	})
	if !ok {
		return managedpostgres.ErrUnsupported
	}
	_, err := client.ControlDatabaseMigrationActivation(ctx, &scheddpb.DatabaseMigrationActivationRequest{
		DatabaseId: resourceUUID(target), Op: string(op), Activation: &vmmdpb.DatabaseMigrationActivation{
			SwitchId: a.SwitchID, OperationId: a.OperationID, SourcePin: a.SourcePin,
			SourceDatabase: a.SourceDatabase, Roles: a.Roles,
		},
	})
	if err == nil {
		return nil
	}
	if status.Code(err) == codes.Unimplemented {
		return managedpostgres.ErrUnsupported
	}
	return providerError(err)
}
