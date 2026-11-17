package daemonunitspec

// adr: 1580

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// apid reloads by a two-generation hand-off (ADR-1736) and its cgroup holds
// both generations while the old one drains (ADR 20261018132058). The unit
// shipped by the generator and by the control-plane role must carry the
// memory cap, the per-process soft limit, the hand-off reload and the API
// socket that covers a restart fallback.
func TestAPIDUnitCarriesTwoGenerationMemory(t *testing.T) {
	u := UnitApid()
	if u.MemoryMax != fmt.Sprintf("%dM", api.APIDMemoryMaxMB) {
		t.Errorf("apid MemoryMax = %q, want %dM", u.MemoryMax, api.APIDMemoryMaxMB)
	}
	var gomemlimit string
	for _, kv := range u.Environment {
		if kv.Key == "GOMEMLIMIT" {
			gomemlimit = kv.Value
		}
	}
	if gomemlimit != fmt.Sprintf("%dMiB", api.APIDGoMemLimitMB) {
		t.Errorf("apid GOMEMLIMIT = %q, want %dMiB", gomemlimit, api.APIDGoMemLimitMB)
	}
	if !HandsOffOnReload("apid") || u.NotifyAccess != "main" || u.OOMPolicy != "continue" {
		t.Errorf("apid ExecReload=%q NotifyAccess=%q OOMPolicy=%q, want a hand-off unit", u.ExecReload, u.NotifyAccess, u.OOMPolicy)
	}
	want := []string{
		fmt.Sprintf("MemoryMax=%dM\n", api.APIDMemoryMaxMB),
		fmt.Sprintf("Environment=GOMEMLIMIT=%dMiB\n", api.APIDGoMemLimitMB),
		"ExecReload=" + handoffReload + "\n",
		"NotifyAccess=main\n",
		"OOMPolicy=continue\n",
	}
	root := repoRoot(t)
	for _, path := range []string{
		filepath.Join(root, "deploy", "systemd", "faas-apid.service"),
		filepath.Join(root, "deploy", "ansible", "roles", "control_plane_service", "files", "faas-apid.service"),
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range want {
			if !strings.Contains(string(body), line) {
				t.Errorf("%s lacks %q; run make generate", path, strings.TrimSpace(line))
			}
		}
		if !strings.Contains(string(body), "faas-apid.socket") {
			t.Errorf("%s does not require faas-apid.socket: a restart fallback would refuse API connections", path)
		}
	}
}

// The cap holds two generations at their soft limits, and the hand-off
// threshold sits above apid's measured steady state, so a reload outside a
// spike is never refused (production, 14 days to 2026-10-18).
func TestAPIDMemoryFitsTwoGenerations(t *testing.T) {
	const measuredSteadyMaxMB = 94
	if 2*api.APIDGoMemLimitMB > api.APIDMemoryMaxMB {
		t.Errorf("two generations at GOMEMLIMIT %d MiB exceed MemoryMax %d MB", api.APIDGoMemLimitMB, api.APIDMemoryMaxMB)
	}
	if threshold := api.APIDMemoryMaxMB * api.HandoffMaxMemoryPercent / 100; threshold < 2*measuredSteadyMaxMB {
		t.Errorf("hand-off threshold %d MB leaves no room above the measured %d MB steady state", threshold, measuredSteadyMaxMB)
	}
}

// The rc.344 deploy (2026-11-15) found apid's cgroup at 244 MB after the
// memory wait, above the 230 MB threshold of a 512 MB cap: the hand-off was
// refused and apid restarted, a ~9 s API gap that failed the rollout gate.
// The threshold must clear that reading with room for a successor spiking
// like the largest observed run, and the cap must still hold both
// (ADR-20261116090000).
func TestAPIDHandoffThresholdClearsObservedWorkingSet(t *testing.T) {
	const (
		observedAtDeployMB = 244 // rc.344, deployctl's refusal
		largestSpikeMB     = 257 // ADR 20261018132058
	)
	threshold := api.APIDMemoryMaxMB * api.HandoffMaxMemoryPercent / 100
	if threshold < observedAtDeployMB*3/2 {
		t.Errorf("hand-off threshold %d MB is within 1.5x of the %d MB apid used at the rc.344 deploy", threshold, observedAtDeployMB)
	}
	if threshold+largestSpikeMB > api.APIDMemoryMaxMB {
		t.Errorf("a predecessor at the %d MB threshold plus a %d MB successor spike exceed MemoryMax %d MB", threshold, largestSpikeMB, api.APIDMemoryMaxMB)
	}
}
