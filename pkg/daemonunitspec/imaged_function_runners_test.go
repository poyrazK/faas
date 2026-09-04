package daemonunitspec

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// wantFunctionRunnerEnv is the closed set of per-runtime function runner
// paths imaged needs. Mirrors the keyword table in cmd/imaged/main.go
// (the `for _, kw := range []struct{ envKey, runtime string ... }` block)
// and the directories emitted by
// deploy/packer/scripts/compile-runners.sh.
var wantFunctionRunnerEnv = map[string]string{
	"FAAS_FUNCTION_RUNNER_NODE22":       "/opt/faas/current/bin/runners/node22/faas-runner",
	"FAAS_FUNCTION_RUNNER_NODE24":       "/opt/faas/current/bin/runners/node24/faas-runner",
	"FAAS_FUNCTION_RUNNER_PYTHON312":    "/opt/faas/current/bin/runners/python312/faas-runner",
	"FAAS_FUNCTION_RUNNER_PYTHON313":    "/opt/faas/current/bin/runners/python313/faas-runner",
	"FAAS_FUNCTION_RUNNER_GO124":        "/opt/faas/current/bin/runners/go124/faas-runner",
	"FAAS_FUNCTION_RUNNER_GO124_ALPINE": "/opt/faas/current/bin/runners/go124-alpine/faas-runner",
}

// TestUnitImaged_SetsEveryFunctionRunnerPath is the regression guard for a
// platform-wide outage: the runner binaries shipped in the image and
// cmd/imaged read FAAS_FUNCTION_RUNNER_<RUNTIME> since M6, but nothing
// ever SET the variables. Every function deploy therefore failed at build
// time on every node with "function runner binary not configured for
// runtime X", and apps that could not redeploy eventually lost their live
// deployment and 404'd.
func TestUnitImaged_SetsEveryFunctionRunnerPath(t *testing.T) {
	got := map[string]string{}
	for _, kv := range UnitImaged().Environment {
		if strings.HasPrefix(kv.Key, "FAAS_FUNCTION_RUNNER_") {
			got[kv.Key] = kv.Value
		}
	}
	for key, want := range wantFunctionRunnerEnv {
		if got[key] != want {
			t.Errorf("%s = %q, want %q — imaged refuses a function build for any runtime whose runner path is unset", key, got[key], want)
		}
	}
	for key := range got {
		if _, ok := wantFunctionRunnerEnv[key]; !ok {
			t.Errorf("unexpected %s — add it to cmd/imaged/main.go's keyword table and to wantFunctionRunnerEnv, or drop it", key)
		}
	}
}

// TestUnitImaged_FunctionRunnerEnvMatchesImagedSource keeps the unit and
// the consumer in lockstep. A new runtime added to cmd/imaged's table
// without a matching Environment entry reproduces the original bug for
// that runtime only — silently, since nothing else references the pair.
func TestUnitImaged_FunctionRunnerEnvMatchesImagedSource(t *testing.T) {
	src, err := os.ReadFile("../../cmd/imaged/main.go")
	if err != nil {
		t.Skipf("cannot read cmd/imaged/main.go: %v", err)
	}
	re := regexp.MustCompile(`"(FAAS_FUNCTION_RUNNER_[A-Z0-9_]+)"`)
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		seen[m[1]] = true
	}
	if len(seen) == 0 {
		t.Skip("no FAAS_FUNCTION_RUNNER_* keys found in cmd/imaged/main.go")
	}
	var missing []string
	for key := range seen {
		if _, ok := wantFunctionRunnerEnv[key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("cmd/imaged reads %v but UnitImaged() never sets them; function deploys for those runtimes will fail at build time", missing)
	}
}
