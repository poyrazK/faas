package rolloutevidence

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// prod 2026-11-15: a rolled-back canary stayed the debug status headline
// after a newer release went live; it counts only while its stable release
// still serves.
func TestRollbackSupersededByANewerRelease(t *testing.T) {
	aborted := api.DebugRolloutExplanation{State: api.DebugRolloutAborted, StableDeploymentID: "stable"}
	if rollbackSuperseded(aborted, []string{"stable"}) {
		t.Fatal("a rollback whose stable release still serves was dropped")
	}
	if !rollbackSuperseded(aborted, []string{"newer"}) {
		t.Fatal("a rollback replaced by a newer live release was kept")
	}
	if rollbackSuperseded(aborted, nil) || rollbackSuperseded(api.DebugRolloutExplanation{State: api.DebugRolloutAborted}, []string{"newer"}) {
		t.Fatal("a rollback without serving or stable evidence was dropped")
	}
}
