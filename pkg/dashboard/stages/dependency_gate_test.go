// adr: 685
package stages

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDependencyGateBlockerIsVisibleAndEscaped(t *testing.T) {
	deadline := time.Now().UTC().Add(time.Minute)
	stages := state.StageState{Current: state.StageReadiness, DependencyGate: &state.DeploymentDependencyGate{
		Status: "waiting", DeadlineAt: &deadline, Blocker: `waiting for dependency <backend>`,
	}}
	var output bytes.Buffer
	if err := RenderSummaryText(&output, stages, "snapshotting", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), stages.DependencyGate.Blocker) || !strings.Contains(output.String(), "Dependency deadline:") {
		t.Fatalf("missing wait diagnostics: %s", output.String())
	}
	html := string(RenderSummaryHTML(stages, "snapshotting", time.Time{}))
	if !strings.Contains(html, "&lt;backend&gt;") || strings.Contains(html, "<backend>") {
		t.Fatalf("unsafe dependency diagnostics: %s", html)
	}
}
