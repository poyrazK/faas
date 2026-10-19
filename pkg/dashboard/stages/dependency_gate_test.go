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

// The footer names the deployment status for unrecognised statuses. It sits
// inside the pre-rendered template.HTML block, so it must be escaped like
// every other value there.
func TestRenderSummaryHTML_FooterStatusEscaped(t *testing.T) {
	ss := allItems([]int64{1000, 1000, 1000, 0, 0, 0})
	terminalAt := time.Date(2026, 8, 20, 12, 34, 56, 0, time.UTC)
	html := string(RenderSummaryHTML(ss, `<img src=x onerror=alert(1)>`, terminalAt))
	if strings.Contains(html, "<img") || !strings.Contains(html, "&lt;img src=x onerror=alert(1)&gt; at 2026-08-20T12:34:56Z") {
		t.Fatalf("footer status not escaped: %s", html)
	}
}
