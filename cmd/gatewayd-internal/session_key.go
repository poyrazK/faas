// The existing FAAS_SESSION_KEY contract is shared with apid. It accepts
// operator-provisioned 32-byte hex content or a LoadCredential file path.
// ADR-570 derives a distinct managed-deadline MAC key from this master.
package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/trafficdeadline"
)

func loadSharedSessionKey(getenv func(string) string) ([]byte, error) {
	raw := strings.TrimSpace(getenv("FAAS_SESSION_KEY"))
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "/") {
		info, err := os.Stat(raw)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("FAAS_SESSION_KEY path is unavailable or not a regular file")
		}
		data, err := os.ReadFile(raw)
		if err != nil {
			return nil, errors.New("FAAS_SESSION_KEY path read failed")
		}
		raw = strings.TrimSpace(string(data))
	}
	key, err := hex.DecodeString(raw)
	if err != nil {
		return nil, errors.New("FAAS_SESSION_KEY is not valid hex")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("FAAS_SESSION_KEY has wrong byte length: got %d, want 32", len(key))
	}
	return key, nil
}

func loadSessionManager(getenv func(string) string, log *slog.Logger) *session.Manager {
	key, err := loadSharedSessionKey(getenv)
	if err != nil {
		log.Error("gatewayd: session key load failed", "err", err)
		return nil
	}
	if len(key) == 0 {
		manager, err := session.NewEphemeralManager(7 * 24 * time.Hour)
		if err != nil {
			log.Error("gatewayd: ephemeral session manager failed", "err", err)
			return nil
		}
		log.Warn("FAAS_SESSION_KEY unset; ephemeral session key in use (dev only)")
		return manager
	}
	manager, err := session.NewManager(key, 7*24*time.Hour)
	if err != nil {
		log.Error("gatewayd: session manager build failed", "err", err)
		return nil
	}
	return manager
}

// No ephemeral fallback: separate gateway processes must verify one another.
func loadTrafficDeadlineSigner(getenv func(string) string) (*trafficdeadline.Signer, error) {
	key, err := loadSharedSessionKey(getenv)
	if err != nil {
		return nil, err
	}
	if len(key) == 0 {
		return nil, nil
	}
	return trafficdeadline.New(key, nil)
}
