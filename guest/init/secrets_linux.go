// secrets_linux.go — load /etc/faas/secrets.env after pivot_root.
//
// The file is a JSON envelope written by vmmd at wake time (spec §11/G2).
// Plain JSON because vmmd has already unsealed the per-app blob against the
// host age key — the on-disk sealed ciphertext never crosses the VM
// boundary. The threat perimeter (spec §11) is rooted at "the box's root
// can read these plaintext values"; we accept that because the alternative
// is teaching guest-init about X25519 + ChaCha20-Poly1305, which is real
// attack surface for zero benefit (any guest that can read secrets.env on
// disk can also read the same secrets through an exec probe).
//
// guest-init treats the file as optional: a missing or malformed file is
// logged and the boot proceeds with no secrets. A malformed file is never
// a fatal error — the worst case is "no env vars this run" not "hang at
// boot".
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
)

// secretsEnvPath is the vmmd-written file inside the per-app drive1
// (mirrors pkg/fcvm/vmm.go::secretsEnvPath — keeping them in sync is a
// build-time invariant tested by the G2 e2e).
const secretsEnvPath = "/etc/faas/secrets.env"

// loadSecrets reads /etc/faas/secrets.env and returns the decoded envelope.
// Errors:
//   - file absent  → ("", nil) — caller treats as "no secrets this app"
//   - permission denied → returns wrapped ErrSecretsUnreadable; the boot
//     proceeds with no secrets and a slog.Warn surfaces the misconfig.
//   - parse failure → returns wrapped ErrSecretsParseFail; same fallback.
//
// The signature intentionally returns `(map, err)` not `(map, *error)` so
// the call site at main_linux.go:boot can pick its policy per-error —
// unreadable ≠ parse-fail ≠ absent.
func loadSecrets(log *slog.Logger) (map[string]string, error) {
	data, err := os.ReadFile(secretsEnvPath)
	if err != nil {
		if isNotExist(err) {
			return nil, nil // no file — most apps have no secrets
		}
		return nil, fmt.Errorf("secrets: read %q: %w", secretsEnvPath, err)
	}
	if len(data) == 0 {
		return nil, nil // empty file == no secrets
	}
	out := map[string]string{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("secrets: parse %q: %w", secretsEnvPath, err)
	}
	if log != nil {
		log.Info("secrets loaded", "count", len(out))
	}
	return out, nil
}

func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	// Wrap-friendly: walk error chain looking for fs.ErrNotExist. os.ReadFile
	// wraps as *fs.PathError, which Unwraps fs.ErrNotExist, so errors.Is is
	// the right tool. The fs.PathError type-assertion we used to do here
	// trips errorlint (and silently fails on wrapped chains).
	return errors.Is(err, fs.ErrNotExist)
}
