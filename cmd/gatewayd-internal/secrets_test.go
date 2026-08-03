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
//
// Note: os.WriteFile's perm is reduced by the process umask (typically 022
// on Linux), so 0o660 would land as 0o640 on disk and the test would pass
// spuriously. We chmod after writing to assert against the exact perm we
// care about.
func TestLoadSecretFile_RejectsGroupWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o660); err != nil {
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

// TestLoadSecretFile_OK0440 — the production Hetzner token file is installed
// `0440 root:faas` (owner root, group faas, mode 0440). The daemon runs as
// faas:faas, so it relies on the group-read bit. allowedSecretPerm must
// accept this perm.
func TestLoadSecretFile_OK0440(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte("abc"), 0o440); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecretFile(p); err != nil {
		t.Errorf("0440 must be accepted (production token file perm): %v", err)
	}
}

// TestLoadSecretFile_OK0640 — same rationale as OK0440 but with owner-write
// preserved. Some operators provision with 0640 instead of 0440 to allow
// root to update the token without changing group ownership.
func TestLoadSecretFile_OK0640(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte("abc"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecretFile(p); err != nil {
		t.Errorf("0640 must be accepted: %v", err)
	}
}

// TestLoadSecretFile_Rejects0444 — world-readable is a leak. allowedSecretPerm
// must fail closed even though the group-read bit is set.
func TestLoadSecretFile_Rejects0444(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte("abc"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecretFile(p); !errors.Is(err, ErrInsecureSecretPerms) {
		t.Errorf("0444 must fail closed with ErrInsecureSecretPerms, got %v", err)
	}
}

// TestLoadSecretFile_Rejects0660 — group-writable is the canonical
// privilege-escalation signal even with owner-write allowed. Must fail closed.
// Uses os.Chmod (not os.WriteFile) because the process umask would silently
// downgrade 0660 to 0640 on disk and the test would pass spuriously.
func TestLoadSecretFile_Rejects0660(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o660); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecretFile(p); !errors.Is(err, ErrInsecureSecretPerms) {
		t.Errorf("0660 must fail closed with ErrInsecureSecretPerms, got %v", err)
	}
}
