//go:build contractacceptance && metal

package e2e_test

import "testing"

func TestProjectMigrationContractSuccessMetal(t *testing.T) {
	qualifyProjectProgressiveScenario(t, false, true, "success")
}
func TestProjectMigrationContractFailureMetal(t *testing.T) {
	qualifyProjectProgressiveScenario(t, false, true, "failure")
}
func TestProjectMigrationContractInterruptedMetal(t *testing.T) {
	qualifyProjectProgressiveScenario(t, false, true, "interrupted")
}
