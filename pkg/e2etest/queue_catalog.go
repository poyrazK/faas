package e2etest

import "github.com/onebox-faas/faas/pkg/managedpostgres"

// QueueGuestCatalogConfig initializes complete binding inventory and its shared
// promotion fence. This catalog has no database rows, provisioning, usage or
// health collection. The unused backend uses no real organization/credential.
func QueueGuestCatalogConfig() managedpostgres.Config {
	return managedpostgres.Config{DefaultRegion: "queue-fixture", Health: managedpostgres.HealthConfig{Enabled: new(bool)}, Defaults: map[string]string{"queue-fixture": "queue-unused"},
		Backends: []managedpostgres.BackendConfig{{ID: "queue-unused", Driver: "neon", Region: "queue-fixture", Namespace: "org-queue-native-unused",
			Settings: map[string]string{"region_id": "aws-eu-central-1", "max_storage_bytes": "1073741824", "max_restore_window_seconds": "0"}, SecretEnv: map[string]string{"api-key": "GREGALE_QUEUE_UNUSED_PROVIDER_KEY"}}}}
}
