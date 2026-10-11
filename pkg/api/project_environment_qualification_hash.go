package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// QualificationWorkloadConfigHash binds tested workload settings to the flag
// snapshot observed before probes. The settings hash itself remains unchanged
// in deployment pins and environment state. An empty flag hash is the legacy
// identity, permitted only while an environment has never published flags.
func QualificationWorkloadConfigHash(settingsHash, featureFlagsHash string) (string, error) {
	if !ValidProjectEnvironmentConfigHash(settingsHash) || (featureFlagsHash != "" && !ValidProjectEnvironmentConfigHash(featureFlagsHash)) {
		return "", fmt.Errorf("invalid qualification configuration identity")
	}
	if featureFlagsHash == "" {
		return settingsHash, nil
	}
	hash := sha256.Sum256([]byte("gregale.dev/environment-workload-qualification/v1\x00" + settingsHash + "\x00" + featureFlagsHash))
	return hex.EncodeToString(hash[:]), nil
}
