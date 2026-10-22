//go:build !no_pg

package state_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

// Hunt #10: job task instances have no app ID; AppByID("") reached Postgres
// as an invalid uuid on every job run.
func TestPgStoreAppByEmptyIDIsNotFound(t *testing.T) {
	s := state.XNewPgStore(t)
	if _, err := s.AppByID(t.Context(), ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("AppByID(\"\") = %v, want ErrNotFound", err)
	}
}
