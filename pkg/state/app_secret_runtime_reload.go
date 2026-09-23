package state

import (
	"encoding/hex"
	"strings"
)

func validAppSecretRuntimeReloadResult(result AppSecretRuntimeReloadResult) bool {
	if result.AccountID == "" || result.AppID == "" || result.InstanceID == "" ||
		!ValidSecretReloadOutcome(result.Revision, result.Projection, result.Signal, result.ErrorCode) {
		return false
	}
	for _, candidate := range result.Candidates {
		if candidate.Scope == "" || candidate.Key == "" || candidate.Version < 1 {
			return false
		}
	}
	return true
}

// ValidSecretReloadOutcome validates the closed, non-sensitive outcome
// vocabulary accepted from guest-init.
func ValidSecretReloadOutcome(revision string, projection SecretReloadProjectionStatus, signal SecretReloadSignalStatus, errorCode string) bool {
	if len(revision) != 64 || strings.ToLower(revision) != revision {
		return false
	}
	if decoded, err := hex.DecodeString(revision); err != nil || len(decoded) != 32 {
		return false
	}
	switch {
	case projection == SecretReloadProjectionFailed && signal == SecretReloadSignalNotAttempted && errorCode == "projection_failed":
	case projection == SecretReloadProjectionUpdated && signal == SecretReloadSignalSent && errorCode == "":
	case projection == SecretReloadProjectionUpdated && signal == SecretReloadSignalQueued && errorCode == "":
	case projection == SecretReloadProjectionUpdated && signal == SecretReloadSignalFailed && errorCode == "signal_failed":
	case projection == SecretReloadProjectionUnchanged && signal == SecretReloadSignalNotAttempted && errorCode == "":
	default:
		return false
	}
	return true
}
