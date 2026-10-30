package objectstorage

import (
	"fmt"
	"slices"
)

// validatePool checks one region's ordered placement pool
// (ADR-20261029174627). A bucket's features must not depend on which backend
// it landed on, so the members of a multi-backend pool share their driver,
// Object Lock settings, encryption algorithms and allowed origins, carry no
// customer encryption keys (a key lives in one provider account), and each
// declare the ceiling that moves placement to the next one.
func validatePool(region string, ids []string, defaults map[string]string, configs map[string]BackendConfig) error {
	if len(ids) == 0 {
		return fmt.Errorf("object storage: pool for region %s is empty", region)
	}
	if id, ok := defaults[region]; ok && id != ids[0] {
		return fmt.Errorf("object storage: region %s's default must be its pool's first backend", region)
	}
	first := configs[ids[0]]
	seen := map[string]bool{}
	for _, id := range ids {
		b, ok := configs[id]
		if !ok || b.Region != region || seen[id] {
			return fmt.Errorf("object storage: pool for region %s names a missing, repeated or other-region backend", region)
		}
		seen[id] = true
		if len(ids) == 1 {
			continue
		}
		if b.MaxBuckets == 0 {
			return fmt.Errorf("object storage: backend %s in region %s's pool needs max_buckets", id, region)
		}
		if !samePlacementFeatures(first, b) {
			return fmt.Errorf("object storage: backend %s does not offer the same features as the rest of region %s's pool", id, region)
		}
	}
	return nil
}

func samePlacementFeatures(a, b BackendConfig) bool {
	if a.Driver != b.Driver || a.ObjectLock != b.ObjectLock || len(a.Encryption.Keys) != 0 || len(b.Encryption.Keys) != 0 {
		return false
	}
	return sameSet(a.Encryption.Algorithms, b.Encryption.Algorithms) && sameSet(a.AllowedOrigins, b.AllowedOrigins)
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}
