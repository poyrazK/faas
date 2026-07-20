package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSecretFile_OK0600(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadSecretFile(p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "abc" {
		t.Errorf("got %q, want abc (trailing newline trimmed)", got)
	}
}

func TestLoadSecretFile_OK0400(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte("abc"), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecretFile(p); err != nil {
		t.Errorf("0400 should be accepted: %v", err)
	}
}

// TestLoadSecretFile_RejectsGroupWrite — 0660 / 0664 / 0666 must fail closed.
// A token file that the `faas` group can write to is the canonical
// privilege-escalation signal (any process running as faas could overwrite
// the gateway's ACME credentials).
func TestLoadSecretFile_RejectsGroupWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte("abc"), 0o660); err != nil {
		t.Fatal(err)
	}
	_, err := loadSecretFile(p)
	if !errors.Is(err, ErrInsecureSecretPerms) {
		t.Errorf("0660 must fail closed with ErrInsecureSecretPerms, got %v", err)
	}
}

func TestLoadSecretFile_RejectsOtherReadable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte("abc"), 0o604); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecretFile(p); !errors.Is(err, ErrInsecureSecretPerms) {
		t.Errorf("0604 must fail closed, got %v", err)
	}
}

func TestLoadSecretFile_RejectsMissing(t *testing.T) {
	if _, err := loadSecretFile("/no/such/file"); err == nil {
		t.Error("missing file must error")
	}
}

func TestLoadSecretFile_RejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecretFile(p); err == nil {
		t.Error("empty file must error")
	}
}

func TestLoadSecretFile_EmptyPath(t *testing.T) {
	if _, err := loadSecretFile(""); err == nil {
		t.Error("empty path must error")
	}
}
