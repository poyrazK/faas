package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ErrSafeDeployTokenPairIncomplete identifies a configuration that would
// enable only half of the Safe Deploy control loop. Canary progression and
// safedeploy action dispatch must be enabled together: the latter needs the
// APID client created by the former for promote/demote/rollback actions.
var ErrSafeDeployTokenPairIncomplete = errors.New("meterd: safe-deploy token pair incomplete")
var ErrSafeDeployTokenInvalid = errors.New("meterd: safe-deploy service tokens invalid")

const defaultSafeDeployInternalBaseURL = "http://127.0.0.1:9101"

// safeDeployToken returns an env-provided internal service token in the form
// used by the runtime gates. Whitespace-only values are treated as unset so a
// malformed secret file cannot accidentally enable a partial control loop.
func safeDeployToken(getenv func(string) string, name string) string {
	if getenv == nil {
		return ""
	}
	return strings.TrimSpace(getenv(name))
}

// validateSafeDeployTokenPair enforces the Safe Deploy activation contract.
// Both empty means the feature is intentionally disabled; both present means
// the canary and action paths can be wired atomically. A single token is a
// startup error rather than a degraded mode that could leave rollouts stuck.
func validateSafeDeployTokenPair(canaryToken, safedeployToken string) error {
	canarySet := strings.TrimSpace(canaryToken) != ""
	safedeploySet := strings.TrimSpace(safedeployToken) != ""
	if canarySet == safedeploySet {
		if canarySet && (len(canaryToken) < 32 || len(safedeployToken) < 32 || canaryToken == safedeployToken) {
			return fmt.Errorf("%w: tokens must be distinct and at least 32 bytes each", ErrSafeDeployTokenInvalid)
		}
		return nil
	}
	if canarySet {
		return fmt.Errorf("%w: FAAS_CANARY_PROGRESSION_TOKEN is set but FAAS_SAFEDEPLOY_TOKEN is empty", ErrSafeDeployTokenPairIncomplete)
	}
	return fmt.Errorf("%w: FAAS_SAFEDEPLOY_TOKEN is set but FAAS_CANARY_PROGRESSION_TOKEN is empty", ErrSafeDeployTokenPairIncomplete)
}

func safeDeployInternalBaseURL(getenv func(string) string) string {
	if getenv != nil {
		if raw := strings.TrimSpace(getenv("FAAS_APID_INTERNAL_BASE_URL")); raw != "" {
			return raw
		}
	}
	return defaultSafeDeployInternalBaseURL
}

func validateSafeDeployInternalBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("meterd: Safe Deploy APID operator URL must be a plain loopback HTTP origin")
	}
	host := net.ParseIP(u.Hostname())
	port, portErr := strconv.Atoi(u.Port())
	if host == nil || !host.IsLoopback() || portErr != nil || port < 1 || port > 65535 {
		return fmt.Errorf("meterd: Safe Deploy APID operator URL must be a plain loopback HTTP origin")
	}
	return nil
}
