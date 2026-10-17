package statetest

// adr: 1547

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type migrationReplyOverride struct {
	PgStore         *state.PgStore
	commit, resolve bool
}

func (s *migrationReplyOverride) MigrateInstanceOwnerWithRuntimeConfig(context.Context, string, string, string, string, state.RuntimeConfigMigration) error {
	s.commit = true
	return errOverride
}

func (s *migrationReplyOverride) ResolveInstanceMigrationCommit(context.Context, state.MigrationCommitAttempt) (state.MigrationCommitResolution, error) {
	s.resolve = true
	return state.MigrationCommitRecovered, nil
}

// If the adapter silently delegates either optional interface, the native
// fault case could miss its injection. A nil backing store makes that fail.
func TestPgDispatchesMigrationReplyOverrides(t *testing.T) {
	override := &migrationReplyOverride{}
	s := NewPg(override)
	var publisher state.RuntimeConfigMigrationStore = s
	if err := publisher.MigrateInstanceOwnerWithRuntimeConfig(t.Context(), "instance", "source", "dest", "lease", state.RuntimeConfigMigration{}); !errors.Is(err, errOverride) {
		t.Fatal("migration commit bypassed the reply-loss override")
	}
	var resolver state.MigrationCommitRecoveryStore = s
	decision, err := resolver.ResolveInstanceMigrationCommit(t.Context(), state.MigrationCommitAttempt{})
	if err != nil || decision != state.MigrationCommitRecovered || !override.commit || !override.resolve {
		t.Fatal("migration decision bypassed the recovery override")
	}
}
