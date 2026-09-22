package main

import (
	"flag"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestApplyDevManifestDefaultsUsesManifestOnlyForUnsetFlags(t *testing.T) {
	postgres := true
	manifest := &gregalemanifest.Manifest{Dev: &gregalemanifest.DevConfig{
		EnvFile:             ".env.dev",
		ServiceOverrideFile: ".env.services.local",
		Postgres:            &postgres,
		PostgresRegion:      "eu-central-1",
	}}
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	envFile := fs.String("env-file", "", "")
	serviceFile := fs.String("service-override-file", "", "")
	withPostgres := fs.Bool("postgres", false, "")
	postgresRegion := fs.String("postgres-region", "", "")
	if err := fs.Parse([]string{"--env-file", "cli.env", "--postgres=false"}); err != nil {
		t.Fatal(err)
	}
	applyDevManifestDefaults(manifest, flagSetWasSet(fs), "/workspace/api", envFile, serviceFile, withPostgres, postgresRegion)
	if *envFile != "cli.env" {
		t.Fatalf("env file = %q, want explicit CLI value", *envFile)
	}
	if *serviceFile != filepath.Join("/workspace/api", ".env.services.local") {
		t.Fatalf("service file = %q, want manifest path resolved under source root", *serviceFile)
	}
	if *withPostgres || *postgresRegion != "" {
		t.Fatalf("postgres defaults = %t/%q, explicit --postgres=false must disable manifest defaults", *withPostgres, *postgresRegion)
	}
}

func TestApplyDevManifestDefaultsFillsUnsetFlags(t *testing.T) {
	postgres := true
	manifest := &gregalemanifest.Manifest{Dev: &gregalemanifest.DevConfig{
		EnvFile:             ".env.dev",
		ServiceOverrideFile: ".env.services.local",
		Postgres:            &postgres,
		PostgresRegion:      "eu-central-1",
	}}
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	envFile := fs.String("env-file", "", "")
	serviceFile := fs.String("service-override-file", "", "")
	withPostgres := fs.Bool("postgres", false, "")
	postgresRegion := fs.String("postgres-region", "", "")
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	applyDevManifestDefaults(manifest, flagSetWasSet(fs), "/workspace/api", envFile, serviceFile, withPostgres, postgresRegion)
	if *envFile != filepath.Join("/workspace/api", ".env.dev") || *serviceFile != filepath.Join("/workspace/api", ".env.services.local") || !*withPostgres || *postgresRegion != "eu-central-1" {
		t.Fatalf("defaults = %q/%q/%t/%q, want manifest values", *envFile, *serviceFile, *withPostgres, *postgresRegion)
	}
}
