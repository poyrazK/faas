//go:build !no_pg

package state_test

import "testing"

func TestScenarioTestConsumerIntentPostgres(t *testing.T) {
	// adr: 984 — concurrent creation commits one consumer and its intent together.
	store, _ := pgStore(t)
	scenarioConsumerIntentContract(t, store)
}
