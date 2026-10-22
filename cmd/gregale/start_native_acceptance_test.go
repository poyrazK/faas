package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestStartNativeFixtureDetection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the native Start terminal runner requires a Unix host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required to verify the native Start fixtures")
	}
	root := filepath.Join(t.TempDir(), "source")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	code := `import pathlib, runpy, sys
m = runpy.run_path('../../scripts/ci/start-native.py')
m['prepare_sources'](pathlib.Path(sys.argv[1]), 'start-contract', 'portable')
m['write_fixture'](pathlib.Path(sys.argv[1]) / 'apps/start-contract-private-named', 'configuration', configuration_digest='a' * 64)`
	if output, err := exec.CommandContext(ctx, python, "-c", code, root).CombinedOutput(); err != nil {
		t.Fatalf("generate live fixture sources: %v\n%s", err, output)
	}
	plan, err := reposcan.Scan(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"public-default": false, "private-default": false, "public-named": false, "private-named": false}
	if len(plan.Workloads) != len(want) {
		t.Fatalf("fixture scanner produced %d workloads, want %d: %+v", len(plan.Workloads), len(want), plan.Workloads)
	}
	for _, workload := range plan.Workloads {
		name := strings.TrimPrefix(workload.Name, "start-contract-")
		seen, found := want[name]
		if !found || seen || workload.RootDir != "apps/"+workload.Name || workload.Image != "" || workload.Dockerfile != "" {
			t.Fatalf("unexpected fixture build target: %+v", workload)
		}
		want[name] = true
		required, _, configErr := main.StartRequiredConfiguration(filepath.Join(root, workload.RootDir))
		expected := []string{}
		if name == "private-named" {
			expected = append(expected, "START_NATIVE_REQUIRED")
		}
		if configErr != nil || !slices.Equal(required, expected) {
			t.Fatalf("fixture runtime requirements differ: case=%s required=%v err=%v", name, required, configErr)
		}
	}
}
