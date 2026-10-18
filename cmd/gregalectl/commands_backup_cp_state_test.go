// adr: 1344 — control-plane state backup CLI (§8).
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestBackupCPStateSealAndRestore(t *testing.T) {
	dir := t.TempDir()
	secrets := filepath.Join(dir, "etc", "faas", "secrets")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "session.key"), []byte("k"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "host.age"), []byte("AGE-SECRET-KEY-1"), 0o400); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(dir, "paths")
	if err := os.WriteFile(spec, []byte(secrets+"\n!"+filepath.Join(secrets, "host.age")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	operator, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	recipients := filepath.Join(dir, "recipients")
	if err := os.WriteFile(recipients, []byte(operator.Recipient().String()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(dir, "operator.key")
	if err := os.WriteFile(identity, []byte(operator.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	bundle := filepath.Join(dir, "cp-state.tar.age")
	_, restore := captureSizingStdout(t)
	code := cmdBackup([]string{subSealCPState, "--paths", spec, "--recipients", recipients, "--out", bundle, "--primary", "cp-1"})
	out := restore()
	if code != 0 || !strings.Contains(out, "sealed 1 files") {
		t.Fatalf("seal exit %d, output %q", code, out)
	}
	if info, err := os.Stat(bundle); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("bundle mode %v, err %v; want 0600", info.Mode(), err)
	}

	stage := filepath.Join(dir, "stage")
	_, restore = captureSizingStdout(t)
	code = cmdBackup([]string{subRestoreCPState, "--identity", identity, "--in", bundle, "--stage", stage})
	out = restore()
	if code != 0 || !strings.Contains(out, "staged 1 files from cp-1") {
		t.Fatalf("restore exit %d, output %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(stage, "files", secrets, "session.key.b64")); err != nil {
		t.Fatalf("session.key not staged: %v", err)
	}

	if code := cmdBackup([]string{subSealCPState, "--paths", spec, "--recipients", recipients}); code != 2 {
		t.Errorf("seal without --out exit %d, want 2", code)
	}
	if code := cmdBackup([]string{subRestoreCPState, "--in", bundle}); code != 2 {
		t.Errorf("restore without --identity exit %d, want 2", code)
	}
}
