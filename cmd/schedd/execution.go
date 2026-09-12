package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// executionDispatchEnabled is an exact opt-in. Execution remains disabled
// during rollout unless an operator explicitly sets the feature gate to 1.
func executionDispatchEnabled(value string) bool {
	return strings.TrimSpace(value) == "1"
}

// executionRuntimeArtifactsFromEnv reads release-owned runtime metadata. The
// values are deliberately split per runtime so a partial rollout cannot make
// one runtime accidentally reuse another runtime's kernel or guest executor.
// Digests and keys are mandatory; only the host architecture has a safe
// default because it is not caller-controlled and is fixed for this daemon.
//
// FAAS_EXECUTION_<RUNTIME>_<FIELD> fields:
//
//	ARCH, KERNEL_DIGEST, EXECUTOR_DIGEST, BASE_DIGEST, KERNEL_KEY,
//	BASE_KEY, LAYER_KEY, FC_VERSION
func executionRuntimeArtifactsFromEnv(ctx context.Context, runtimeID api.ExecutionRuntime, _ api.ExecutionSnapshotShape) (sched.ExecutionRuntimeArtifacts, error) {
	if err := ctx.Err(); err != nil {
		return sched.ExecutionRuntimeArtifacts{}, err
	}
	prefix := "FAAS_EXECUTION_" + strings.ToUpper(string(runtimeID))
	value := func(field string) string {
		return strings.TrimSpace(os.Getenv(prefix + "_" + field))
	}
	artifacts := sched.ExecutionRuntimeArtifacts{
		Architecture:        value("ARCH"),
		KernelDigest:        value("KERNEL_DIGEST"),
		GuestExecutorDigest: value("EXECUTOR_DIGEST"),
		BaseImageDigest:     value("BASE_DIGEST"),
		KernelKey:           value("KERNEL_KEY"),
		BaseKey:             value("BASE_KEY"),
		LayerKey:            value("LAYER_KEY"),
		FCVersion:           value("FC_VERSION"),
	}
	if artifacts.Architecture == "" {
		artifacts.Architecture = runtime.GOARCH
	}
	missing := make([]string, 0, 7)
	for field, fieldValue := range map[string]string{
		"KERNEL_DIGEST":   artifacts.KernelDigest,
		"EXECUTOR_DIGEST": artifacts.GuestExecutorDigest,
		"BASE_DIGEST":     artifacts.BaseImageDigest,
		"KERNEL_KEY":      artifacts.KernelKey,
		"BASE_KEY":        artifacts.BaseKey,
		"LAYER_KEY":       artifacts.LayerKey,
		"FC_VERSION":      artifacts.FCVersion,
	} {
		if fieldValue == "" {
			missing = append(missing, prefix+"_"+field)
		}
	}
	if len(missing) != 0 {
		return sched.ExecutionRuntimeArtifacts{}, fmt.Errorf("schedd: incomplete execution artifact release metadata for %s (missing %s)", runtimeID, strings.Join(missing, ", "))
	}
	return artifacts, nil
}
