package main

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestExecutionDispatchEnabled(t *testing.T) {
	for value, want := range map[string]bool{
		"1": true, " 1 ": true, "": false, "0": false, "true": false,
	} {
		if got := executionDispatchEnabled(value); got != want {
			t.Errorf("executionDispatchEnabled(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestExecutionRuntimeArtifactsFromEnv(t *testing.T) {
	runtimeID := api.ExecutionRuntimeNode22
	prefix := "FAAS_EXECUTION_NODE22_"
	shape := api.ExecutionSnapshotShape{Runtime: runtimeID, MemoryMB: 128, EphemeralDiskMB: 64}
	for _, field := range []string{"KERNEL_DIGEST", "EXECUTOR_DIGEST", "BASE_DIGEST", "KERNEL_KEY", "BASE_KEY", "LAYER_KEY", "FC_VERSION"} {
		t.Setenv(prefix+field, map[string]string{
			"KERNEL_DIGEST":   strings.Repeat("a", 64),
			"EXECUTOR_DIGEST": strings.Repeat("b", 64),
			"BASE_DIGEST":     strings.Repeat("c", 64),
			"KERNEL_KEY":      "kernel/1.10.0",
			"BASE_KEY":        "base/runner-node22-amd64.ext4",
			"LAYER_KEY":       "layers/execution-node22-amd64.ext4",
			"FC_VERSION":      "1.10.0",
		}[field])
	}
	t.Setenv(prefix+"ARCH", runtime.GOARCH)
	got, err := executionRuntimeArtifactsFromEnv(context.Background(), runtimeID, shape)
	if err != nil {
		t.Fatalf("executionRuntimeArtifactsFromEnv: %v", err)
	}
	if got.KernelDigest != strings.Repeat("a", 64) || got.LayerKey != "layers/execution-node22-amd64.ext4" || got.Architecture != runtime.GOARCH {
		t.Fatalf("artifacts = %+v", got)
	}
}

func TestExecutionRuntimeArtifactsFromEnvFailsClosed(t *testing.T) {
	t.Setenv("FAAS_EXECUTION_NODE22_ARCH", runtime.GOARCH)
	_, err := executionRuntimeArtifactsFromEnv(context.Background(), api.ExecutionRuntimeNode22, api.ExecutionSnapshotShape{Runtime: api.ExecutionRuntimeNode22})
	if err == nil || !strings.Contains(err.Error(), "KERNEL_DIGEST") {
		t.Fatalf("missing metadata error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = executionRuntimeArtifactsFromEnv(canceled, api.ExecutionRuntimeNode22, api.ExecutionSnapshotShape{Runtime: api.ExecutionRuntimeNode22})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error = %v", err)
	}
}

func TestRunWithDepsExecutionGateRequiresDecoder(t *testing.T) {
	t.Setenv("FAAS_EXECUTION_DISPATCH", "1")
	err := runWithDeps(context.Background(), slog.Default(), runDeps{
		configPath: "/dev/null",
		capCheck:   func() error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "authenticated payload decoder") {
		t.Fatalf("execution gate error = %v", err)
	}
}
