package secretbox

import (
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

func TestFleetPreviousIdentityOverlap(t *testing.T) {
	for _, credentials := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical", true: "credentials"}[credentials], func(t *testing.T) {
			dir := t.TempDir()
			current, _ := age.GenerateX25519Identity()
			previous, _ := age.GenerateX25519Identity()
			name, oldName := "fleet.age", "fleet.age.previous"
			if credentials {
				name, oldName = "faas_fleet_age_identity", "faas_fleet_age_identity_previous"
			}
			if err := WriteHostKeyAtPath(filepath.Join(dir, name), current); err != nil {
				t.Fatal(err)
			}
			if err := WriteHostKeyAtPath(filepath.Join(dir, oldName), previous); err != nil {
				t.Fatal(err)
			}
			ids, err := LoadFleetAndHostKeys(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(ids) != 2 || ids[0].Recipient().String() != current.Recipient().String() {
				t.Fatal("fleet rotation lost current or previous identity")
			}
			cipher, err := SealOne(previous.Recipient(), "TOKEN", "overlap", 4096)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := OpenMulti(ids, cipher); err != nil {
				t.Fatal("previous fleet envelope cannot open during overlap")
			}
			if err := os.Chmod(filepath.Join(dir, oldName), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadFleetAndHostKeys(dir); err == nil {
				t.Fatal("insecure previous fleet identity was accepted")
			}
		})
	}
}
