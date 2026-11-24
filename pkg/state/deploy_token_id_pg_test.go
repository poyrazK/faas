//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod hunt r26: revoking a deploy token by the 8-character prefix the list
// suggests reached Postgres as an invalid uuid, and the handler answered 503
// "briefly at capacity". A token ID that is not a UUID names no token.
func TestPostgresDeployTokenShortIDIsNotFound(t *testing.T) {
	store := state.NewPgStore(pgtest.OpenMigrated(t))
	account, app := uuid.NewString(), uuid.NewString()
	if _, err := store.RevokeDeployToken(t.Context(), account, app, "ab9bd328"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("revoke short id = %v, want ErrNotFound", err)
	}
	hash := make([]byte, 32)
	if _, _, err := store.RotateDeployToken(t.Context(), account, app, "ab9bd328", hash, "", time.Now().Add(time.Hour), 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rotate short id = %v, want ErrNotFound", err)
	}
}
