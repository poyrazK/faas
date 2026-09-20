// adr: 192
// spec: §6.3
package fcvm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLoopMounts swaps loopMountSession for a plain directory so the
// pure-Go tier can count mount sessions and inspect what each one wrote.
// Returns the counter and a restore func for t.Cleanup.
func fakeLoopMounts(t *testing.T) (*int, *string) {
	t.Helper()
	sessions := 0
	root := t.TempDir()
	prev := loopMountSession
	loopMountSession = func(drive, prefix string, fn func(mountRoot string) error) error {
		if _, err := os.Stat(drive); err != nil {
			return err
		}
		sessions++
		return fn(root)
	}
	t.Cleanup(func() { loopMountSession = prev })
	return &sessions, &root
}

// newStagingVMM returns a JailerVMM whose chroot for instance already holds
// the canonical drive1 basename, mirroring the state Restore reaches right
// before stagePreBootFiles.
func newStagingVMM(t *testing.T, instance string) *JailerVMM {
	t.Helper()
	v := NewJailerVMM(t.TempDir(), 0)
	root := v.chrootRoot(instance)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, layerImageName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestStagePreBootFiles_SingleMountSession pins ADR-192: every per-instance
// file lands on drive1 through ONE loop-mount session, whatever the mix of
// secrets, API env, resolver and workload files.
func TestStagePreBootFiles_SingleMountSession(t *testing.T) {
	sessions, mountRoot := fakeLoopMounts(t)
	v := newStagingVMM(t, "inst-preboot")

	workloads := []WorkloadSpec{
		{Name: "main", Type: "app", RamMB: 256, Port: 8080, Essential: true},
		{Name: "metrics", Type: "sidecar", RamMB: 64, Port: 9100, preparedEnvJSON: []byte(`{"A":"1"}`)},
	}
	err := v.stagePreBootFiles("inst-preboot", workloads, []byte(`{"S":"x"}`), []byte(`{"K":"v"}`), "10.156.0.1")
	if err != nil {
		t.Fatalf("stagePreBootFiles: %v", err)
	}
	if *sessions != 1 {
		t.Fatalf("loop mount sessions = %d, want exactly 1", *sessions)
	}
	for _, rel := range []string{
		"upper/etc/faas/secrets.env",
		"upper/etc/faas/env.json",
		"upper/etc/resolv.conf",
		"upper/etc/faas/workloads/metrics/env.json",
		"upper/etc/faas/workload.json",
		"upper/etc/faas/workloads.json",
	} {
		if _, err := os.Stat(filepath.Join(*mountRoot, rel)); err != nil {
			t.Errorf("%s not written: %v", rel, err)
		}
	}
	resolver, _ := os.ReadFile(filepath.Join(*mountRoot, "upper/etc/resolv.conf"))
	if !strings.Contains(string(resolver), "nameserver 10.156.0.1") {
		t.Errorf("resolver contents = %q", resolver)
	}
	secrets, _ := os.ReadFile(filepath.Join(*mountRoot, "upper/etc/faas/secrets.env"))
	if string(secrets) != `{"S":"x"}` {
		t.Errorf("secrets.env = %q", secrets)
	}
}

// TestStagePreBootFiles_NothingToWrite_NoMount: an app with no secrets, no
// API env, no resolver and a single workload must not resolve drive1 or
// take a mount at all — the previous per-file short-circuits are preserved
// in aggregate.
func TestStagePreBootFiles_NothingToWrite_NoMount(t *testing.T) {
	sessions, _ := fakeLoopMounts(t)
	v := NewJailerVMM(t.TempDir(), 0) // no chroot, no drive1 on purpose
	if err := v.stagePreBootFiles("inst-empty", []WorkloadSpec{{Name: "main"}}, nil, nil, ""); err != nil {
		t.Fatalf("stagePreBootFiles: %v", err)
	}
	if *sessions != 0 {
		t.Fatalf("loop mount sessions = %d, want 0", *sessions)
	}
}

// TestStagePreBootFiles_ValidationBeforeMount: input validation and byte-cap
// projection happen before any mount so a rejected payload never costs a
// loop device.
func TestStagePreBootFiles_ValidationBeforeMount(t *testing.T) {
	cases := []struct {
		name       string
		workloads  []WorkloadSpec
		resolverIP string
		wantErr    string
	}{
		{
			name:       "public resolver ip rejected",
			workloads:  []WorkloadSpec{{Name: "main"}},
			resolverIP: "8.8.8.8",
			wantErr:    "stage service resolver",
		},
		{
			name: "invalid sidecar name rejected",
			workloads: []WorkloadSpec{
				{Name: "main"},
				{Name: "Bad Name", preparedEnvJSON: []byte(`{}`)},
			},
			wantErr: "invalid workload name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessions, _ := fakeLoopMounts(t)
			v := newStagingVMM(t, "inst-validate")
			err := v.stagePreBootFiles("inst-validate", tc.workloads, nil, nil, tc.resolverIP)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			if *sessions != 0 {
				t.Errorf("loop mount sessions = %d, want 0 on validation failure", *sessions)
			}
		})
	}
}

// TestStagePreBootFiles_WriteErrorNamesOperation: a failure inside the
// session is wrapped with the operation label the previous implementation
// used, so operator triage strings are unchanged.
func TestStagePreBootFiles_WriteErrorNamesOperation(t *testing.T) {
	v := newStagingVMM(t, "inst-fail")
	prev := loopMountSession
	loopMountSession = func(drive, prefix string, fn func(string) error) error {
		root := t.TempDir()
		// Make etc/faas a file so the secrets mkdir fails.
		if err := os.MkdirAll(filepath.Join(root, "upper/etc"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "upper/etc/faas"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		return fn(root)
	}
	t.Cleanup(func() { loopMountSession = prev })

	err := v.stagePreBootFiles("inst-fail", nil, []byte(`{"S":"x"}`), nil, "")
	if err == nil || !strings.HasPrefix(err.Error(), "stage secrets.env: ") {
		t.Fatalf("err = %v, want prefix %q", err, "stage secrets.env: ")
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Errorf("underlying error not preserved via %%w: %v", err)
	}
}

// TestPublicStageMethods_StillMountIndividually: the legacy Manager path
// and callers outside Restore keep the one-file-per-mount contract.
func TestPublicStageMethods_StillMountIndividually(t *testing.T) {
	sessions, mountRoot := fakeLoopMounts(t)
	v := newStagingVMM(t, "inst-public")
	if err := v.StageSecretsEnv("inst-public", []byte(`{"S":"1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := v.StageAPIEnv("inst-public", []byte(`{"A":"1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := v.StageWorkloadEnv("inst-public", "metrics", []byte(`{"B":"1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := v.StageWorkloadRoster("inst-public", WorkloadSpec{Name: "main"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := v.StageWorkloadManifest("inst-public", -1, WorkloadSpec{Name: "main"}); err != nil {
		t.Fatal(err)
	}
	if *sessions != 5 {
		t.Fatalf("loop mount sessions = %d, want 5 (one per public call)", *sessions)
	}
	if _, err := os.Stat(filepath.Join(*mountRoot, "upper/etc/faas/workloads/metrics/env.json")); err != nil {
		t.Errorf("sidecar env not written: %v", err)
	}
	// Empty payloads keep their no-op short-circuit.
	if err := v.StageSecretsEnv("inst-public", nil); err != nil || *sessions != 5 {
		t.Errorf("empty secrets: err=%v sessions=%d", err, *sessions)
	}
}
