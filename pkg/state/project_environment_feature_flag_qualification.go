package state

// FeatureFlagsQualificationHash includes environment lifetime, flag version,
// and the complete configuration. A publish/restore of identical contents
// still changes the identity and requires new health and smoke evidence.
func FeatureFlagsQualificationHash(source FeatureFlagVersion) (string, error) {
	snapshot, err := captureCloneFeatureFlags(source)
	if err != nil {
		return "", err
	}
	if source.Version == 0 {
		return "", nil
	}
	return cloneFeatureFlagsHash(&snapshot), nil
}
