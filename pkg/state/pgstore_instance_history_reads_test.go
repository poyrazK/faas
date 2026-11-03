//go:build !no_pg

// adr: 20261103131546
package state_test

import "testing"

func TestPgInstanceHistoryReads(t *testing.T) {
	s, ctx := pgStore(t)
	node := resolveDefaultLocal(t, ctx, s)
	_, appID, deploymentID := seedLiveDeploy(t, s, ctx, "instance-history")
	testInstanceHistoryReads(t, s, appID, deploymentID, node)
}
