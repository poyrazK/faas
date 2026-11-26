package firecracker

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest/extensionpb"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgextension"
	"google.golang.org/grpc"
)

func (p *Provider) Extensions(ctx context.Context, id, enable string, targetMajor int) (pgextension.Catalog, []pgextension.Installed, error) {
	client, ok := p.scheduler.(interface {
		DatabaseExtensions(context.Context, *scheddpb.DatabaseExtensionsRequest, ...grpc.CallOption) (*scheddpb.DatabaseExtensionsResponse, error)
	})
	if !ok {
		return pgextension.Catalog{}, nil, managedpostgres.ErrUnsupported
	}
	resp, err := client.DatabaseExtensions(ctx, &scheddpb.DatabaseExtensionsRequest{
		DatabaseId: resourceUUID(id), DatabaseName: p.cfg.Database, EnableExtension: enable, TargetMajor: int32(targetMajor), //nolint:gosec // validated by service
	})
	if err != nil {
		return pgextension.Catalog{}, nil, providerError(err)
	}
	catalog := extensionpb.Decode(resp.GetCatalog())
	blockers := extensionpb.DecodeInstalled(resp.GetIncompatibleExtensions())
	if catalog == nil || len(blockers) != len(resp.GetIncompatibleExtensions()) {
		return pgextension.Catalog{}, nil, managedpostgres.ErrUnavailable
	}
	return *catalog, blockers, nil
}
