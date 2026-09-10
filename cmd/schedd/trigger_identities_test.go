package main

import (
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

func TestLoadHostAgeIdentitiesLoadsCurrentThenPrevious(t *testing.T) {
	dir := t.TempDir()
	current, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate current identity: %v", err)
	}
	previous, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate previous identity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host.age"), []byte(current.String()), 0o400); err != nil {
		t.Fatalf("write current identity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host.age.previous"), []byte(previous.String()), 0o400); err != nil {
		t.Fatalf("write previous identity: %v", err)
	}

	identities, err := loadHostAgeIdentities(filepath.Join(dir, "host.age"))
	if err != nil {
		t.Fatalf("loadHostAgeIdentities: %v", err)
	}
	if len(identities) != 2 || identities[0].String() != current.String() || identities[1].String() != previous.String() {
		t.Fatalf("identities = %v, want current then previous", identities)
	}
}
