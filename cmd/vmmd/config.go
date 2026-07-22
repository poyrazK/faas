// Package main's config — parsed from /etc/faas/vmmd.toml (or the path
// passed via --config). Each field is independent of every other so a
// partial config file plus defaults produces a working daemon.

package main

import (
	"crypto/tls"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Config is the on-disk representation of the daemon's TOML config.
// File reads use pelletier/go-toml/v2 (already a transitive dep of
// many tools; pinning it here makes the daemon's config story
// explicit).
type Config struct {
	// SocketPath is the unix-domain socket the gRPC server binds when
	// ListenAddr is empty. Defaults to /run/faas/vmmd.sock.
	// ADR-015 dictates mode 0660 group `faas`.
	SocketPath string `toml:"socket_path"`

	// ListenAddr is the location-transparent gRPC listen target
	// (issue #95, ADR-025). Accepts unix:///path, tcp://host:port, or
	// dns:///host:port (the latter only valid for dial, never bind).
	// When empty, falls back to unix://+SocketPath for backwards
	// compatibility. tcp targets require all TLS paths to be set.
	ListenAddr string `toml:"listen_addr"`

	// MetricsAddr is the optional bind address for /metrics.
	// Empty disables the metrics endpoint.
	MetricsAddr string `toml:"metrics_addr"`

	// OwnerUser is the uid that owns the socket file. Defaults to
	// the daemon's own uid (lookups by name first). Only consulted
	// when the resolved listen target is a unix socket.
	OwnerUser string `toml:"owner_user"`

	// Server-mTLS material (issue #95). All three paths empty =>
	// no TLS; all three set => RequireAndVerifyClientCert. Partial
	// cluster => startup error naming the missing fields.
	TLSCertPath string `toml:"tls_cert_path"`
	TLSKeyPath  string `toml:"tls_key_path"`
	TLSCAPath   string `toml:"tls_ca_path"`

	// KernelKey is the StorageBackend key for the Firecracker kernel
	// artifact vmmd loads on cold boot (issue #96 / ADR-025 axis 2 / PR
	// #116). The local backend's Get resolves it to the same file the
	// legacy KernelPath config used (so single-box behaviour is
	// preserved); the OCI backend fetches over HTTP. Derived from
	// sched.KernelKey(fcVersion) at startup once the running FC version
	// is detected (cmd/vmmd/main.go). Overridable via toml for tests
	// that pin a specific kernel key.
	KernelKey string `toml:"kernel_key"`
	// KernelPath mirrors pkg/fcvm.Paths.Kernel. Deprecated: with #96
	// (PR #116) the kernel flows through StorageBackend like every
	// other artifact. Kept for source compatibility with existing
	// vmmd.toml fixtures; main.go resolves KernelKey after FC version
	// detection and prefers it. Startup logs both so an operator can
	// spot drift between the two.
	KernelPath string `toml:"kernel_path"`

	// ComputeNode is the vmmd self-registration block (issue #98 /
	// ADR-028). vmmd Upserts its own row in compute_nodes at startup
	// so schedd knows it exists without an operator POST. Empty
	// NodeName = "no self-registration" (legacy single-box dev path
	// that relies on the default-local seed from migration 00024).
	ComputeNode ComputeNodeConfig `toml:"compute_node"`

	// DBURL is the Postgres DSN vmmd uses for the
	// compute_nodes self-registration upsert at startup. Required
	// when [compute_node].name is set; optional when NodeName is
	// empty (the legacy default-local path doesn't need DB access).
	// Default empty; FAAS_VMMD_DBURL env var overrides for the
	// containerised deployments that prefer env-only config.
	DBURL string `toml:"db_url"`
}

// ComputeNodeConfig is the [compute_node] TOML section. Field naming
// tracks pkg/state.ComputeNode 1:1; values flow into the upsert
// after the resource sizing checks (VPCPUs > 0, MemMB > 0, etc.).
type ComputeNodeConfig struct {
	NodeName           string `toml:"name"`                 // defaults to short hostname when empty
	VPCPUs             int    `toml:"vpcpus"`               // total vCPUs this box offers
	MemMB              int    `toml:"mem_mb"`               // total RAM in MiB
	MaxConcurrency     int    `toml:"max_concurrency"`      // parallel live instances
	AdmissionCeilingMB int    `toml:"admission_ceiling_mb"` // Σ(ram_mb + 8) cap
	OverlayIP          string `toml:"overlay_ip"`           // Tailscale/Wireguard IP; auto-detected when empty
}

// ResolveListenTarget returns the gRPC target the server should bind.
// ListenAddr wins when set; otherwise unix://+SocketPath. Returns the
// resolved target string (already wire.ParseTarget-compatible).
func (c *Config) ResolveListenTarget() string {
	if c.ListenAddr != "" {
		return c.ListenAddr
	}
	return "unix://" + c.SocketPath
}

// LoadServerTLS returns the server's mTLS config when all three paths
// are set, or (nil, nil) when none are set. A partial cluster is
// rejected — the wire helper returns the error verbatim so callers see
// which fields are missing.
func (c *Config) LoadServerTLS() (*tls.Config, error) {
	return wire.LoadServerTLSConfig(c.TLSCertPath, c.TLSKeyPath, c.TLSCAPath)
}

// LoadConfig reads a TOML file at path and returns the parsed Config with
// defaults filled in. A missing file is not an error if defaults suffice;
// in that case an empty config is returned.
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		SocketPath: "/run/faas/vmmd.sock",
		// KernelPath is the deprecated host-path default; main.go
		// resolves KernelKey from sched.KernelKey(fcVersion) after FC
		// detection. The default here keeps pre-#116 vmmd.toml
		// fixtures working until operators migrate.
		KernelPath: "/srv/fc/base/vmlinux-6.1",
		OwnerUser:  "faas-vmmd",
		ComputeNode: ComputeNodeConfig{
			// Defaults match the synthetic default-local row seeded
			// by migration 00024 so single-box dev (no overlay)
			// still has a coherent self-registration on first boot.
			// Operators scaling beyond one box override every
			// [compute_node] field explicitly via vmmd.toml.
			VPCPUs:             160,
			MemMB:              56000,
			MaxConcurrency:     200,
			AdmissionCeilingMB: 47600,
		},
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("vmmd: read %q: %w", path, err)
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("vmmd: parse %q: %w", path, err)
	}
	return c, nil
}
