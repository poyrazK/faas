package state_test

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestIsMalformedID: only Postgres's uuid-cast refusal (22P02) is a malformed
// ID; other database failures stay failures (Hunt 14 H14-65).
func TestIsMalformedID(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("deployment by id: %w", &pgconn.PgError{Code: "22P02"}), true},
		{&pgconn.PgError{Code: "57P01"}, false},
		{state.ErrNotFound, false},
		{nil, false},
	} {
		if got := state.IsMalformedID(tc.err); got != tc.want {
			t.Errorf("IsMalformedID(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
