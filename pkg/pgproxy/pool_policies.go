package pgproxy

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/dbcreds"
)

type poolRevisionReader interface {
	PoolRevisions(context.Context, []dbcreds.DatabaseRole) (map[dbcreds.DatabaseRole]int64, error)
}

type poolRevision struct {
	revision  int64
	expiresAt time.Time
}

// reusableLocked is called with the pool mutex held. If a revision lookup
// fails, an expired cache permits only fresh PostgreSQL logins until the
// catalogue is reachable again.
func (pl *pool) reusableLocked(srv *serverConn, now time.Time) bool {
	return pl.set.revisions == nil ||
		(now.Before(pl.revision.expiresAt) && srv.hasPolicyRevision && srv.policyRevision == pl.revision.revision)
}

func (pl *pool) borrowIdle() (*serverConn, poolRevision) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.popIdleLocked(), pl.revision
}

func (s *poolSet) refreshPolicies(ctx context.Context) {
	if s.revisions == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshPolicyRevisions(ctx)
		}
	}
}

func (s *poolSet) refreshPolicyRevisions(ctx context.Context) {
	s.mu.Lock()
	local := make(map[dbcreds.DatabaseRole][]*pool)
	for key, pl := range s.pools {
		role := dbcreds.DatabaseRole{DatabaseID: key.databaseID, Role: key.role}
		local[role] = append(local[role], pl)
	}
	s.mu.Unlock()
	if len(local) == 0 {
		return
	}
	roles := make([]dbcreds.DatabaseRole, 0, len(local))
	for role := range local {
		roles = append(roles, role)
	}
	// Bound freshness from the start of the query, including lock wait and
	// network time. An intervening policy commit may postdate its snapshot.
	started := time.Now()
	lookup, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	revisions, err := s.revisions.PoolRevisions(lookup, roles)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for role, pools := range local {
		for _, pl := range pools {
			pl.mu.Lock()
			if err == nil {
				revision, found := revisions[role]
				pl.revision = poolRevision{}
				if found {
					pl.revision = poolRevision{revision: revision, expiresAt: started.Add(dbcreds.PoolPolicyMaxAge)}
				}
			}
			s.pruneIdleLocked(pl, now, time.Time{})
			pl.mu.Unlock()
		}
	}
}
