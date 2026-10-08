package main

import (
	"flag"
	"fmt"
	"path/filepath"

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

func applyDevManifestDefaults(manifest *gregalemanifest.Manifest, explicit map[string]bool, sourceDir string, envFile, serviceOverrideFile *string, postgres *bool, postgresRegion *string) {
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
