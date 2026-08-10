package main

import (
	"crypto/tls"
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/role"
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
	// FairnessWindow is the per-account claim window (B2.2 issue #196).
	// A claim prefers accounts whose last claim is older than this
	// window; falling back to FIFO if every queued account is recent.
	// Zero disables the fairness filter (behaves identically to the
	// pre-B2.2 FIFO path). Default 30s — long enough that a single
	// customer's deploy burst can't starve a quieter customer past
	// the §14 queue-wait SLO.
	FairnessWindow time.Duration `toml:"fairness_window"`
	// StuckBuildSweepInterval is the cadence of the stuck-running
	// build reaper (issue #195 B1.4). Zero falls back to 10 minutes
	// in main.go — slow enough to not hammer the DB, fast enough to
	// clean up after a one-off VM crash within an operator's
	// attention span.
	StuckBuildSweepInterval time.Duration `toml:"stuck_build_sweep_interval"`
	// StuckBuildThreshold is the age past which a 'running' build
	// is considered stuck and flipped to 'failed(timeout)' by the
	// reaper. Default 15 minutes — wider than the 10-minute VM
	// build timeout so a slow-but-finishing build isn't swept out
	// from under itself. Configurable because real-world VM
	// crashes can leave rows with started_at several minutes in
	// the past deliberately.
	StuckBuildThreshold time.Duration `toml:"stuck_build_threshold"`
	// CacheMaxBytes is the ceiling on the build cache disk usage
	// (issue #196 B2.1). Once exceeded, the GC sweep evicts
	// oldest-first until under cap. 0 disables the size cap. Default
	// 50 GB matches the spec fleet budget (CLAUDE.md).
	CacheMaxBytes int64 `toml:"cache_max_bytes"`
	// CacheMaxAge is the TTL for cache entries (B2.1). Entries
	// older than this are evicted on every sweep tick, regardless
	// of total size. 0 disables the TTL. Default 30 days, matching
	// api.DefaultInstanceRetention (the only "old enough to forget"
	// threshold the platform has today).
	CacheMaxAge time.Duration `toml:"cache_max_age"`
	// CacheGCSweepInterval is the cadence of the cache GC loop (B2.1).
	// Zero falls back to 24 hours in main.go — daily is the right
	// frequency for a slow disk-bleed control.
	CacheGCSweepInterval time.Duration `toml:"cache_gc_sweep_interval"`
	// BuilderNodeID is the compute_node name stamped onto every
	// build_provenance row (ADR-038, Tier 3 / issue #197 B3.1).
	// Defaults to "default-local" in LoadConfig — the synthetic
	// node NewMemStore + the production single-box both seed with
	// the same name. Operators can override via this field when
	// builderd runs on a non-default node (multi-node deployments
	// post-PR-B).
	BuilderNodeID string `toml:"builder_node_id"`

	// Role is the box shape this builderd inhabits (Gate-B; env
	// override FAAS_BUILDERD_ROLE wins when set). builderd is a
	// compute-only daemon — it refuses to start under
	// RoleControlPlane. RoleSingleBox is the default and lets
	// single-box dev boot unmoved.
	Role role.Role `toml:"role"`
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
		// B2.2 (issue #196): 30s fairness window — wide enough to
		// distinguish a deploy burst from steady-state traffic,
		// narrow enough that one customer's idle window rescues the
		// next customer's queued build without an SLA-busting wait.
		FairnessWindow:          30 * time.Second,
		StuckBuildSweepInterval: 10 * time.Minute,
		StuckBuildThreshold:     15 * time.Minute,
		// B2.1 (issue #196): cache GC. 50 GB cap matches the spec
		// fleet budget. 30-day TTL matches the only other retention
		// constant the platform has (api.DefaultInstanceRetention).
		// 24h sweep cadence is the right frequency for a slow bleed.
		CacheMaxBytes:        50 << 30, // 50 GiB
		CacheMaxAge:          30 * 24 * time.Hour,
		CacheGCSweepInterval: 24 * time.Hour,
		// ADR-038: default compute_node name stamped on provenance
		// rows. "default-local" matches the synthetic node seeded by
		// migrations/00024 + NewMemStore, so a MemStore-backed test
		// and the production single-box write the same value.
		BuilderNodeID: "default-local",
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Gate-B: even on the missing-file path, resolve Role
			// against FAAS_BUILDERD_ROLE so env wins over the
			// empty TOML default. role.FromConfig falls back to
			// RoleSingleBox when the env is unset.
			c.Role = role.FromConfig(string(c.Role), "FAAS_BUILDERD_ROLE")
			return c, nil
		}
		return nil, fmt.Errorf("builderd: read %q: %w", path, err)
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("builderd: parse %q: %w", path, err)
	}
	// Gate-B: resolve Role AFTER toml.Unmarshal so the post-decode
	// c.Role is consulted against FAAS_BUILDERD_ROLE. Setting Role
	// in the defaults-struct literal lets toml.Unmarshal overwrite
	// it, which would silently make the env override dead. The
	// role gate at boot calls role.Require to refuse to start
	// under the wrong box shape.
	c.Role = role.FromConfig(string(c.Role), "FAAS_BUILDERD_ROLE")
	return c, nil
}
