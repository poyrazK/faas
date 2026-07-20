// Token / secret loaders for gatewayd (spec §11/G2). The shape mirrors
// pkg/secretbox.LoadRecipient: stat-check perm bits before reading, fail
// closed if the file is group/other writable or has any exec/setuid bits.
//
// Why this lives in cmd/gatewayd (not pkg/gateway or pkg/secretbox):
//
//   - pkg/gateway is a shared lib — importing pkg/secretbox from it would
//     force every consumer (test binaries, alternate frontends) to take
//     pkg/secretbox's deps. Putting the loader in cmd/gatewayd keeps
//     pkg/gateway free of the 0400 perm-check dependency.
//   - pkg/secretbox is age-specific; the Hetzner token is a plain bearer
//     token and doesn't share the age X25519 shape.
package main

import (
	"errors"
	"fmt"
	"os"
)

// ErrInsecureSecretPerms is returned when a token file is group/other
// writable or has any exec/setuid bits. The error is intentionally distinct
// from "file not found" so an operator can tell "didn't provision" apart from
// "provisioned insecurely".
var ErrInsecureSecretPerms = errors.New("gatewayd: secret file mode permits more than owner read/write")

// allowedSecretPerm reports whether perm is a safe mode for a token file.
// The Hetzner DNS API token is operator-provisioned and used only by the
// gatewayd process (running as root). The narrowest safe shape is owner-only
// (0400 / 0600); we reject any bit set outside the owner's read/write —
// group-readable is unnecessary, other-readable is a leak, and any exec /
// setuid / setgid / sticky bit is the canonical privilege-escalation signal.
func allowedSecretPerm(perm os.FileMode) bool {
	const ownerOnly = os.FileMode(0o600)
	return perm&^ownerOnly == 0
}

// loadSecretFile reads path and returns its trimmed contents. Fails closed
// if the file doesn't exist, is empty, or has loose permissions. Mirrors
// pkg/secretbox.LoadRecipient so the operator-facing error messages have the
// same shape across the platform.
func loadSecretFile(path string) (string, error) {
	if path == "" {
		return "", errors.New("gatewayd: secret path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("gatewayd: stat secret %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("gatewayd: secret %q is not a regular file (mode %s)", path, info.Mode())
	}
	if !allowedSecretPerm(info.Mode().Perm()) {
		return "", fmt.Errorf("gatewayd: secret %q mode %#o: %w",
			path, info.Mode().Perm(), ErrInsecureSecretPerms)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("gatewayd: read secret %q: %w", path, err)
	}
	// Trim trailing newline (most operators provision the file with
	// `echo "$TOKEN" > path`). Don't trim interior whitespace — tokens
	// can legitimately contain leading/trailing non-newline whitespace
	// (though they shouldn't).
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	if len(b) == 0 {
		return "", fmt.Errorf("gatewayd: secret %q is empty", path)
	}
	return string(b), nil
}