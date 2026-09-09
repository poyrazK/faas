package main

import (
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

func writeTestIdentity(t *testing.T, path string) {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(identity.String()), 0o400); err != nil {
		t.Fatal(err)
	}
}

func TestLoadIdentitiesAllowsMissingOptionalPreviousCredential(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "host.age")
	writeTestIdentity(t, current)

	identities, err := loadIdentities(func(name string) string {
		switch name {
		case "FAAS_HOST_AGE_IDENTITY_PATH":
			return current
		case "FAAS_HOST_AGE_PREVIOUS_IDENTITY_PATH":
			return filepath.Join(dir, "host.age.previous")
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 {
		t.Fatalf("identities = %d, want current identity only", len(identities))
	}
}

func TestLoadIdentitiesRejectsInvalidExistingPreviousCredential(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "host.age")
	previous := filepath.Join(dir, "host.age.previous")
	writeTestIdentity(t, current)
	if err := os.WriteFile(previous, []byte("not-an-age-identity\n"), 0o400); err != nil {
		t.Fatal(err)
	}

	_, err := loadIdentities(func(name string) string {
		if name == "FAAS_HOST_AGE_IDENTITY_PATH" {
			return current
		}
		if name == "FAAS_HOST_AGE_PREVIOUS_IDENTITY_PATH" {
			return previous
		}
		return ""
	})
	if err == nil {
		t.Fatal("err = nil, want invalid previous identity failure")
	}
}
