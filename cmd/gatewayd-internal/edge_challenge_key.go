package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Where the gateway fleet key came from (gateway_edge_challenge_key_info).
const (
	gatewayFleetKeyFleet = "fleet"
)

// gatewayFleetKeyEnv names the gateway fleet key file (ADR-20261102191500).
// gatewayFleetKeyLegacyEnv is its name before then (ADR-831 amendment 17),
// read for one release so an older drop-in still boots the gateway.
const (
	gatewayFleetKeyEnv       = "FAAS_GATEWAY_FLEET_KEY_FILE"
	gatewayFleetKeyLegacyEnv = "FAAS_EDGE_CHALLENGE_KEY_FILE"
)

// loadGatewayFleetKey returns the secret every gatewayd-internal of a fleet
// shares (ADR-20261102191500): edge challenge tokens and passes, and managed
// deadline tokens, are derived from it under separate labels. A pass or a
// deadline one gateway signs is checked by another, so a per-host key does
// not work, and the gateway no longer holds the session key to fall back on.
// Without the file the gateway refuses to start.
//
// The file is the root-only credential Ansible installs from CD's
// GATEWAYD_EDGE_CHALLENGE_KEY secret: 32 bytes as 64 hex digits.
func loadGatewayFleetKey(getenv func(string) string) ([]byte, error) {
	name := gatewayFleetKeyEnv
	path := strings.TrimSpace(getenv(name))
	if path == "" {
		name = gatewayFleetKeyLegacyEnv
		path = strings.TrimSpace(getenv(name))
	}
	if path == "" {
		return nil, fmt.Errorf("%s is unset: gatewayd-internal needs the gateway fleet key to sign edge challenges and traffic deadlines (ADR-20261102191500)", gatewayFleetKeyEnv)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("gateway fleet key file must hold 32 bytes as 64 hex digits")
	}
	return key, nil
}
