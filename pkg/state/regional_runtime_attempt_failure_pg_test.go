//go:build !no_pg

package state

// adr: 762
import "testing"

func TestPGRegionalRuntimeAttemptFailureContract(t *testing.T) {
	runRegionalAttemptFailureContract(t, func(t *testing.T) (regionalCapacityFixture, Instance) {
		s, _, cell, account := releasePGFixture(t)
		return regionalAttemptFailureFixture(t, s, account, cell)
	})
}
