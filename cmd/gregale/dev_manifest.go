package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func loadDevManifest(sourceDir string) (*gregalemanifest.Manifest, error) {
	manifest, present, err := gregalemanifest.Load(sourceDir)
	if err != nil {
		return nil, err
	}
	if !present || manifest == nil {
		return nil, nil
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("gregale.yaml: %w", err)
	}
	return manifest, nil
}

func flagSetWasSet(fs *flag.FlagSet) map[string]bool {
	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
	})
	return set
}

func applyDevManifestDefaults(manifest *gregalemanifest.Manifest, explicit map[string]bool, sourceDir string, envFile, serviceOverrideFile *string, postgres *bool, postgresRegion, ttl *string) {
	if manifest == nil || manifest.Dev == nil {
		return
	}
	dev := manifest.Dev
	if !explicit["env-file"] && dev.EnvFile != "" {
		*envFile = filepath.Join(sourceDir, dev.EnvFile)
	}
	if !explicit["service-override-file"] && dev.ServiceOverrideFile != "" {
		*serviceOverrideFile = filepath.Join(sourceDir, dev.ServiceOverrideFile)
	}
	if !explicit["postgres"] && dev.Postgres != nil {
		*postgres = *dev.Postgres
	}
	if !explicit["postgres-region"] && (!explicit["postgres"] || *postgres) {
		*postgresRegion = dev.PostgresRegion
	}
	if !explicit["ttl"] && dev.TTL != "" {
		*ttl = dev.TTL
	}
}

// resolveDevTTL validates `--ttl`/`dev.ttl` locally before any remote
// mutation. Zero means "not chosen": the API keeps its default lease.
func resolveDevTTL(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	return gregalemanifest.ParseDevTTL(raw)
}

// formatDevLease renders a lease the way developers type it (72h, 168h)
// rather than Go's 72h0m0s.
func formatDevLease(lease time.Duration) string {
	if lease <= 0 {
		lease = api.DeveloperLeaseDefault
	}
	if lease%time.Hour == 0 {
		return fmt.Sprintf("%dh", lease/time.Hour)
	}
	return lease.String()
}

// applyDevSeedManifestDefault applies dev.postgres_seed unless the command was
// given explicitly. It is separate from applyDevManifestDefaults because only
// the watch loop runs a seed; `gregale dev setup` hands off to it unchanged.
func applyDevSeedManifestDefault(manifest *gregalemanifest.Manifest, explicit map[string]bool, postgresSeed *string) {
	if manifest == nil || manifest.Dev == nil || explicit["postgres-seed"] {
		return
	}
	*postgresSeed = manifest.Dev.PostgresSeed
}

func resolveDevSourceConfigWithManifest(sourceDir string) (devSourceConfig, error) {
	if _, err := loadDevManifest(sourceDir); err != nil {
		return devSourceConfig{}, err
	}
	return resolveDevSourceConfig(sourceDir)
}
