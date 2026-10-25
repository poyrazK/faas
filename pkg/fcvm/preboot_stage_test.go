// adr: 192, 20261025090100
// spec: §6.3
package fcvm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/guestdisk"
)

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

// TestStagePreBootFiles_SingleDriveSession pins ADR-192: every per-instance
// file lands on drive1 through ONE drive session, whatever the mix of
// secrets, API env, resolver and workload files.
func TestStagePreBootFiles_SingleDriveSession(t *testing.T) {
	sessions, mountRoot := fakeDriveImages(t)
	v := newStagingVMM(t, "inst-preboot")

	workloads := []WorkloadSpec{
		{Name: "main", Type: "app", RamMB: 256, Port: 8080, Essential: true},
		{Name: "metrics", Type: "sidecar", RamMB: 64, Port: 9100, preparedEnvJSON: []byte(`{"A":"1"}`)},
	}
	err := v.stagePreBootFiles("inst-preboot", workloads, []byte(`{"S":"x"}`), []byte(`{"K":"v"}`), "10.156.0.1", true)
	if err != nil {
		t.Fatalf("stagePreBootFiles: %v", err)
	}
	if *sessions != 1 {
		t.Fatalf("drive sessions = %d, want exactly 1", *sessions)
	}
	for _, rel := range []string{
		"upper/etc/faas/secrets.env",
		"upper/etc/faas/env.json",
		"upper/etc/resolv.conf",
		"upper/etc/faas/app-task.json",
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

// TestStagePreBootFiles_NothingToWrite_NoSession: an app with no secrets, no
// API env, no resolver and a single workload must not resolve drive1 or
// open a drive session at all — the per-file short-circuits are preserved
// in aggregate.
func TestStagePreBootFiles_NothingToWrite_NoSession(t *testing.T) {
	sessions, _ := fakeDriveImages(t)
	v := NewJailerVMM(t.TempDir(), 0) // no chroot, no drive1 on purpose
	if err := v.stagePreBootFiles("inst-empty", []WorkloadSpec{{Name: "main"}}, nil, nil, "", false); err != nil {
		t.Fatalf("stagePreBootFiles: %v", err)
	}
	if *sessions != 0 {
		t.Fatalf("drive sessions = %d, want 0", *sessions)
	}
}

// TestStagePreBootFiles_ValidationBeforeSession: input validation and
// byte-cap projection happen before any drive session so a rejected payload
// never costs one.
func TestStagePreBootFiles_ValidationBeforeSession(t *testing.T) {
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
			sessions, _ := fakeDriveImages(t)
			v := newStagingVMM(t, "inst-validate")
			err := v.stagePreBootFiles("inst-validate", tc.workloads, nil, nil, tc.resolverIP, false)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			if *sessions != 0 {
				t.Errorf("drive sessions = %d, want 0 on validation failure", *sessions)
			}
		})
	}
}

// TestStagePreBootFiles_WriteErrorNamesOperation: a failure inside the
// session is wrapped with the operation label the previous implementation
// used, so operator triage strings are unchanged.
func TestStagePreBootFiles_WriteErrorNamesOperation(t *testing.T) {
	v := newStagingVMM(t, "inst-fail")
	root := t.TempDir()
	// Make etc/faas a file so the secrets.env parent is not a directory.
	if err := os.MkdirAll(filepath.Join(root, "upper/etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "upper/etc/faas"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	withDriveDir(t, root)

	err := v.stagePreBootFiles("inst-fail", nil, []byte(`{"S":"x"}`), nil, "", false)
	if err == nil || !strings.HasPrefix(err.Error(), "stage secrets.env: ") {
		t.Fatalf("err = %v, want prefix %q", err, "stage secrets.env: ")
	}
	var pathErr *guestdisk.PathError
	if !errors.As(err, &pathErr) || !errors.Is(err, guestdisk.ErrNotDirectory) {
		t.Errorf("underlying error not preserved via %%w: %v", err)
	}
}

// TestPublicStageMethods_OneSessionEach: the legacy Manager path and callers
// outside Restore keep the one-file-per-session contract.
func TestPublicStageMethods_OneSessionEach(t *testing.T) {
	sessions, mountRoot := fakeDriveImages(t)
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
		t.Fatalf("drive sessions = %d, want 5 (one per public call)", *sessions)
	}
	if _, err := os.Stat(filepath.Join(*mountRoot, "upper/etc/faas/workloads/metrics/env.json")); err != nil {
		t.Errorf("sidecar env not written: %v", err)
	}
	// Empty payloads keep their no-op short-circuit.
	if err := v.StageSecretsEnv("inst-public", nil); err != nil || *sessions != 5 {
		t.Errorf("empty secrets: err=%v sessions=%d", err, *sessions)
	}
}
