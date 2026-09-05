package main

import (
	"errors"
	"fmt"
	"strings"
)

// ErrSafeDeployTokenPairIncomplete identifies a configuration that would
// enable only half of the Safe Deploy control loop. Canary progression and
// safedeploy action dispatch must be enabled together: the latter needs the
// APID client created by the former for promote/demote/rollback actions.
var ErrSafeDeployTokenPairIncomplete = errors.New("meterd: safe-deploy token pair incomplete")

// safeDeployToken returns an env-provided service-account token in the form
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
		return nil
	}
	if canarySet {
		return fmt.Errorf("%w: FAAS_CANARY_PROGRESSION_TOKEN is set but FAAS_SAFEDEPLOY_TOKEN is empty", ErrSafeDeployTokenPairIncomplete)
	}
	return fmt.Errorf("%w: FAAS_SAFEDEPLOY_TOKEN is set but FAAS_CANARY_PROGRESSION_TOKEN is empty", ErrSafeDeployTokenPairIncomplete)
}
