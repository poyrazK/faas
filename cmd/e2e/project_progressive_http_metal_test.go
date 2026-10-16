//go:build metal

package e2e_test

import "testing"

func TestProjectProgressiveHTTPPromotionMetal(t *testing.T) {
	qualifyProjectProgressiveMode(t, false, false, true, "")
}
func TestProjectProgressiveHTTPRollbackMetal(t *testing.T) {
	qualifyProjectProgressiveMode(t, true, false, true, "")
}
func TestProjectProgressiveHTTPStabilizationRollbackMetal(t *testing.T) {
	qualifyProjectProgressiveMode(t, true, true, true, "")
}

func TestProjectProgressiveHTTPMissingEvidenceTimeoutMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "missing_evidence", "")
}
func TestProjectProgressiveHTTPEarlyAbortReplayMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "early_abort", "")
}
func TestProjectProgressiveHTTPFullAbortRecoveryReplayMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "full_recover", "")
}
