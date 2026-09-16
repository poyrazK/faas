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
