//go:build !no_pg

package reconcile_test

import "testing"

func TestPgProjectDeletionRechecksStagesAndClones(t *testing.T) {
	for _, competing := range []string{"stage", "clone", "none"} {
		t.Run(competing, func(t *testing.T) {
			store, _, _, _ := pgReconcileStore(t)
			testProjectDeletionRechecksStagesAndClones(t, store, competing)
		})
	}
}

func TestPgReconcileEnvironmentPreservesProduction(t *testing.T) {
	store, _, _, _ := pgReconcileStore(t)
	testReconcileEnvironmentPreservesProduction(t, store)
}

func TestPgProductionReconcilePinsCurrentSettings(t *testing.T) {
	store, _, _, _ := pgReconcileStore(t)
	testProductionReconcilePinsCurrentSettings(t, store)
}

func TestPgProductionReconcilePreservesEnvironmentOnlyServices(t *testing.T) {
	store, _, _, _ := pgReconcileStore(t)
	testProductionReconcilePreservesEnvironmentOnlyServices(t, store)
}

func TestPgReleaseMembershipIsEnvironmentScoped(t *testing.T) {
	store, _, _, _ := pgReconcileStore(t)
	testReleaseMembershipIsEnvironmentScoped(t, store)
}
