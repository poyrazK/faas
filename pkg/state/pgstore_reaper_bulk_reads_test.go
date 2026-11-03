//go:build !no_pg

// adr: 20261102205122
package state_test

import (
	"testing"

	"github.com/google/uuid"
)

func TestPgReaperBulkReads(t *testing.T) {
	s, ctx := pgStore(t)
	plainAccount, _, plainDeployment := seedLiveDeploy(t, s, ctx, "bulk-plain", "plain")
	budgetAccount, _, budgetDeployment := seedLiveDeploy(t, s, ctx, "bulk-budget", "budget")
	testReaperBulkReads(t, s, plainAccount, budgetAccount, []string{plainDeployment, budgetDeployment}, uuid.NewString())
}
