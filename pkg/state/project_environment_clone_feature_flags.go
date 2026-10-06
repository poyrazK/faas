package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
)

// The source history belongs to its environment. A clone starts its own history
// at version one, retaining seeds so percentage and variant assignments match.
type projectCloneFeatureFlags struct {
	Version             int          `json:"version"`
	SourceEnvironmentID string       `json:"source_environment_id"`
	SourceVersion       int64        `json:"source_version"`
	Config              flags.Config `json:"config"`
}

func normalizeCloneFeatureFlags(snapshot projectCloneFeatureFlags) (projectCloneFeatureFlags, error) {
	canonicalID := func(value string) bool {
		id, err := uuid.Parse(value)
		return err == nil && id != uuid.Nil && id.String() == value
	}
	// MemStore uses compact UUID environment identities; retain that exact
	// identity while seeds and customer IDs use their canonical wire spelling.
	if snapshot.Version != 1 || !validCloneCredentialSourceID(snapshot.SourceEnvironmentID) || snapshot.SourceVersion < 0 || snapshot.SourceVersion > api.FlagsMaxConfigVersion ||
		(snapshot.SourceVersion == 0 && (len(snapshot.Config.Flags) != 0 || len(snapshot.Config.Groups) != 0)) {
		return projectCloneFeatureFlags{}, ErrConflict
	}
	raw, err := json.Marshal(snapshot.Config)
	if err != nil || len(raw) > api.FlagsMaxBundleBytes {
		return projectCloneFeatureFlags{}, ErrConflict
	}
	if err := json.Unmarshal(raw, &snapshot.Config); err != nil {
		return projectCloneFeatureFlags{}, ErrConflict
	}
	if snapshot.Config.Flags == nil {
		snapshot.Config.Flags = []flags.Flag{}
	}
	if snapshot.Config.Groups == nil {
		snapshot.Config.Groups = map[string][]string{}
	}
	if flags.Validate(snapshot.Config) != nil {
		return projectCloneFeatureFlags{}, ErrConflict
	}
	for _, flag := range snapshot.Config.Flags {
		if !canonicalID(flag.Seed) {
			return projectCloneFeatureFlags{}, ErrConflict
		}
	}
	for _, id := range flagCustomerIDs(snapshot.Config) {
		if !canonicalID(id) {
			return projectCloneFeatureFlags{}, ErrConflict
		}
	}
	return snapshot, nil
}

func captureCloneFeatureFlags(source FeatureFlagVersion) (projectCloneFeatureFlags, error) {
	return normalizeCloneFeatureFlags(projectCloneFeatureFlags{Version: 1, SourceEnvironmentID: source.EnvironmentID, SourceVersion: source.Version, Config: source.Config})
}

func cloneFeatureFlagsHash(snapshot *projectCloneFeatureFlags) string {
	if snapshot == nil {
		return ""
	}
	raw, _ := json.Marshal(snapshot) // only normalized snapshots reach this helper
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func validateCloneFeatureFlagsProof(snapshot *projectCloneFeatureFlags, target FeatureFlagVersion) error {
	if snapshot == nil || target.Version != 1 || target.Actor != "environment-clone" || target.RestoredFrom != 0 {
		return ErrConflict
	}
	// Compare configuration separately from each environment's version identity.
	expected, err := json.Marshal(snapshot.Config)
	if err != nil {
		return ErrConflict
	}
	actual, err := json.Marshal(target.Config)
	if err != nil || string(expected) != string(actual) {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) latestFeatureFlagsLocked(scope FeatureFlagScope) FeatureFlagVersion {
	rows := m.featureFlagVersions[scope.EnvironmentID]
	if len(rows) == 0 {
		return emptyFeatureFlags(scope)
	}
	return cloneFeatureFlags(rows[len(rows)-1])
}

func (m *MemStore) copyCloneFeatureFlagsLocked(target ProjectEnvironment, snapshot projectCloneFeatureFlags) {
	if m.featureFlagVersions == nil {
		m.featureFlagVersions = map[string][]FeatureFlagVersion{}
	}
	m.featureFlagVersions[target.ID] = []FeatureFlagVersion{{Bundle: flags.Bundle{EnvironmentID: target.ID, Version: 1, Config: snapshot.Config},
		Actor: "environment-clone", CreatedAt: target.CreatedAt}}
}
