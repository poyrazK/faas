//go:build !no_pg

// adr: 20261103164725
package state_test

import "testing"

func TestPgAccountOverageCaps(t *testing.T) {
	s, _ := pgStore(t)
	testAccountOverageCaps(t, s)
}
