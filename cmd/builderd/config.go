package main

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Config is the on-disk representation of /etc/faas/builderd.toml. Every
// field has a working default so a missing or partial file still yields a
// runnable daemon.
type Config struct {
	// VMMDSocket is the vmmd gRPC socket builderd dials to spawn builder VMs.
	// Defaults to /run/faas/vmmd.sock — the same socket schedd uses
	// (ADR-014/015).
	VMMDSocket string `toml:"vmmd_socket"`
	// CacheDir is the on-disk root for content-addressed build cache.
	// Defaults to /var/cache/faas/builds.
	CacheDir string `toml:"cache_dir"`
	// MetricsAddr is the optional bind address for /metrics. Empty disables it.
	MetricsAddr string `toml:"metrics_addr"`
	// DBURL is the Postgres DSN; empty falls back to $DATABASE_URL (db.Open).
	DBURL string `toml:"db_url"`
}

// LoadConfig reads a TOML file at path with defaults filled in. A missing
// file is not an error — the defaults produce a working daemon.
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		VMMDSocket: "/run/faas/vmmd.sock",
		CacheDir:   "/var/cache/faas/builds",
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("builderd: read %q: %w", path, err)
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("builderd: parse %q: %w", path, err)
	}
	return c, nil
}
