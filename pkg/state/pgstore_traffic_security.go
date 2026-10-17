// adr: 570
package state

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

type PGTrafficSecurityBackend struct{ pool *pgxpool.Pool }

var _ trafficrevocation.Store = (*PGTrafficSecurityBackend)(nil)

func NewPGTrafficSecurityBackend(pool *pgxpool.Pool) *PGTrafficSecurityBackend {
	return &PGTrafficSecurityBackend{pool: pool}
}

// Read uses the shared authoritative pool. Empty input still touches the table
// so production startup can verify the migration and privileges before serving.
func (b *PGTrafficSecurityBackend) Read(ctx context.Context, scopes []trafficrevocation.Scope) (map[trafficrevocation.Scope]trafficrevocation.State, error) {
	if b == nil || b.pool == nil || len(scopes) > api.TrafficSecurityMaxScopes {
		return nil, trafficrevocation.ErrUnavailable
	}
	args := sqlc.ReadTrafficSecurityEpochsParams{}
	requested := make(map[trafficrevocation.Scope][]trafficrevocation.Scope)
	for _, scope := range scopes {
		id, err := uuid.Parse(scope.ID)
		if err != nil || id == uuid.Nil || (scope.Kind != "account" && scope.Kind != "app" && scope.Kind != "deployment") {
			return nil, trafficrevocation.ErrUnavailable
		}
		canonical := trafficrevocation.Scope{Kind: scope.Kind, ID: id.String()}
		requested[canonical] = append(requested[canonical], scope)
		args.ScopeKinds = append(args.ScopeKinds, scope.Kind)
		args.ScopeIds = append(args.ScopeIds, pgtype.UUID{Bytes: id, Valid: true})
	}
	rows, err := sqlc.New().ReadTrafficSecurityEpochs(ctx, b.pool, args)
	if err != nil {
		return nil, fmt.Errorf("read traffic security generations: %w", err)
	}
	result := make(map[trafficrevocation.Scope]trafficrevocation.State, len(rows))
	for _, row := range rows {
		if !row.ScopeID.Valid || row.Revision <= 0 {
			return nil, trafficrevocation.ErrUnavailable
		}
		canonical := trafficrevocation.Scope{Kind: row.ScopeKind, ID: uuid.UUID(row.ScopeID.Bytes).String()}
		for _, scope := range requested[canonical] {
			result[scope] = trafficrevocation.State{Revision: row.Revision, Revoked: row.Revoked}
		}
	}
	return result, nil
}
