package secretbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadHostKeyMissing verifies ErrHostKeyNotFound surfaces for vmmd's
// first-boot signal. vmmd's run() does:
//
//	if errors.Is(err, secretbox.ErrHostKeyNotFound) { generate and save }
func TestLoadHostKeyMissing(t *testing.T) {
	_, err := LoadHostKey(filepath.Join(t.TempDir(), "missing.age"))
	if err == nil {
		t.Fatal("expected error for missing host key")
	}
	if !errors.Is(err, ErrHostKeyNotFound) {
		t.Fatalf("got %v, want ErrHostKeyNotFound", err)
	}
}

// TestGenerateAndSaveRoundTrip writes a key, loads it back, asserts
// Recipient() matches the original. Mode 0400 must be honored.
func TestGenerateAndSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host.age")
	id, err := GenerateAndSaveHostKey(path)
	if err != nil {
		t.Fatalf("generate+save: %v", err)
	}
	// Mode: 0400 only owner can read.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o400 {
		t.Errorf("mode=%o want 0o400", perm)
	}
	// Reload and compare recipient.
	id2, err := LoadHostKey(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if RecipientString(id) != RecipientString(id2) {
		t.Errorf("recipient mismatch: %q vs %q", RecipientString(id), RecipientString(id2))
	}
}

// TestRecipientFileRoundTrip covers the vmmd-writes-pub / apid-reads-pub
// handshake. The recipient file is 0444 (public); the identity file is
// 0400 (private). vmmd is the only writer of both.
func TestRecipientFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	idPath := filepath.Join(dir, "host.age")
	pubPath := filepath.Join(dir, "host.age.pub")

	id, err := GenerateAndSaveHostKey(idPath)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if err := WriteRecipientFile(pubPath, id); err != nil {
		t.Fatalf("write pub: %v", err)
	}
	st, err := os.Stat(pubPath)
	if err != nil {
		t.Fatalf("stat pub: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o444 {
		t.Errorf("pub mode=%o want 0o444", perm)
	}
	r, err := LoadRecipient(pubPath)
	if err != nil {
		t.Fatalf("load pub: %v", err)
	}
	if r.String() != id.Recipient().String() {
		t.Errorf("recipient mismatch: %q vs %q", r.String(), id.Recipient().String())
	}
}

// TestLoadRecipientMissing documents the missing-file error path used by
// apid's startup: if vmmd hasn't run yet (no host.age.pub), apid refuses
// to start so a misconfigured box doesn't accept plaintext secrets that
// have nowhere to seal to.
func TestLoadRecipientMissing(t *testing.T) {
	_, err := LoadRecipient(filepath.Join(t.TempDir(), "missing.pub"))
	if err == nil {
		t.Fatal("expected error for missing recipient")
	}
}
