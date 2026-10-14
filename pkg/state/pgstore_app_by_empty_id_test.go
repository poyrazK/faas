//go:build !no_pg

package state

import (
	"errors"
	"testing"
)

// Hunt #10: job task instances have no app ID; AppByID("") reached Postgres
// as an invalid uuid on every job run.
func TestPgStoreAppByEmptyIDIsNotFound(t *testing.T) {
	s := newPgStore(t)
	if _, err := s.AppByID(t.Context(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AppByID(\"\") = %v, want ErrNotFound", err)
	}
}
