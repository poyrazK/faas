package neon

import (
	"context"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.ReadOnlyQuerier = (*Provider)(nil)

// QueryReadOnly runs one statement as the database's agent query login
// (ADR-20261117215858 §2). The login is an ordinary read_only credential
// with the fixed agent query identity: issuing it again recovers the same
// login and runs the read-only privilege verification of every read-only
// binding, so the statement can read only what such a binding can.
func (p *Provider) QueryReadOnly(ctx context.Context, req managedpostgres.ReadOnlyQueryRequest) (managedpostgres.ReadOnlyQueryResult, error) {
	if err := managedpostgres.ValidateQuerySQL(req.SQL); err != nil {
		return managedpostgres.ReadOnlyQueryResult{}, err
	}
	material, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{
		ProviderResourceID: req.ProviderResourceID,
		IdentityKey:        managedpostgres.AgentQueryIdentity,
		IdempotencyKey:     "credentials-" + managedpostgres.AgentQueryIdentity,
		Access:             managedpostgres.CredentialReadOnly,
	})
	if err != nil {
		return managedpostgres.ReadOnlyQueryResult{}, err
	}
	conn, err := connectProbe(ctx, material)
	if err != nil {
		return managedpostgres.ReadOnlyQueryResult{}, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	return managedpostgres.RunReadOnlyStatement(ctx, conn, req)
}
