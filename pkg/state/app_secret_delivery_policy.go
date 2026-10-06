package state

import "fmt"

// SelectAppSecretsForDelivery applies the binding privilege boundary before
// ciphertext reaches a workload. The release flag comes from a persisted task
// kind. An explicit serving reference cannot opt into migration credentials.
// Release tasks receive migration bindings even when the serving allowlist
// restricts ordinary secrets.
func SelectAppSecretsForDelivery(rows []AppSecret, requested map[string]string, release bool) ([]AppSecret, error) {
	selected := make([]AppSecret, 0, len(rows))
	found := make(map[string]bool, len(rows))
	for _, row := range rows {
		migration := false
		if row.ManagedPostgresBindingID != "" {
			switch row.ManagedPostgresAccess {
			case "read_write", "read_only":
			case "migration":
				migration = true
			default:
				return nil, fmt.Errorf("managed PostgreSQL secret %q has unavailable access metadata", row.Key)
			}
		}
		_, explicit := requested[row.Key]
		if migration && !release {
			if explicit {
				return nil, fmt.Errorf("secret %q is restricted to release tasks", row.Key)
			}
			continue
		}
		if len(requested) > 0 && !explicit && (!release || !migration) {
			continue
		}
		selected = append(selected, row)
		found[row.Key] = true
	}
	for key := range requested {
		if !found[key] {
			return nil, fmt.Errorf("missing app_secrets row for %q", key)
		}
	}
	return selected, nil
}
