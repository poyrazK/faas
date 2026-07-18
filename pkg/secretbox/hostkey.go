// Package secretbox — sealed-at-rest customer secrets (spec §11/G2).
//
// Threat model summary
//
//   - Plaintext VALUE never enters logs. apid accepts plaintext over TLS,
//     seals it with the host X25519 recipient, and stores the ciphertext.
//   - host.age sits at /etc/faas/secrets/host.age (root:root 0400). Only
//     vmmd reads it; apid uses the matching public recipient (extracted from
//     the identity at startup) to seal, and never sees the private half.
//   - Per-wake injection: vmmd loopback-mounts drive1 and writes
//     /etc/faas/secrets.env (JSON), which guest-init reads after pivot_root.
//     The on-disk format is plain because vmmd is the only root component
//     that touches the jailer chroot; the threat model is the same as
//     /etc/faas/app.json.
//
// What lives here
//
//   - hostkey.go — load/save the host identity to /etc/faas/secrets/host.age;
//     expose the recipient (string) and the identity (for vmmd to unseal).
//   - seal.go     — Seal / Open round-trip on an arbitrary env map.
//
// Wire shape: the on-disk envelope is age's standard format (Stanza header +
// ChaCha20-Poly1305 body). The plaintext is a canonical-JSON encoding of
// Envelope (map[string]string) so the same decoder can validate shape on
// Open.
package secretbox

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"filippo.io/age"
)

// ErrRecipientInsecurePerms is returned by LoadRecipient when host.age.pub's
// mode allows write/exec/setuid to anyone other than the file's owner. The
// public half is by design world-readable, but a public-key file that is also
// writable (or setuid) is the canonical signal that the host has been
// tampered with: an attacker who can write to host.age.pub can substitute
// their own recipient and start collecting freshly-sealed ciphertexts.
// Fail-fast at apid startup rather than serving sealed blobs to a
// hijacked recipient.
var ErrRecipientInsecurePerms = errors.New("secretbox: host.age.pub permissions allow write/exec/setuid to non-owner")

// DefaultHostKeyPath is where vmmd looks for (and auto-generates) the host
// X25519 identity on first boot. Spec §11: secrets in /etc/faas/secrets/
// root:root 0400.
const DefaultHostKeyPath = "/etc/faas/secrets/host.age"

// ErrHostKeyNotFound is returned by LoadHostKey when the file is missing.
// Callers (vmmd) treat this as the first-boot signal and call
// GenerateAndSaveHostKey to create one.
var ErrHostKeyNotFound = errors.New("secretbox: host key not found")

// LoadHostKey parses an age-format X25519 identity from path. The file is
// expected to be the raw "AGE-SECRET-KEY-1..." string (age's standard
// textual representation), mode 0400 in production but mode is not enforced
// here — the host key's filesystem permissions are an ansible / Makefile
// concern, not a runtime check.
func LoadHostKey(path string) (*age.X25519Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrHostKeyNotFound
		}
		return nil, fmt.Errorf("secretbox: read host key %q: %w", path, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("secretbox: host key %q is empty", path)
	}
	id, err := age.ParseX25519Identity(string(data))
	if err != nil {
		return nil, fmt.Errorf("secretbox: parse host key %q: %w", path, err)
	}
	return id, nil
}

// GenerateAndSaveHostKey creates a new X25519 identity, writes its textual
// representation to path with mode 0400, and returns the identity. Called by
// vmmd on first boot when LoadHostKey returns ErrHostKeyNotFound.
//
// On a fresh box this is the bootstrap moment: vmmd is the only root
// component, so it owns key generation. apid never generates — it consumes
// the recipient string.
func GenerateAndSaveHostKey(path string) (*age.X25519Identity, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("secretbox: generate host key: %w", err)
	}
	if err := os.WriteFile(path, []byte(id.String()), 0o400); err != nil {
		return nil, fmt.Errorf("secretbox: write host key %q: %w", path, err)
	}
	return id, nil
}

// RecipientString returns the age-1... bech32 encoding of id's public half.
// apid stores this in memory and uses it for Seal; vmmd uses the identity
// for Open. The pair (RecipientString, identity) is the only state
// secretbox cares about.
func RecipientString(id *age.X25519Identity) string {
	return id.Recipient().String()
}

// DefaultHostAgeRecipientPath is where vmmd writes the host age recipient
// (public side) for apid to consume. vmmd owns the private half; apid reads
// only the public string at startup. Mode 0444 — public by design.
const DefaultHostAgeRecipientPath = "/etc/faas/secrets/host.age.pub"

// WriteRecipientFile writes the public side of id to path with mode 0444.
// Called by vmmd after LoadOrGenerate. apid reads the file at startup;
// both daemons are owned by root so the file is root-readable (and the
// public key is intentionally non-secret anyway).
func WriteRecipientFile(path string, id *age.X25519Identity) error {
	if err := os.WriteFile(path, []byte(RecipientString(id)), 0o444); err != nil {
		return fmt.Errorf("secretbox: write recipient %q: %w", path, err)
	}
	return nil
}

// LoadRecipient parses a recipient string from path. apid calls this at
// startup to obtain the sealing key. The file is the canonical host.age.pub
// artifact written by vmmd (mode 0444).
//
// Security check: refuse to start if the file's mode permits write/exec/
// setuid to anyone other than the file's owner. The recipient is technically
// public, but a public-key file that is ALSO writable (or setuid) is the
// canonical signal that the host's PKI has been tampered with: an attacker
// who can write to host.age.pub can substitute their own recipient and
// start collecting freshly-sealed ciphertexts. Fail-fast at apid startup
// rather than serving sealed blobs to a hijacked recipient.
//
// Permitted shapes: any combination of read bits for owner (0o400),
// owner-write (0o200), group-read (0o040), and other-read (0o004). That
// accepts the production modes 0o400, 0o440, 0o404, 0o444. Any other bit
// (group/other write, any exec, setuid, setgid, sticky) is rejected.
func LoadRecipient(path string) (*age.X25519Recipient, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("secretbox: stat recipient %q: %w", path, err)
	}
	const allowedPerm = os.FileMode(0o644)
	if info.Mode().Perm() & ^allowedPerm != 0 {
		return nil, fmt.Errorf("secretbox: recipient %q mode %#o: %w",
			path, info.Mode().Perm(), ErrRecipientInsecurePerms)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("secretbox: read recipient %q: %w", path, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("secretbox: recipient file %q is empty", path)
	}
	r, err := age.ParseX25519Recipient(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("secretbox: parse recipient %q: %w", path, err)
	}
	return r, nil
}
