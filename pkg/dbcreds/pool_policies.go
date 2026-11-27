package dbcreds

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// PoolPolicyMaxAge bounds reuse of a backend with a cached policy revision.
const PoolPolicyMaxAge = 3 * time.Second

type DatabaseRole struct {
	DatabaseID string
	Role       string
}

// PoolRevisions loads the current revisions for just the roles pooled locally.
// A missing verifier is omitted so a revoked role cannot reuse a backend.
func (s *VerifierStore) PoolRevisions(ctx context.Context, roles []DatabaseRole) (map[DatabaseRole]int64, error) {
	ids := make(map[string]struct{})
	wanted := make(map[DatabaseRole]struct{}, len(roles))
	databases := make([]pgtype.UUID, 0, len(roles))
	for _, role := range roles {
		wanted[role] = struct{}{}
		if _, ok := ids[role.DatabaseID]; ok {
			continue
		}
		id, err := databaseUUID(role.DatabaseID)
		if err != nil {
			return nil, err
		}
		databases = append(databases, id)
		ids[role.DatabaseID] = struct{}{}
	}
	rows, err := s.q.GetDatabaseRolePoolRevisions(ctx, s.pool, databases)
	if err != nil {
		return nil, err
	}
	revisions := make(map[DatabaseRole]int64, len(roles))
	for _, row := range rows {
		role := DatabaseRole{DatabaseID: row.DatabaseID.String(), Role: row.RoleName}
		if _, ok := wanted[role]; ok {
			revisions[role] = row.PoolRevision
		}
	}
	return revisions, nil
}
