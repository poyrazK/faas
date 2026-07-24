package main

import (
	"crypto/tls"
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Config is the on-disk representation of /etc/faas/builderd.toml. Every
// field has a working default so a missing or partial file still yields a
// runnable daemon.
type Config struct {
	// VMMDSocket is the vmmd gRPC socket builderd dials to spawn builder VMs
	// when VMMTarget is empty. Defaults to /run/faas/vmmd.sock — the same
	// socket schedd uses (ADR-014/015).
	VMMDSocket string `toml:"vmmd_socket"`
	// VMMTarget is the location-transparent gRPC dial target for vmmd
	// (issue #95, ADR-025). When non-empty, takes precedence over
	// VMMDSocket.
	VMMTarget string `toml:"vmmd_target"`
	// Client-mTLS material for the vmmd dial (issue #95). All three
	// paths empty => no TLS; all three set => mTLS. Partial cluster
	// => startup error.
	TLSCertPath string `toml:"tls_cert_path"`
	TLSKeyPath  string `toml:"tls_key_path"`
	TLSCAPath   string `toml:"tls_ca_path"`
	// CacheDir is the on-disk root for content-addressed build cache.
	// Defaults to /var/cache/faas/builds.
	CacheDir string `toml:"cache_dir"`
	// MetricsAddr is the optional bind address for /metrics. Empty disables it.
	MetricsAddr string `toml:"metrics_addr"`
	// DBURL is the Postgres DSN; empty falls back to $DATABASE_URL (db.Open).
	DBURL string `toml:"db_url"`
	// BuilderBase is drive0: the read-only shared base rootfs the builder VM
	// boots from. Built once from images/builder-base.Dockerfile by imaged;
	// staged to /srv/fc/base/builder-base.ext4 (the default).
	BuilderBase string `toml:"builder_base"`
	// BuildDriveDir hosts the per-VM drive1 tmp files builderd creates at
	// Spawn time. /var/lib/faas/build-drive (default).
	BuildDriveDir string `toml:"build_drive_dir"`
	// BuildExportDir is the parent of all per-build export directories. vmmd
	// writes <dir>/<build_id>/build-done.json + /build/out/* here during
	// Destroy. /var/lib/faas/build-out (default).
	BuildExportDir string `toml:"build_export_dir"`
	// ScheddMetricsURL is where builderd polls schedd's /metrics
	// endpoint for the fcvm_resident_ram_pct gauge (spec §4.5
	// opportunistic-slot rule).
	//
	// Schedd mounts the daemon's own ops counters at /metrics and the
	// fcvm_* dashboard gauges at /metrics/fcvm (see cmd/schedd/main.go).
	// The default therefore includes the /fcvm subpath — pointing at
	// /metrics silently strips the opportunistic slot because
	// parseResidentPct never finds the gauge there.
	//
	// Empty disables the 2nd slot — same behaviour as the pre-fix
	// nil-probe path.
	ScheddMetricsURL string `toml:"schedd_metrics_url"`
	// PollInterval is the cadence of the durable worker (PR-B) that
	// scans the build queue via SELECT … FOR UPDATE SKIP LOCKED. The
	// fast path remains LISTEN/NOTIFY (apid's emit on build_queued);
	// this worker is the recovery net for missed notify / apid
	// crashed mid-deploy / Postgres restart windows. Zero falls back
	// to 2 s in main.go — well below the pg_notify RTT on the EX44
	// (≈200 ms) so the worker is the safety net, not the primary.
	PollInterval time.Duration `toml:"poll_interval"`
}

// ResolveVMMTarget returns the dial target for vmmd. VMMTarget wins
// when set; otherwise unix://+VMMDSocket.
func (c *Config) ResolveVMMTarget() string {
	if c.VMMTarget != "" {
		return c.VMMTarget
	}
	return "unix://" + c.VMMDSocket
}

// LoadVMMTLS returns the client mTLS config builderd uses to dial vmmd.
// Empty cluster returns (nil, nil); partial cluster is rejected.
func (c *Config) LoadVMMTLS() (*tls.Config, error) {
	return wire.LoadClientTLSConfig(c.TLSCertPath, c.TLSKeyPath, c.TLSCAPath)
}

// LoadConfig reads a TOML file at path with defaults filled in. A missing
// file is not an error — the defaults produce a working daemon.
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		VMMDSocket:       "/run/faas/vmmd.sock",
		CacheDir:         "/var/cache/faas/builds",
		BuilderBase:      "/srv/fc/base/builder-base.ext4",
		BuildDriveDir:    "/var/lib/faas/build-drive",
		BuildExportDir:   "/var/lib/faas/build-out",
		ScheddMetricsURL: "http://127.0.0.1:9090/metrics/fcvm",
		PollInterval:     2 * time.Second,
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
