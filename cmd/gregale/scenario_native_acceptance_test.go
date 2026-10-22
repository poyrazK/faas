package main_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestScenarioNativeCancellationHelper(t *testing.T) {
	if os.Getenv("GREGALE_NATIVE_ACCEPTANCE_CANCEL_HELPER") != "1" {
		return
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestScenarioCommandCancellationRecordsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := main.RunTestCommand(ctx, t.TempDir(), append(os.Environ(), "GREGALE_NATIVE_ACCEPTANCE_CANCEL_HELPER=1"), []string{os.Args[0], "-test.run=^TestScenarioNativeCancellationHelper$"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("command cancellation lost its context cause: %v", err)
	}
}

func TestScenarioNativeAcceptanceHarness(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is needed for native acceptance harness tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "../../scripts/ci/scenario-restart-native_test.py")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("portable native harness contracts: %v\n%s", err, output)
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	variants := t.TempDir()
	code := `import pathlib, runpy, sys
root = pathlib.Path(sys.argv[1])
module = runpy.run_path(str(root / 'scripts/ci/scenario-restart-native.py'))
module['manifests'](root, pathlib.Path(sys.argv[2]), 'native-checkout')`
	if output, err := exec.CommandContext(ctx, python, "-c", code, repo, variants).CombinedOutput(); err != nil {
		t.Fatalf("generate native manifests: %v\n%s", err, output)
	}
	for _, mode := range []string{"tcp-flight", "tcp-seq", "pool-full", "pool-queue", "req-timeout", "req-body", "conn-gauge", "tcp-cancel", "req-bounded", "req-outlier", "req-no-id", "req-no-ack", "success", "failure", "cancel", "batch-success", "batch-failure", "batch-cancel", "pending-healthy", "pending-stuck", "flight-before", "flight-pending", "flight-after", "dep-pending", "dep-after", "auth-stall", "commit-loss", "commit-request", "commit-response", "bandwidth", "read-cut", "ssl-request", "retry-budget", "circuit-breaker", "bulkhead", "combined", "repeated", "isolation"} {
		manifest, dir, err := main.ReadTestManifestDocument(filepath.Join(variants, mode+".yaml"), func(string, main.TestScenario) []string { return nil })
		if err != nil {
			t.Fatalf("native %s manifest: %v", mode, err)
		}
		if _, err := main.CollectTestValidation(manifest.Scenarios, dir, ""); err != nil {
			t.Fatalf("native %s source validation: %v", mode, err)
		}
	}
}
