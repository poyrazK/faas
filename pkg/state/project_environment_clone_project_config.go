package state

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

// The source version's hash is its existing identity. JSONB may normalize
// numeric spellings, so the workload snapshot separately authenticates the
// actual stored values rather than deriving a new source version identity.
type projectCloneProjectConfig struct {
	Hash         string                    `json:"hash"`
	Values       json.RawMessage           `json:"values"`
	FeatureFlags *projectCloneFeatureFlags `json:"feature_flags,omitempty"`
}

func normalizeCloneProjectConfig(config projectCloneProjectConfig) (projectCloneProjectConfig, error) {
	if !api.ValidProjectEnvironmentConfigHash(config.Hash) {
		return projectCloneProjectConfig{}, ErrConflict
	}
	values, _, err := api.NormalizeProjectEnvironmentConfig(config.Values)
	if err != nil {
		return projectCloneProjectConfig{}, ErrConflict
	}
	config.Values = values
	if config.FeatureFlags != nil {
		snapshot, err := normalizeCloneFeatureFlags(*config.FeatureFlags)
		if err != nil {
			return projectCloneProjectConfig{}, err
		}
		config.FeatureFlags = &snapshot
	}
	return config, nil
}

func capturedCloneProjectConfig(records []projectCloneWorkloadRecord) (projectCloneProjectConfig, error) {
	if len(records) == 0 || records[0].snapshot.ProjectConfig == nil {
		return projectCloneProjectConfig{}, ErrConflict
	}
	captured, err := normalizeCloneProjectConfig(*records[0].snapshot.ProjectConfig)
	if err != nil {
		return captured, err
	}
	_, expectedValuesHash, _ := api.NormalizeProjectEnvironmentConfig(captured.Values)
	for _, record := range records {
		if record.snapshot.ProjectConfig == nil || record.snapshot.ProjectConfig.Hash != captured.Hash {
			return projectCloneProjectConfig{}, ErrConflict
		}
		_, hash, err := api.NormalizeProjectEnvironmentConfig(record.snapshot.ProjectConfig.Values)
		normalized, normalizeErr := normalizeCloneProjectConfig(*record.snapshot.ProjectConfig)
		if err != nil || hash != expectedValuesHash || normalizeErr != nil || cloneFeatureFlagsHash(normalized.FeatureFlags) != cloneFeatureFlagsHash(captured.FeatureFlags) {
			return projectCloneProjectConfig{}, ErrConflict
		}
	}
	return captured, nil
}

func validateCloneProjectConfigProof(op ProjectEnvironmentCloneOperation, resources []ProjectEnvironmentCloneResource, records []projectCloneWorkloadRecord, target ProjectEnvironmentConfig) error {
	if len(records) == 0 {
		return nil
	}
	captured, err := capturedCloneProjectConfig(records)
	if err != nil {
		return err
	}
	declared := false
	for _, resource := range resources {
		if resource.Kind != "project_config" {
			continue
		}
		if declared || resource.Name != op.SourceEnvironment || resource.SourceVersion != captured.Hash {
			return ErrConflict
		}
		declared = true
	}
	_, capturedValuesHash, _ := api.NormalizeProjectEnvironmentConfig(captured.Values)
	_, targetValuesHash, err := api.NormalizeProjectEnvironmentConfig(target.Values)
	if !declared || target.ID == "" || target.ConfigHash != captured.Hash || err != nil || targetValuesHash != capturedValuesHash {
		return ErrConflict
	}
	return nil
}
