//go:build !no_pg

package state_test

import "testing"

func TestPgStoreScenarioTestMemberServiceNames(t *testing.T) {
	store, ctx := pgStore(t)
	assertScenarioTestMemberServiceNames(t, ctx, store, "member-names-pg@example.com")
}
