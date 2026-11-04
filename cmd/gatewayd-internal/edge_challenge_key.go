package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Where the challenge signing secret came from (gateway_edge_challenge_key_info).
const (
	edgeChallengeKeyFleet   = "fleet"
	edgeChallengeKeySession = "session"
)

// edgeChallengeSecret returns the secret kind=challenge tokens and passes are
// signed with (ADR-831 amendment 17). Every gateway of a fleet must sign with
// one secret: gatewayd-public spreads a visitor's requests across them, and a
// pass one gateway issued is refused by a gateway with another key.
//
// FAAS_EDGE_CHALLENGE_KEY_FILE names the root-only credential Ansible installs
// from the fleet vault: 32 bytes as 64 hex digits. Without it the gateway
// signs with its session key, which `gregalectl secrets init` makes per host,
// so that only serves a fleet of one gateway.
func edgeChallengeSecret(getenv func(string) string, sessionKey []byte) (secret []byte, source string, err error) {
	path := strings.TrimSpace(getenv("FAAS_EDGE_CHALLENGE_KEY_FILE"))
	if path == "" {
		return sessionKey, edgeChallengeKeySession, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read edge challenge key file: %w", err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, "", errors.New("edge challenge key file must hold 32 bytes as 64 hex digits")
	}
	return key, edgeChallengeKeyFleet, nil
}
