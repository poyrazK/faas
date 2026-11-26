package firecracker

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.DataAPICatalogPreviewer = (*Provider)(nil)

// PreviewDataAPICatalog exposes only the fixed metadata query. It deliberately
// does not implement ReadOnlyQuerier or enable arbitrary SQL on this backend.
func (p *Provider) PreviewDataAPICatalog(ctx context.Context, databaseID string) (managedpostgres.ReadOnlyQueryResult, error) {
	material, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{
		ProviderResourceID: databaseID,
		IdentityKey:        managedpostgres.AgentQueryIdentity,
		IdempotencyKey:     "credentials-" + managedpostgres.AgentQueryIdentity,
		Access:             managedpostgres.CredentialReadOnly,
	})
	if err != nil {
		for _, safe := range []error{managedpostgres.ErrInvalid, managedpostgres.ErrUnsupported, managedpostgres.ErrConflict} {
			if errors.Is(err, safe) {
				return managedpostgres.ReadOnlyQueryResult{}, safe
			}
		}
		return managedpostgres.ReadOnlyQueryResult{}, managedpostgres.ErrUnavailable
	}
	conn, err := p.connect(ctx, material)
	if err != nil {
		return managedpostgres.ReadOnlyQueryResult{}, managedpostgres.ErrUnavailable
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	result, err := managedpostgres.RunReadOnlyStatement(ctx, conn, managedpostgres.ReadOnlyQueryRequest{SQL: api.DataAPICatalogPreviewSQL, MaxRows: managedpostgres.QueryMaxRows, MaxBytes: managedpostgres.QueryMaxValueBytes})
	if err != nil {
		return managedpostgres.ReadOnlyQueryResult{}, managedpostgres.ErrUnavailable
	}
	return result, nil
}
