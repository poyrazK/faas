package main

import (
	"flag"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestApplyDevManifestDefaultsUsesManifestOnlyForUnsetFlags(t *testing.T) {
	postgres := true
	manifest := &gregalemanifest.Manifest{Dev: &gregalemanifest.DevConfig{
		EnvFile:             ".env.dev",
		ServiceOverrideFile: ".env.services.local",
		Postgres:            &postgres,
		PostgresRegion:      "eu-central-1",
		TTL:                 "72h",
	}}
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	envFile := fs.String("env-file", "", "")
	serviceFile := fs.String("service-override-file", "", "")
	withPostgres := fs.Bool("postgres", false, "")
	postgresRegion := fs.String("postgres-region", "", "")
	ttl := fs.String("ttl", "", "")
	if err := fs.Parse([]string{"--env-file", "cli.env", "--postgres=false", "--ttl", "168h"}); err != nil {
		t.Fatal(err)
	}
	applyDevManifestDefaults(manifest, flagSetWasSet(fs), "/workspace/api", envFile, serviceFile, withPostgres, postgresRegion, ttl)
	if *envFile != "cli.env" {
		t.Fatalf("env file = %q, want explicit CLI value", *envFile)
	}
	if *serviceFile != filepath.Join("/workspace/api", ".env.services.local") {
		t.Fatalf("service file = %q, want manifest path resolved under source root", *serviceFile)
	}
	if *ttl != "168h" {
		t.Fatalf("ttl = %q, want explicit CLI value", *ttl)
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
		TTL:                 "72h",
	}}
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	envFile := fs.String("env-file", "", "")
	serviceFile := fs.String("service-override-file", "", "")
	withPostgres := fs.Bool("postgres", false, "")
	postgresRegion := fs.String("postgres-region", "", "")
	ttl := fs.String("ttl", "", "")
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	applyDevManifestDefaults(manifest, flagSetWasSet(fs), "/workspace/api", envFile, serviceFile, withPostgres, postgresRegion, ttl)
	if *envFile != filepath.Join("/workspace/api", ".env.dev") || *serviceFile != filepath.Join("/workspace/api", ".env.services.local") || !*withPostgres || *postgresRegion != "eu-central-1" || *ttl != "72h" {
		t.Fatalf("defaults = %q/%q/%t/%q, want manifest values", *envFile, *serviceFile, *withPostgres, *postgresRegion)
	}
}

func TestResolveDevTTL(t *testing.T) {
	cases := []struct {
		raw     string
		want    time.Duration
		wantErr string
	}{
		{raw: "", want: 0},
		{raw: "1h", want: time.Hour},
		{raw: "72h", want: 72 * time.Hour},
		{raw: "90m", want: 90 * time.Minute},
		{raw: "59m", wantErr: "at least"},
		{raw: "-2h", wantErr: "at least"},
		{raw: "3d", wantErr: "not a duration"},
		{raw: "1h0.5s", wantErr: "whole number of seconds"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := resolveDevTTL(tc.raw)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("resolveDevTTL(%q) error = %v, want %q", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("resolveDevTTL(%q) = %s, %v; want %s", tc.raw, got, err, tc.want)
			}
		})
	}
}

func TestFormatDevLease(t *testing.T) {
	for lease, want := range map[time.Duration]string{0: "24h", 72 * time.Hour: "72h", 90 * time.Minute: "1h30m0s"} {
		if got := formatDevLease(lease); got != want {
			t.Fatalf("formatDevLease(%s) = %q, want %q", lease, got, want)
		}
	}
}

func TestDevSessionRequestCarriesLease(t *testing.T) {
	config := devSourceConfig{shape: shapeApp}
	if got := config.sessionRequest("", false, "", 72*time.Hour, nil).LeaseSeconds; got != 72*3600 {
		t.Fatalf("lease_seconds = %d, want %d", got, 72*3600)
	}
	if got := config.sessionRequest("", false, "", 0, nil).LeaseSeconds; got != 0 {
		t.Fatalf("lease_seconds without --ttl = %d, want omitted", got)
	}
}
