package e2e_test

// Every phase has TWO time budgets, and they must agree.
//
//   - the inner one, `go test -timeout`, from phase_timeout in
//     scripts/ci/run-native-e2e.sh
//   - the outer one, systemd's RuntimeMaxSec, in .github/workflows/e2e-native.yml
//
// If the outer is not strictly larger, systemd SIGTERMs the unit before `go
// test` can hit its own alarm, and the phase dies with exit 143 having printed
// no tally, no FAIL lines and no panic trace — nothing to diagnose. The Verdict
// then reports a phase that never reported itself.
//
// That is exactly what #2694 caused: it raised streaming's inner budget to 60m
// and left the outer at 30m, so gate run 35045370846 killed the streaming phase
// halfway with
//
//	native e2e: FAIL (143); services restored and staging removed
//
// The two numbers live in two files, so nothing connected them. This test does.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/e2etest"
)

func phaseOuterBudgets(t *testing.T) map[string]time.Duration {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "e2e-native.yml"))
	if err != nil {
		t.Fatalf("read e2e-native workflow: %v", err)
	}
	phaseRe := regexp.MustCompile(`PHASE:\s*(\w+)`)
	maxRe := regexp.MustCompile(`RuntimeMaxSec=(\w+)`)

	out := map[string]time.Duration{}
	current := ""
	for _, line := range strings.Split(string(body), "\n") {
		if m := phaseRe.FindStringSubmatch(line); m != nil {
			current = m[1]
		}
		if m := maxRe.FindStringSubmatch(line); m != nil && current != "" {
			d, perr := time.ParseDuration(m[1])
			if perr != nil {
				t.Fatalf("phase %s: unparseable RuntimeMaxSec %q: %v", current, m[1], perr)
			}
			out[current] = d
		}
	}
	return out
}

func phaseInnerBudgets(t *testing.T) map[string]time.Duration {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ci", "run-native-e2e.sh"))
	if err != nil {
		t.Fatalf("read run-native-e2e.sh: %v", err)
	}
	// e.g.  `streaming) phase_timeout=60m ;;`  or  `twonode | deploy) phase_timeout=25m ;;`
	re := regexp.MustCompile(`(?m)^\s*([a-z|\s]+)\)\s*phase_timeout=(\w+)\s*;;`)

	out := map[string]time.Duration{}
	for _, m := range re.FindAllStringSubmatch(string(body), -1) {
		d, perr := time.ParseDuration(m[2])
		if perr != nil {
			t.Fatalf("unparseable phase_timeout %q: %v", m[2], perr)
		}
		for _, name := range strings.Split(m[1], "|") {
			name = strings.TrimSpace(name)
			if name == "" || name == "*" {
				continue
			}
			out[name] = d
		}
	}
	return out
}

func TestEveryPhaseOuterBudgetOutlastsItsInnerOne(t *testing.T) {
	outer := phaseOuterBudgets(t)
	inner := phaseInnerBudgets(t)

	if len(outer) == 0 {
		t.Fatal("parsed no RuntimeMaxSec values from the workflow")
	}
	if len(inner) == 0 {
		t.Fatal("parsed no phase_timeout values from run-native-e2e.sh")
	}

	// The default arm covers every phase without an explicit case.
	def, ok := inner["default"]
	if !ok {
		def = 15 * time.Minute
	}

	for phase, out := range outer {
		in, named := inner[phase]
		if !named {
			in = def
		}
		if out <= in {
			t.Errorf("phase %s: RuntimeMaxSec=%s does not outlast go test -timeout=%s. "+
				"systemd would SIGTERM the unit first and the phase would exit 143 "+
				"with no tally and no panic trace.", phase, out, in)
		}
	}
}

// The phases whose budgets this pair of files disagreed about, pinned by name
// so a future edit to either file trips here rather than on hardware an hour in.
func TestSourceBuildingPhasesHaveABuildSizedBudget(t *testing.T) {
	inner := phaseInnerBudgets(t)

	// build, streaming and deploy each run real builder microVMs; the platform
	// caps one build at api.BuildTimeoutSeconds (900s).
	for _, phase := range []string{"build", "streaming", "deploy"} {
		got, ok := inner[phase]
		if !ok {
			t.Errorf("phase %s has no explicit phase_timeout; it would inherit the "+
				"short default despite running real builds", phase)
			continue
		}
		if got < 2*e2etest.DefaultBuildCeiling {
			t.Errorf("phase %s budget %s leaves room for fewer than two builds at %s each",
				phase, got, e2etest.DefaultBuildCeiling)
		}
	}
}

// The job timeout must outlast the SUM of every phase's outer cap. When the
// job hits timeout-minutes GitHub kills it outright and skips the remaining
// steps — always() included — so the Verdict never runs and the acceptance
// node is never stopped. Run 35115616655 hit 150m against 325m of caps: no
// verdict, and the node billed until the next dispatch happened to find it.
func TestJobTimeoutCoversEveryPhaseCap(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "e2e-native.yml"))
	if err != nil {
		t.Fatalf("read e2e-native workflow: %v", err)
	}
	m := regexp.MustCompile(`(?m)^\s*timeout-minutes:\s*(\d+)`).FindStringSubmatch(string(body))
	if m == nil {
		t.Fatal("no job timeout-minutes in the workflow; a wedged run would hold the node until GitHub's 6h default")
	}
	jobTimeout, _ := time.ParseDuration(m[1] + "m")

	// A lane never runs in the same job as the phases (the steps are gated on
	// inputs.lane either way), so it must not be added to their sum — but it
	// must fit on its own.
	lanes := map[string]bool{"smoke": true}
	var sum, laneMax time.Duration
	for phase, d := range phaseOuterBudgets(t) {
		if lanes[phase] {
			if d > laneMax {
				laneMax = d
			}
			continue
		}
		sum += d
	}
	if sum < laneMax {
		sum = laneMax
	}
	// Staging, source transfer, log collection and the node stop itself.
	const overhead = 20 * time.Minute
	if jobTimeout < sum+overhead {
		t.Errorf("job timeout-minutes=%s does not cover the phase caps (%s) plus %s overhead. "+
			"A run that uses its budgets is killed before Verdict and before the node is stopped.",
			jobTimeout, sum, overhead)
	}
	// GitHub-hosted runners cap a job at 6h; a larger value is silently clamped.
	if jobTimeout > 6*time.Hour {
		t.Errorf("job timeout-minutes=%s exceeds GitHub's 6h job ceiling and would be clamped", jobTimeout)
	}
}

// The smoke lane and the full phases are mutually exclusive within one run:
// every full-phase step is gated off when lane == smoke, and the smoke step
// is gated on when it is. A step missing its gate would run the matrix on a
// smoke dispatch and bring the hour back.
func TestSmokeLaneAndFullPhasesAreExclusive(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "e2e-native.yml"))
	if err != nil {
		t.Fatalf("read e2e-native workflow: %v", err)
	}
	wf := string(body)
	if !strings.Contains(wf, "default: smoke") {
		t.Error("the lane input does not default to smoke; a bare dispatch would run the hour-long matrix")
	}
	full := strings.Count(wf, "&& inputs.lane != 'smoke'")
	if full != 9 {
		t.Errorf("%d full-phase steps are gated on lane != smoke, want 9 (one per phase)", full)
	}
	if strings.Count(wf, "&& inputs.lane == 'smoke'") != 1 {
		t.Error("expected exactly one step gated on lane == smoke")
	}
	if !strings.Contains(wf, "smoke=${{ steps.phase_smoke.outcome }}") {
		t.Error("the Verdict does not see the smoke step's outcome; a red smoke run would report green")
	}
}

func TestFullLaneRunsFullAPIHostingRuntimeCatalog(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "e2e-native.yml"))
	if err != nil {
		t.Fatalf("read e2e-native workflow: %v", err)
	}
	wf := string(body)
	start := strings.Index(wf, "- name: Phase 2 — build")
	if start < 0 {
		t.Fatal("workflow has no source-build phase")
	}
	end := strings.Index(wf[start:], "- name: Phase 3 — deploy")
	if end < 0 {
		t.Fatal("workflow has no deploy phase after source-build phase")
	}
	buildPhase := wf[start : start+end]
	if !strings.Contains(buildPhase, "CATALOG_MODE: ${{ inputs.lane == 'qualify' && 'qualify' || 'full' }}") {
		t.Error("native build phase does not select full fixtures for the full lane and candidates for qualify")
	}
	if !strings.Contains(buildPhase, "--setenv=FAAS_E2E_API_HOSTING_CATALOG='$CATALOG_MODE'") {
		t.Error("native build phase does not forward its selected catalog mode to the remote runner")
	}
}
