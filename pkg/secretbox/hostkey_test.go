package secretbox

import (
	"errors"
	"fmt"
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

// TestLoadRecipient_RejectsInsecurePerms pins down the security-critical
// refuse-to-start behavior: apid must reject host.age.pub files whose mode
// allows write/exec/setuid to non-owner. A writable public key is the
// canonical tamper signal — an attacker could substitute their own
// recipient and start collecting freshly-sealed ciphertexts.
//
// Cases: writable for group, writable for other, setuid, setgid, sticky,
// and exec for owner. Each must fail with ErrRecipientInsecurePerms in the
// chain. Sanity cases (0400, 0444, 0440) must succeed — those are the
// production modes.
func TestLoadRecipient_RejectsInsecurePerms(t *testing.T) {
	id, err := GenerateAndSaveHostKey(filepath.Join(t.TempDir(), "host.age"))
	if err != nil {
		t.Fatalf("gen: %v", err)
	}

	rejectModes := []os.FileMode{
		0o666, // world-writable — primary concern
		0o660, // group-writable
		0o646, // other-writable
		0o755, // exec for everyone
		0o711, // exec for owner
		0o4744, // setuid
		0o2744, // setgid
		0o1744, // sticky
	}
	for _, mode := range rejectModes {
		t.Run(fmt.Sprintf("mode_%o", mode), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "host.age.pub")
			// WriteFile applies umask (typically 0o022), which would
			// strip the bits we're trying to test. Create with a
			// neutral mode, then chmod to the exact target.
			if err := os.WriteFile(path, []byte(RecipientString(id)), 0o600); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatalf("chmod %o: %v", mode, err)
			}
			_, err := LoadRecipient(path)
			if err == nil {
				t.Fatalf("mode %o accepted — must refuse", mode)
			}
			if !errors.Is(err, ErrRecipientInsecurePerms) {
				t.Errorf("mode %o: err = %v, want ErrRecipientInsecurePerms in chain", mode, err)
			}
		})
	}

	acceptModes := []os.FileMode{
		0o400, 0o404, 0o440, 0o444, 0o600, 0o640, 0o604, // production-permitted shapes
	}
	for _, mode := range acceptModes {
		t.Run(fmt.Sprintf("accept_%o", mode), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "host.age.pub")
			if err := os.WriteFile(path, []byte(RecipientString(id)), 0o600); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatalf("chmod %o: %v", mode, err)
			}
			if _, err := LoadRecipient(path); err != nil {
				t.Errorf("mode %o rejected: %v", mode, err)
			}
		})
	}
}
