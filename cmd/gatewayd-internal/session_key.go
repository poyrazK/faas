// session_key.go — load the AEAD session-manager key from
// FAAS_SESSION_KEY (Move 4 PR-2). The app-logs route walks through
// pkg/auth.Middleware.RequireSession, whose session-cookie branch
// AEAD-verifies the cookie envelope — that requires a per-daemon
// *session.Manager. The key material lives in /etc/faas/secrets/
// session.key as a hex-encoded 32-byte string; the env wrapper
// keeps the secrets dir unchanged.
//
// We deliberately do NOT lift the cmd/apid loader:
// cmd/apid/loadSessionManager is package-private to cmd/apid and
// inlined into the apid boot path. Cd/gatewayd duplicates the env
// parsing (8 lines) so the two daemons stay independent — the
// AEAD keys are per-process, and a shared helper would imply a
// shared key path, which crosses the per-daemon secret boundary
// (spec §11).
package main

import (
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/session"
)

// loadSessionManager matches cmd/apid/handlers_auth.go::loadSessionManager
// verbatim (the two daemons share the same env contract; the
// repetition is the per-daemon secret boundary). Empty env value
// → ephemeral manager + a warning the caller logs. Production
// sets FAAS_SESSION_KEY to the hex contents of /etc/faas/secrets/
// session.key (root:root 0400, spec §11).
func loadSessionManager(getenv func(string) string, log *slog.Logger) *session.Manager {
	raw := getenv("FAAS_SESSION_KEY")
	if raw == "" {
		m, err := session.NewEphemeralManager(7 * 24 * time.Hour)
		if err != nil {
			log.Error("gatewayd: ephemeral session manager failed", "err", err)
			return nil
		}
		log.Warn("FAAS_SESSION_KEY unset; ephemeral session key in use (dev only)")
		return m
	}
	key, err := hex.DecodeString(raw)
	if err != nil {
		// Not hex (or odd length). Distinct from a wrong-byte-length
		// failure so the operator can tell from the log line which
		// axis is broken — the bootstrap.sh script emits the canonical
		// 64-hex string, but a hand-edited secrets file could easily
		// truncate or paste non-hex bytes.
		log.Error("FAAS_SESSION_KEY is not valid hex", "got_len", len(raw), "err", err)
		return nil
	}
	if len(key) != 32 {
		log.Error("FAAS_SESSION_KEY has wrong byte length", "got_bytes", len(key), "want_bytes", 32)
		return nil
	}
	m, err := session.NewManager(key, 7*24*time.Hour)
	if err != nil {
		log.Error("gatewayd: session manager build failed", "err", err)
		return nil
	}
	return m
}
