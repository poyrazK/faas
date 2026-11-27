package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/runrelease"
)

func TestRunReleaseCommandNeverFallsThroughToDaemon(t *testing.T) {
	for _, args := range [][]string{{"run-release"}, {"run-release", "unknown"}, {"run-release", "preflight"}, {"run-release", "rollback", "--plan", "/absent/run-release-plan"}, {"run-release", "capabilities", "extra"}} {
		var out, stderr bytes.Buffer
		code, handled := runAdoptionCommand(args, &out, &stderr)
		if !handled || code != adoptionExitError {
			t.Fatalf("%v: code=%d handled=%v", args, code, handled)
		}
	}
	var out, stderr bytes.Buffer
	code, handled := runAdoptionCommand([]string{"run-release", "capabilities"}, &out, &stderr)
	if !handled || code != 0 {
		t.Fatalf("capabilities: %d %s", code, stderr.String())
	}
	var reader runrelease.Reader
	if err := json.Unmarshal(out.Bytes(), &reader); err != nil {
		t.Fatal(err)
	}
	if len(reader.BinarySHA256) != 64 || reader.TemplateFlight != 1 || reader.RuntimeCaptureAuthority != 2 || reader.KVMOnlyDeviceScope != 2 || reader.NativeLaunchOwner != 3 {
		t.Fatalf("incomplete reader: %+v", reader)
	}
}

func TestRunReleaseConfigUsesPinnedBytesAndEffectiveOverrides(t *testing.T) {
	// The same normalization as daemon startup chooses zero prepared networks
	// under native recovery; an explicit environment override remains visible.
	t.Setenv("FAAS_PREPARED_NETWORKS", "0")
	t.Setenv("FAAS_VMMD_RESOURCE_JOURNAL_DIR", "/private/run-release-resources")
	t.Setenv("FAAS_VMMD_RESOURCE_JOURNAL_VOLATILE_DIR", "")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "vmmd.toml")
	body := []byte("native_process_recovery = true\nnative_launch_owner_records = true\nnative_runtime_capture = true\nnative_snapshot_publication_root = '/private/run-release-publications'\nnative_image_staging_root = '/private/run-release-images'\nnative_runtime_snapshot_root = '/private/run-release-snapshots'\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	pin := runrelease.File{Path: path, SHA256: runrelease.SHA256(body)}
	cfg, err := loadRunReleaseConfig(t.Context(), pin)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.NativeRecovery || !cfg.RuntimeCapture || !cfg.LaunchOwners || cfg.PreparedNetworks != 0 || cfg.ResourceRoot != "/private/run-release-resources" || cfg.VolatileRoot != "" {
		t.Fatalf("wrong effective config: %+v", cfg)
	}
	t.Setenv("FAAS_PREPARED_NETWORKS", "1")
	cfg, err = loadRunReleaseConfig(t.Context(), pin)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PreparedNetworks != 1 {
		t.Fatal("environment override disappeared")
	}
	if err := os.WriteFile(path, append(body, []byte("# changed")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRunReleaseConfig(t.Context(), pin); err == nil {
		t.Fatal("changed config accepted")
	}
}

func TestRunReleaseInvalidConfigDoesNotPrintSecrets(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "bad.toml")
	secret := "private-config-token-must-stay-private"
	config := []byte(secret + " = [invalid\n")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	pin := runrelease.File{Path: configPath, SHA256: runrelease.SHA256(config)}
	plan := runrelease.Plan{CandidateConfig: pin, RollbackConfig: pin}
	body, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := runReleaseCommand([]string{"preflight", "--plan", path}, &out, &stderr); code != adoptionExitError {
		t.Fatalf("got %d", code)
	}
	if strings.Contains(out.String()+stderr.String(), secret) {
		t.Fatal("secret appeared in command output")
	}
}

func TestRunReleasePreparePinsInputsWithoutExecutingThem(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "input")
	body := []byte("not an executable; must only be hashed")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--source-commit", strings.Repeat("a", 40), "--candidate-reader", path, "--rollback-binary", path, "--rollback-reader", path, "--config", path, "--rollback-config", path, "--acceptance", path}
	for _, kind := range []string{"kernel", "base", "guest_executor", "firecracker", "jailer", "jail_helper"} {
		args = append(args, "--artifact-"+kind, path)
	}
	var out, stderr bytes.Buffer
	if code := prepareRunReleasePlan(t.Context(), args, path, &out, &stderr); code != 0 {
		t.Fatalf("prepare: %d %s", code, stderr.String())
	}
	var plan runrelease.Plan
	if err := runrelease.Decode(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Validate() != nil || plan.Candidate.File.SHA256 != runrelease.SHA256(body) || plan.Artifacts["base"].Path != path {
		t.Fatalf("wrong plan: %+v", plan)
	}
	// Building a plan does not approve the fake manifests or non-executable
	// inputs. Real preflight must still reject them.
	if plan.Candidate.File.Path != path {
		t.Fatal("candidate was replaced")
	}
}
