// Package secretbox — sealed-at-rest customer secrets (spec §11/G2).
//
// Threat model summary
//
//   - Plaintext VALUE never enters logs. apid accepts plaintext over TLS,
//     seals it with the host X25519 recipient, and stores the ciphertext.
//   - host.age sits at /etc/faas/secrets/host.age, mode 0400 root:root
//     (spec §11). vmmd reads it (root) when injecting per-wake secrets
//     into the jailer chroot. apid does NOT read it on disk — instead
//     systemd's LoadCredential=faas_host_age_identity (see
//     deploy/systemd/faas-apid.service) copies the file into the apid
//     unit's credential dir owned by faas-apid:faas, so apid's MFA
//     handlers (/verify + /confirm + /recover + /disable, IAM-2 /
//     issue #186) can unseal the TOTP secret without ever opening the
//     0400 root:root file. The on-disk mode and the apid-read path are
//     independent — that decoupling is what lets us hold the 0400
//     contract even though apid needs to consume the identity.
//   - Per-wake injection: vmmd loopback-mounts drive1 and writes
//     /etc/faas/secrets.env (JSON), which guest-init reads after pivot_root.
//     The on-disk format is plain because vmmd is the only root component
//     that touches the jailer chroot; the threat model is the same as
//     /etc/faas/app.json.
//
// What lives here
//
//   - hostkey.go — load/save the host identity to /etc/faas/secrets/host.age;
//     expose the recipient (string) and the identity (for vmmd + apid).
//   - seal.go     — Seal / Open round-trip on an arbitrary env map.
//
// Wire shape: the on-disk envelope is age's standard format (Stanza header +
// ChaCha20-Poly1305 body). The plaintext is a canonical-JSON encoding of
// Envelope (map[string]string) so the same decoder can validate shape on
// Open.
package secretbox

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// X25519 identity on first boot. Spec §11: secrets in /etc/faas/secrets/,
// mode 0400 root:root. apid reads via systemd LoadCredential, not the
// on-disk file — see the package docstring above for the decoupling.
const DefaultHostKeyPath = "/etc/faas/secrets/host.age"

// DefaultFleetKeyPath is the dedicated X25519 identity shared by every
// Gregale control-plane and compute node. It is deliberately separate from
// host.age: host.age remains a per-host identity, while ciphertext that may be
// consumed on another node is sealed to fleet.age.
const DefaultFleetKeyPath = "/etc/faas/secrets/fleet.age"

// DefaultHostKeyPreviousPath is the rotation-overlap twin of DefaultHostKeyPath.
// During the 30-day overlap window (issue #316 / ADR-057), the operator
// renames the old host.age to host.age.previous; daemons load BOTH
// identities via LoadHostKeys(dir) and pass the slice to secretbox.OpenMulti,
// which lets age's native multi-recipient fallback unseal envelopes
// sealed under either key. After 30 days the operator invokes
// `gregale host-age prune-previous` to remove this file.
//
// Same mode contract (0400 root:root) as DefaultHostKeyPath.
const DefaultHostKeyPreviousPath = "/etc/faas/secrets/host.age.previous"

// ErrHostKeyInsecurePerms is returned by LoadHostKey when the file's mode
// allows anything other than owner-read. The private half of the host identity
// is the secret material every SealedSecret in app_secrets is encrypted
// against; a host.age that's group- or world-readable lets any unprivileged
// user on the box extract the X25519 secret key and unseal every customer's
// env vars. Spec §11: secrets in /etc/faas/secrets/ root:root 0400. This
// runtime check is the tripwire when the file's been chmod'd loose (operator
// drift) or restored from a backup with wrong mode.
var ErrHostKeyInsecurePerms = errors.New("secretbox: host.key permissions allow read/write/exec/setuid to non-owner")

// ErrHostKeyNotFound is returned by LoadHostKey when the file is missing.
// Callers (vmmd) treat this as the first-boot signal and call
// GenerateAndSaveHostKey to create one.
var ErrHostKeyNotFound = errors.New("secretbox: host key not found")

// LoadHostKey parses an age-format X25519 identity from path. The file is the
// raw "AGE-SECRET-KEY-1..." string (age's standard textual representation).
//
// Security check (M8 §11): refuse to load if the file's mode permits anything
// other than owner-read (0o400). The private half is the secret material that
// decrypts every customer's sealed env blobs — a single bit set for group,
// other, or any write/exec/setuid is enough for an attacker to extract it.
// Mirrors the public-side check in LoadRecipient, but stricter: 0o400 only.
// ErrHostKeyInsecurePerms is returned for any deviation; the vmmd startup
// path fails-fast on this so a stolen/loosened host.age cannot reach the
// unseal path.
func LoadHostKey(path string) (*age.X25519Identity, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrHostKeyNotFound
		}
		return nil, fmt.Errorf("secretbox: stat host key %q: %w", path, err)
	}
	// Security check (M8 §11): refuse to load if the file's mode permits anything
	// other than owner-read (0o400).
	// Exception: systemd LoadCredential creates files in /run/credentials/...
	// with mode 0o440 owned by the service user/group. If the path is under a
	// credentials directory, mode 0o440 is also permitted.
	perm := info.Mode().Perm()
	isSystemdCredential := strings.Contains(path, "/credentials/")
	if perm != 0o400 && (!isSystemdCredential || perm != 0o440) {
		return nil, fmt.Errorf("secretbox: host key %q mode %#o: %w",
			path, perm, ErrHostKeyInsecurePerms)
	}
	data, err := os.ReadFile(path)
	if err != nil {
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
// representation to path with mode 0400, and returns the identity. Called
// by vmmd on first boot when LoadHostKey returns ErrHostKeyNotFound.
//
// File mode is 0400 root:root — spec §11 literal. The on-disk file is
// only readable by vmmd (root), which is exactly the threat model: a
// single root component owns the secret material. apid reads the
// identity through systemd's LoadCredential=faas_host_age_identity
// (see deploy/systemd/faas-apid.service), which copies the file into
// the unit's credential dir owned by faas-apid:faas. The on-disk
// mode and the credential-dir mode are independent — apid never
// touches /etc/faas/secrets/host.age directly.
//
// On a fresh box this is the bootstrap moment: vmmd is the only root
// component, so it owns key generation. apid never generates — it
// consumes the recipient + identity via LoadCredential.
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

// DefaultFleetAgeRecipientPath is the public half of DefaultFleetKeyPath.
const DefaultFleetAgeRecipientPath = "/etc/faas/secrets/fleet.age.pub"

// WriteRecipientFile writes the public side of id to path with mode 0444.
// Called by vmmd after LoadOrGenerate. apid reads the file at startup;
// both daemons are owned by root so the file is root-readable (and the
// public key is intentionally non-secret anyway).
func WriteRecipientFile(path string, id *age.X25519Identity) error {
	want := []byte(RecipientString(id))
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, want) {
		return nil
	}
	if err := os.WriteFile(path, want, 0o444); err != nil {
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

// LoadHostKeys loads BOTH the current and previous host identities
// from a directory and returns them as an ordered slice suitable for
// passing to secretbox.OpenMulti / age.Decrypt. The rotation-overlap
// plumbing (issue #316 / ADR-057): the operator renames
// /etc/faas/secrets/host.age → host.age.previous, then drops a new
// host.age. Both files must unseal envelopes during the 30-day
// overlap window; age.Decrypt(src, identities ...) natively falls
// back across the slice ("all identities will be tried until one
// successfully decrypts the file") so no schema migration is needed.
//
// Returned order: current first, previous second. age.Decrypt tries
// every supplied identity regardless of order, but pinning the order
// keeps `gregale host-age status` output deterministic and means the
// audit-log shows current-first when the unseal succeeds.
//
// If host.age is missing AND host.age.previous is missing, returns
// ErrHostKeyNotFound (the canonical first-boot signal — vmmd handles
// this with GenerateAndSaveHostKey). If ONLY host.age.previous is
// present, returns the previous identity as a 1-element slice (the
// manual-promote path: operator renamed current → previous without
// dropping a fresh current, e.g. for a key restore from backup).
//
// Each file is parsed via LoadHostKey so the same 0o400/0o440 perm
// tripwire applies — a loose .previous file is the same leak as a
// loose current file.
func LoadHostKeys(dir string) ([]*age.X25519Identity, error) {
	currentPath := filepath.Join(dir, "host.age")
	previousPath := filepath.Join(dir, "host.age.previous")

	// Try current first so the "both missing" case surfaces
	// ErrHostKeyNotFound (matches the single-file LoadHostKey
	// contract). If current exists but .previous doesn't, we still
	// return a 1-element slice — the normal pre-rotation state.
	current, err := LoadHostKey(currentPath)
	if err != nil {
		if errors.Is(err, ErrHostKeyNotFound) {
			// Fall through to the previous-only path so a box that's
			// been promoted-by-rename (operator flipped .previous →
			// host.age but the rename hasn't happened yet) still loads.
			if prev, prevErr := LoadHostKey(previousPath); prevErr == nil {
				return []*age.X25519Identity{prev}, nil
			} else if !errors.Is(prevErr, ErrHostKeyNotFound) {
				return nil, fmt.Errorf("secretbox: load host.age.previous %q: %w", previousPath, prevErr)
			}
		}
		return nil, err
	}

	previous, err := LoadHostKey(previousPath)
	if err != nil {
		if errors.Is(err, ErrHostKeyNotFound) {
			// Normal pre-rotation state: only current exists.
			return []*age.X25519Identity{current}, nil
		}
		return nil, fmt.Errorf("secretbox: load host.age.previous %q: %w", previousPath, err)
	}
	return []*age.X25519Identity{current, previous}, nil
}

// LoadFleetAndHostKeys loads the fleet identity first, followed by the
// current and previous per-host identities. New shared ciphertext is sealed
// to identities[0]; the host identities remain available only to migrate
// legacy host-sealed rows without ever writing plaintext to disk.
//
// systemd LoadCredential uses service-specific filenames, so both the
// canonical on-disk names and the credential names emitted by
// pkg/daemonunitspec are accepted. fleet.age is mandatory: callers that need
// the legacy single-host posture must continue to call LoadHostKeys.
func LoadFleetAndHostKeys(dir string) ([]*age.X25519Identity, error) {
	fleet, err := loadFirstIdentity(dir, []string{"fleet.age", "faas_fleet_age_identity"})
	if err != nil {
		return nil, fmt.Errorf("secretbox: load fleet identity: %w", err)
	}
	identities := []*age.X25519Identity{fleet}
	seen := map[string]struct{}{fleet.Recipient().String(): {}}

	for _, names := range [][]string{
		{"host.age", "faas_host_age_identity"},
		{"host.age.previous", "faas_host_age_identity_previous"},
	} {
		id, loadErr := loadFirstIdentity(dir, names)
		if loadErr != nil {
			if errors.Is(loadErr, ErrHostKeyNotFound) {
				continue
			}
			return nil, loadErr
		}
		kid := id.Recipient().String()
		if _, duplicate := seen[kid]; duplicate {
			continue
		}
		seen[kid] = struct{}{}
		identities = append(identities, id)
	}
	return identities, nil
}

func loadFirstIdentity(dir string, names []string) (*age.X25519Identity, error) {
	for _, name := range names {
		id, err := LoadHostKey(filepath.Join(dir, name))
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, ErrHostKeyNotFound) {
			return nil, err
		}
	}
	return nil, ErrHostKeyNotFound
}

// WriteHostKeyAtPath writes id's textual representation to path with
// mode 0400 (spec §11). Used by `gregale host-age rotate` to drop a
// new key and by `gregale host-age prune-previous` (via os.Remove —
// not this helper).
//
// The write is atomic in the same shape as cmd/gregale/commands_backup.go:
// tmp file in the destination directory (so the rename is a single
// filesystem operation), 0400 chmod, rename into place. A half-written
// host.age would brick vmmd's identity load and force a manual
// recovery, so the tmp + rename dance is load-bearing — a plain
// os.WriteFile that loses a write to ENOSPC mid-flush would land the
// daemon unable to unseal customer envelopes on the next restart.
//
// Does NOT create the parent directory; the caller (the gregale CLI,
// or a future installer path) is responsible for ensuring the secrets
// dir exists with mode 0700 root:root. Silently MkdirAll'ing would
// mask the canonical "role hasn't run yet" failure mode.
func WriteHostKeyAtPath(path string, id *age.X25519Identity) error {
	if id == nil {
		return errors.New("secretbox: nil identity")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".host.age.tmp.*")
	if err != nil {
		return fmt.Errorf("secretbox: create tmp %q: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename didn't happen. The Close
		// inside Write already happened (or errored); the unlink
		// here is the safety net for the rename-failure path.
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write([]byte(id.String())); err != nil {
		// Best-effort close: the tmp file is unlinked by the
		// deferred os.Remove above; a stuck close on the error path
		// would only delay that cleanup.
		_ = tmp.Close()
		return fmt.Errorf("secretbox: write identity to %q: %w", path, err)
	}
	if err := tmp.Chmod(0o400); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secretbox: chmod 0400 %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("secretbox: close tmp %q: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("secretbox: rename into place %q: %w", path, err)
	}
	return nil
}

// PromotePreviousToCurrent renames host.age.previous → host.age,
// preserving mode. Used by the manual escape hatch in
// `gregale host-age prune-previous --promote` when the operator
// decides the previous key should be the new current (e.g. the
// freshly-rotated current key was lost or compromised and the
// previous key is the recovery target).
//
// Refuses if host.age.previous is missing (nothing to promote) or
// if host.age already exists (would silently overwrite the current).
// The CLI surfaces a clearer error in those cases; this helper
// returns ErrHostKeyNotFound for the missing case and a wrapped
// error for the "would overwrite" case.
func PromotePreviousToCurrent(dir string) error {
	previousPath := filepath.Join(dir, "host.age.previous")
	currentPath := filepath.Join(dir, "host.age")

	if _, err := os.Stat(previousPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("secretbox: promote: %w", ErrHostKeyNotFound)
		}
		return fmt.Errorf("secretbox: stat previous %q: %w", previousPath, err)
	}
	if _, err := os.Stat(currentPath); err == nil {
		return fmt.Errorf("secretbox: promote refused: current %q already exists (remove it first)", currentPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("secretbox: stat current %q: %w", currentPath, err)
	}
	if err := os.Rename(previousPath, currentPath); err != nil {
		return fmt.Errorf("secretbox: rename previous → current: %w", err)
	}
	return nil
}
