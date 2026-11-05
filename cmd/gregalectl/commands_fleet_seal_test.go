package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/fleetseal"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestFleetSealInitWritesSecureMatchingPair(t *testing.T) {
	dir := t.TempDir()
	if err := runFleetSealInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	identity, err := secretbox.LoadHostKey(filepath.Join(dir, "fleet.age"))
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := secretbox.LoadRecipient(filepath.Join(dir, "fleet.age.pub"))
	if err != nil {
		t.Fatal(err)
	}
	if identity.Recipient().String() != recipient.String() {
		t.Fatal("fleet identity and recipient do not match")
	}
	if info, _ := os.Stat(filepath.Join(dir, "fleet.age")); info.Mode().Perm() != 0o400 {
		t.Fatalf("fleet.age mode=%#o, want 0400", info.Mode().Perm())
	}
	if err := runFleetSealInit([]string{"--dir", dir}); err == nil {
		t.Fatal("second init must refuse overwrite")
	}
}

func TestValidateFleetAgePairRejectsSubstitution(t *testing.T) {
	dir := t.TempDir()
	first, _ := age.GenerateX25519Identity()
	second, _ := age.GenerateX25519Identity()
	key := filepath.Join(dir, "fleet.age")
	pub := filepath.Join(dir, "fleet.age.pub")
	if err := os.WriteFile(key, []byte(first.String()), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pub, []byte(second.Recipient().String()), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := validateFleetAgePair(key, pub); err == nil {
		t.Fatal("mismatched fleet pair accepted")
	}
}

func TestWriteFleetSealMetricReadyAndFailed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "textfiles", "fleet.prom")
	report := fleetseal.VerificationReport{Ready: true, ClusterKID: "abc123"}
	if err := writeFleetSealMetric(path, report, true); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "faas_fleet_seal_domain_ready 1") || !strings.Contains(string(body), `kid="abc123"`) {
		t.Fatalf("unexpected ready metric:\n%s", body)
	}
	if err := writeFleetSealMetric(path, report, false); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(path)
	if !strings.Contains(string(body), "faas_fleet_seal_domain_ready 0") {
		t.Fatalf("unexpected failed metric:\n%s", body)
	}
}

// TestLoadFleetSealControlKey pins when fleet-seal reads the control
// seal-domain identity: the default only once the role staged it, and a
// staged key this process cannot reach fails rather than passing as absent.
func TestLoadFleetSealControlKey(t *testing.T) {
	dir := t.TempDir()
	prev := controlKeyDefault
	t.Cleanup(func() { controlKeyDefault = prev })
	controlKeyDefault = filepath.Join(dir, "control.age")

	if ids, err := loadFleetSealControlKey(""); err != nil || ids != nil {
		t.Fatalf("unstaged default = %v, %v; want nothing", ids, err)
	}
	if _, err := loadFleetSealControlKey(filepath.Join(dir, "missing.age")); err == nil {
		t.Fatal("a missing explicit --control-key loaded")
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(controlKeyDefault, []byte(id.String()), 0o400); err != nil {
		t.Fatal(err)
	}
	ids, err := loadFleetSealControlKey("")
	if err != nil || len(ids) != 1 || ids[0].Recipient().String() != id.Recipient().String() {
		t.Fatalf("staged default = %v, %v; want the control identity", ids, err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root reaches the key through any directory mode")
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := loadFleetSealControlKey(""); err == nil {
		t.Fatal("a staged key behind an unreadable directory passed as absent")
	}
}
