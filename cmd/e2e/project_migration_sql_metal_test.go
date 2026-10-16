//go:build managed_postgres_contract && contractacceptance && metal

package e2e_test

import "testing"

func TestProjectMigrationSQLSuccessMetal(t *testing.T) {
	qualifyProjectProgressiveScenario(t, false, true, "success", newProjectContractSQLSetup(t))
}
func TestProjectMigrationSQLInterruptedMetal(t *testing.T) {
	qualifyProjectProgressiveScenario(t, false, true, "interrupted", newProjectContractSQLSetup(t))
}
