package e2e_test

// The gate must start from a clean slate it made itself, not refuse to start
// over a previous run's leftovers. Smoke run 35206846279 executed zero tests
// because two app-instance jail chroots from the run before it (instances
// destroyed mid-wake) survived a node STOP/START — /srv/fc/jail is not tmpfs
// on the acceptance host — and the pre-flight leakcheck saw them first.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func readCIScript(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ci", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestRunnerReapsStaleJailsBeforePreflightLeakcheck(t *testing.T) {
	runner := readCIScript(t, "run-native-e2e.sh")

	if !regexp.MustCompile(`(?m)^source "\$\{repo_root\}/scripts/ci/native-e2e-reap\.sh"`).MatchString(runner) {
		t.Error("run-native-e2e.sh does not source native-e2e-reap.sh; reap_stale_jails is undefined at runtime")
	}
	// The pre-flight leakcheck is the bare `bash .../leakcheck.sh` line (the
	// cleanup one is inside an `if !`). A reap must come before it.
	pre := regexp.MustCompile(`(?m)^reap_test_microvms\n(?:#[^\n]*\n)*bash "\$\{repo_root\}/deploy/scripts/leakcheck\.sh"$`)
	if !pre.MatchString(runner) {
		t.Error("the pre-flight leakcheck is not preceded by reap_test_microvms; a previous run's " +
			"leftover chroots would turn this run into \"no test executed\"")
	}
	if !regexp.MustCompile(`reap_stale_jails "\$\{FAAS_E2E_JAIL_ROOT:-/srv/fc/jail\}"`).MatchString(runner) {
		t.Error("reap_test_microvms does not call reap_stale_jails on the jail root")
	}
}

// The reaper must remove app-instance chroots, not only build-* ones: the
// two that blocked run 35206846279 were app instances.
func TestReapStaleJailsRemovesAppInstanceChroots(t *testing.T) {
	lib := readCIScript(t, "native-e2e-reap.sh")
	if !regexp.MustCompile(`for d in "\$\{root\}"/firecracker-v\*/\*/; do`).MatchString(lib) {
		t.Error("reap_stale_jails does not iterate every chroot under firecracker-v*/; " +
			"app-instance chroots would be left for the pre-flight leakcheck to refuse")
	}
	if regexp.MustCompile(`firecracker-v\*/build-\*/`).MatchString(lib) {
		t.Error("reap_stale_jails is back to build-* only")
	}
}
