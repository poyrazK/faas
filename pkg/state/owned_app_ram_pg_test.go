//go:build !no_pg

// adr: 20261026170000
package state_test

import "testing"

func TestOwnedAppRAMPostgres(t *testing.T) {
	s, _ := lifecycleCompletionPGStore(t)
	checkOwnedAppRAM(t, s)
}
