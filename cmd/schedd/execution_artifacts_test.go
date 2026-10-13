package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/storage"
)

func executionArtifactsTestStorage(t *testing.T) storage.StorageBackend {
	t.Helper()
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	return backend
}

func putTestObject(t *testing.T, backend storage.StorageBackend, key, value string) {
	t.Helper()
	if err := backend.Put(context.Background(), key, strings.NewReader(value)); err != nil {
		t.Fatalf("Put %s: %v", key, err)
	}
}

const (
	testConfigDigest = "sha256:" + "aa" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testGuestInit    = "bb" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func stageExecutionArtifacts(t *testing.T, backend storage.StorageBackend, runtime, profile, arch string) {
	t.Helper()
	putTestObject(t, backend, sched.KernelKey("1.10.1"), "kernel-bytes")
	putTestObject(t, backend, sched.BaseKeyForArch(sched.ExecutionBaseRuntimeForProfile(runtime, profile), arch), "base")
	putTestObject(t, backend, sched.BaseDigestKeyForArch(sched.ExecutionBaseRuntimeForProfile(runtime, profile), arch),
		testConfigDigest+"\nfaas-base-layout-v3\nguest-init-sha256="+testGuestInit+"\nsource-ref=ghcr.io/x/y@sha256:"+strings.Repeat("c", 64))
	putTestObject(t, backend, sched.ExecutionLayerKeyForArch(profile, arch), "layer")
	putTestObject(t, backend, sched.ExecutionLayerDigestKeyForArch(profile, arch), "sha256:"+strings.Repeat("d", 64)+"\n")
}

func TestStorageExecutionArtifactsDeriveFromStagedObjects(t *testing.T) {
	backend := executionArtifactsTestStorage(t)
	stageExecutionArtifacts(t, backend, "node22", "standard", "amd64")
	resolver := newStorageExecutionArtifacts(backend, "1.10.1", "amd64", func(string) string { return "" })

	shape := api.ExecutionSnapshotShape{Profile: api.ExecutionProfileStandard, Runtime: api.ExecutionRuntimeNode22, MemoryMB: 128, EphemeralDiskMB: 64}
	got, err := resolver.resolve(context.Background(), api.ExecutionRuntimeNode22, shape)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	kernelSum := sha256.Sum256([]byte("kernel-bytes"))
	want := sched.ExecutionRuntimeArtifacts{
		Profile: api.ExecutionProfileStandard, Architecture: "amd64",
		KernelDigest: hex.EncodeToString(kernelSum[:]), GuestExecutorDigest: testGuestInit,
		BaseImageDigest: strings.TrimPrefix(testConfigDigest, "sha256:"),
		KernelKey:       "kernel/1.10.1", BaseKey: "base/runner-node22-amd64.ext4",
		LayerKey: "execution/layer-v1-standard-amd64.ext4", FCVersion: "1.10.1",
	}
	if got != want {
		t.Fatalf("artifacts = %+v\nwant %+v", got, want)
	}
	// The resolved artifacts must pass the scheduler's own identity checks.
	if _, err := (sched.StaticExecutionRuntimeArtifacts{api.ExecutionRuntimeNode22: got}).ResolveExecutionArtifacts(context.Background(), api.ExecutionRuntimeNode22, shape); err != nil {
		t.Fatalf("scheduler rejected derived artifacts: %v", err)
	}
}

func TestStorageExecutionArtifactsProfileUsesProfileBase(t *testing.T) {
	backend := executionArtifactsTestStorage(t)
	stageExecutionArtifacts(t, backend, "python313", "python-data-v1", "amd64")
	resolver := newStorageExecutionArtifacts(backend, "1.10.1", "amd64", func(string) string { return "" })
	got, err := resolver.resolve(context.Background(), api.ExecutionRuntimePython313, api.ExecutionSnapshotShape{
		Profile: api.ExecutionProfilePythonDataV1, Runtime: api.ExecutionRuntimePython313, MemoryMB: 256, EphemeralDiskMB: 64,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.BaseKey != "base/runner-python-data-v1-amd64.ext4" || got.LayerKey != "execution/layer-v1-python-data-v1-amd64.ext4" {
		t.Fatalf("profile artifacts = %+v", got)
	}
}

func TestStorageExecutionArtifactsFailClosedWhenNotStaged(t *testing.T) {
	shape := api.ExecutionSnapshotShape{Profile: api.ExecutionProfileStandard, Runtime: api.ExecutionRuntimeNode24, MemoryMB: 128, EphemeralDiskMB: 64}
	for _, tc := range []struct {
		name      string
		fcVersion string
		stage     func(storage.StorageBackend)
	}{
		{name: "unknown fc version", fcVersion: "", stage: func(b storage.StorageBackend) { stageExecutionArtifacts(t, b, "node24", "standard", "amd64") }},
		{name: "no base", fcVersion: "1.10.1", stage: func(b storage.StorageBackend) {
			putTestObject(t, b, sched.KernelKey("1.10.1"), "k")
			putTestObject(t, b, sched.ExecutionLayerDigestKeyForArch("standard", "amd64"), "x")
		}},
		{name: "no layer", fcVersion: "1.10.1", stage: func(b storage.StorageBackend) {
			putTestObject(t, b, sched.KernelKey("1.10.1"), "k")
			putTestObject(t, b, sched.BaseDigestKeyForArch("node24", "amd64"), testConfigDigest+"\nfaas-base-layout-v3\nguest-init-sha256="+testGuestInit)
		}},
		{name: "legacy sidecar without guest-init", fcVersion: "1.10.1", stage: func(b storage.StorageBackend) {
			putTestObject(t, b, sched.KernelKey("1.10.1"), "k")
			putTestObject(t, b, sched.BaseDigestKeyForArch("node24", "amd64"), testConfigDigest+"\nfaas-base-layout-v3")
			putTestObject(t, b, sched.ExecutionLayerDigestKeyForArch("standard", "amd64"), "x")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := executionArtifactsTestStorage(t)
			tc.stage(backend)
			resolver := newStorageExecutionArtifacts(backend, tc.fcVersion, "amd64", func(string) string { return "" })
			_, err := resolver.resolve(context.Background(), api.ExecutionRuntimeNode24, shape)
			if !errors.Is(err, sched.ErrExecutionRuntimeArtifactsUnavailable) {
				t.Fatalf("resolve error = %v, want ErrExecutionRuntimeArtifactsUnavailable", err)
			}
		})
	}
}

func TestStorageExecutionArtifactsEnvOverrideWins(t *testing.T) {
	env := map[string]string{"FAAS_EXECUTION_NODE22_KERNEL_DIGEST": strings.Repeat("1", 64)}
	resolver := newStorageExecutionArtifacts(executionArtifactsTestStorage(t), "1.10.1", "amd64", func(key string) string { return env[key] })
	_, err := resolver.resolve(context.Background(), api.ExecutionRuntimeNode22, api.ExecutionSnapshotShape{
		Profile: api.ExecutionProfileStandard, Runtime: api.ExecutionRuntimeNode22, MemoryMB: 128, EphemeralDiskMB: 64,
	})
	// A partial explicit override is the legacy env contract: it must be
	// rejected as incomplete rather than silently mixed with storage values.
	if err == nil || !strings.Contains(err.Error(), "incomplete execution artifact release metadata") {
		t.Fatalf("partial env override error = %v", err)
	}
}

func TestParseBaseDigestSidecar(t *testing.T) {
	for _, tc := range []struct {
		name, sidecar string
		ok            bool
	}{
		{"current", testConfigDigest + "\nfaas-base-layout-v3\nguest-init-sha256=" + testGuestInit, true},
		{"with source ref", testConfigDigest + "\nfaas-base-layout-v3\nguest-init-sha256=" + testGuestInit + "\nsource-ref=x", true},
		{"no guest-init", testConfigDigest + "\nfaas-base-layout-v3", false},
		{"bad digest", "sha256:xyz\nfaas-base-layout-v3\nguest-init-sha256=" + testGuestInit, false},
		{"bad guest-init", testConfigDigest + "\nfaas-base-layout-v3\nguest-init-sha256=zz", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, guestInit, ok := parseBaseDigestSidecar(tc.sidecar)
			if ok != tc.ok || (ok && (config != testConfigDigest || guestInit != testGuestInit)) {
				t.Fatalf("parse = %q %q %v, want ok=%v", config, guestInit, ok, tc.ok)
			}
		})
	}
}
