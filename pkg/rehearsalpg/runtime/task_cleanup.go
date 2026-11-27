package runtime

import (
	"context"

	"github.com/onebox-faas/faas/pkg/rehearsalpg/runtime/sqlc"
)

// ObserveDisposedTaskFixture reads the physical catalog after teardown. The
// journal identifies the same resource and backend used by this rehearsal.
func (s *Service) ObserveDisposedTaskFixture(ctx context.Context, account, run, migrationApp string) (databasePresent bool, loginsRemaining int, err error) {
	p, ok := s.Backend.(*Postgres)
	if !ok {
		return false, 0, ErrFixture
	}
	r, err := s.Catalog.GetScenarioPostgres(ctx, account, run)
	if err != nil || r.AccountID != account || r.RunID != run || r.AppID != migrationApp ||
		r.Status != "deleted" || !p.resourceNames(r) {
		return false, 0, ErrFixture
	}
	conn, err := p.connect(ctx, "")
	if err != nil {
		return false, 0, ErrFixture
	}
	defer closeTaskParticipantConnection(ctx, conn)
	q := sqlc.New()
	present, err := q.DatabaseExists(ctx, conn, r.DatabaseName)
	if err != nil {
		return false, 0, ErrFixture
	}
	owner, err := q.RoleExists(ctx, conn, r.RoleName)
	if err != nil {
		return false, 0, ErrFixture
	}
	roles, err := q.ParticipantRoles(ctx, conn, r.RoleName)
	if err != nil {
		return false, 0, ErrFixture
	}
	count := len(roles)
	if owner {
		count++
	}
	return present, count, nil
}
