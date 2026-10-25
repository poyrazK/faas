//go:build metal && function_background_native

package e2e_test

// adr: 20261018030615

import (
	"context"
	"sync"

	"github.com/onebox-faas/faas/pkg/e2etest/functionmigration"
	"github.com/onebox-faas/faas/pkg/state"
)

// Keep the real store in a named field. The shared statetest adapter delegates
// every other method, including native grant and ownership validation.
type nativeCommitReplyLoss struct {
	PgStore    *state.PgStore
	instanceID string
	mu         sync.Mutex
	discards   int
	decisions  int
}

func (s *nativeCommitReplyLoss) MigrateInstanceOwnerWithRuntimeConfig(ctx context.Context, id, from, to, lease string, inputs state.RuntimeConfigMigration) error {
	if id != s.instanceID {
		return state.ErrConflict
	}
	if err := s.PgStore.MigrateInstanceOwnerWithRuntimeConfig(ctx, id, from, to, lease, inputs); err != nil {
		return err
	}
	s.mu.Lock()
	s.discards++
	s.mu.Unlock()
	// The real PostgreSQL transaction succeeded. Model loss of its reply at
	// the caller boundary; the scheduler must consult its durable decision.
	return functionmigration.ErrReplyDiscarded
}

func (s *nativeCommitReplyLoss) ResolveInstanceMigrationCommit(ctx context.Context, attempt state.MigrationCommitAttempt) (state.MigrationCommitResolution, error) {
	if attempt.InstanceID != s.instanceID {
		return state.MigrationCommitRetained, state.ErrConflict
	}
	resolution, err := s.PgStore.ResolveInstanceMigrationCommit(ctx, attempt)
	if err == nil && resolution == state.MigrationCommitRecovered {
		s.mu.Lock()
		s.decisions++
		s.mu.Unlock()
	}
	return resolution, err
}

func (s *nativeCommitReplyLoss) evidence() *functionmigration.ReplyFault {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &functionmigration.ReplyFault{Mode: "post-commit-error", CommitDiscards: s.discards, RecoveredDecisions: s.decisions}
}
