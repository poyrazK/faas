package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/ci"
)

// CIStore is an optional internal surface, deliberately outside the broad
// Store interface. CI metadata has no scheduler authority. The dormant Jobs
// adapter separately writes ordinary job intent and never owns instances.
func (s *MemStore) CIStore() ci.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.internalCI == nil {
		s.internalCI = ci.NewMemoryStore()
	}
	return s.internalCI
}

func (s *PgStore) CIStore() ci.Store { return ci.NewPostgresStore(s.pool.Pool) }

func (s *PgStore) ciJobRunOwned(ctx context.Context, account, runID string) (bool, error) {
	return ci.NewPostgresStore(s.pool.Pool).JobsRunOwned(ctx, account, runID)
}

func (s *PgStore) ciJobRetryBlocked(ctx context.Context, runID string) (bool, error) {
	return ci.NewPostgresStore(s.pool.Pool).JobsRetryBlocked(ctx, runID)
}
