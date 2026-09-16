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
