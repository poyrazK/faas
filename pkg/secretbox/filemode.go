package secretbox

import (
	"errors"
	"fmt"
	"os"
)

// Shared file-mode guard for operator-provisioned secret files
// (issue #603).
//
// # Why this lives here
//
// pkg/secretbox already owns the two age-specific perm checks
// (LoadHostKey's 0400-only rule, LoadRecipient's public-half rule).
// The mode predicate itself is not age-specific, and a second daemon
// reading a different secret file should not have to re-derive
// "which modes are safe" — that answer is a platform rule (spec §11),
// not a per-daemon one. cmd/gatewayd-internal/secrets.go states the
// rationale for the allowlist in full; this is that allowlist,
// exported, so githubd's GitHub App private key gets the same gate
// without either cmd package importing the other (both are package
// main).
//
// Only the predicate moved. The loaders stay where they are:
// gatewayd-internal's plain-bearer-token reader is deliberately not
// in this package (see the note at the top of
// cmd/gatewayd-internal/secrets.go).

// ErrInsecureFileMode is returned by AssertSecretFileMode when a
// secret file's mode is outside the allowlist. Distinct from a
// stat error so an operator can tell "didn't provision" apart from
// "provisioned insecurely".
var ErrInsecureFileMode = errors.New("secretbox: secret file mode permits more than owner/group read")

// secretFileModes is the allowlist of safe modes for an
// operator-provisioned secret file read by a non-root daemon:
//
//	0400, 0440, 0600, 0640  — owner, and optionally group, read/write
//
// Everything else is rejected. Other-readable is a leak;
// group-writable is a privilege-escalation signal (any process in the
// `faas` group could substitute the daemon's credentials); any exec /
// setuid / setgid / sticky bit is the canonical priv-esc signal.
//
// The list is explicit rather than a bitmask because a bitmask cannot
// express "group-read allowed, group-write forbidden" — and
// group-write is exactly what must stay closed.
var secretFileModes = []os.FileMode{0o400, 0o440, 0o600, 0o640}

// secretFileModesMsg is secretFileModes rendered for the
// operator-facing error. A literal rather than a loop over the slice:
// the modes are fixed in this file, and the message is only ever read
// by a human staring at a failed unit start.
const secretFileModesMsg = "0400, 0440, 0600, 0640"

// AllowedSecretFileMode reports whether perm is a safe mode for an
// operator-provisioned secret file. Exported for callers that do
// their own stat + read and only need the platform's answer to
// "is this mode safe" (cmd/gatewayd-internal/secrets.go).
func AllowedSecretFileMode(perm os.FileMode) bool {
	for _, m := range secretFileModes {
		if perm == m {
			return true
		}
	}
	return false
}

// AssertSecretFileMode stat-checks path and returns nil only when it
// is a regular file whose mode is allowed. The error names
// the daemon (for the operator reading a failed unit start), the
// observed mode, and the fix.
//
// systemd's LoadCredential materialises credentials under
// /run/credentials/... as 0440 owned by the service user, which the
// allowlist already accepts — so a unit that switches from a
// bind-mounted path to LoadCredential does not have to change this
// call.
//
// Callers use this as a gate BEFORE reading: a fail-loud startup
// error beats a daemon that runs happily on a world-readable private
// key.
func AssertSecretFileMode(daemon, path string) error {
	if path == "" {
		return fmt.Errorf("%s: secret path is empty", daemon)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s: stat secret %q: %w", daemon, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s: secret %q is not a regular file (mode %s)", daemon, path, info.Mode())
	}
	perm := info.Mode().Perm()
	if !AllowedSecretFileMode(perm) {
		return fmt.Errorf("%s: secret %q mode %#o: %w (want one of %s; fix: chmod 0400 %s)",
			daemon, path, perm, ErrInsecureFileMode, secretFileModesMsg, path)
	}
	return nil
}
