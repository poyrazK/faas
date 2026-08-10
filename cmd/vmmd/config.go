// Package main's config — parsed from /etc/faas/vmmd.toml (or the path
// passed via --config). Each field is independent of every other so a
// partial config file plus defaults produces a working daemon.

package main

import (
	"crypto/tls"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
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

	// ScheddClientTLS is the client mTLS material vmmd uses to dial
	// schedd for the capacity publisher (ADR-052 / issue #95 slice 2).
	// Empty cluster => no TLS, single-box default; full cluster => mTLS
	// to remote schedd. Partial cluster => startup error. The leaf is
	// vmmd's per-role client cert (CommonName "vmmd.faas", EKU
	// ClientAuth only — issued by `gregale pki init` as
	// /etc/faas/tls/vmmd/schedd-client.{crt,key}).
	ScheddClientCertPath string `toml:"schedd_client_cert_path"`
	ScheddClientKeyPath  string `toml:"schedd_client_key_path"`
	ScheddClientCAPath   string `toml:"schedd_client_ca_path"`

	// AdvisoryClientTLS is the client mTLS material vmmd uses to
	// dial apid's advisory listener (ADR-052). Same contract as
	// ScheddClientTLS; the leaf is /etc/faas/tls/vmmd/apid-client.{crt,key}.
	AdvisoryClientCertPath string `toml:"advisory_client_cert_path"`
	AdvisoryClientKeyPath  string `toml:"advisory_client_key_path"`
	AdvisoryClientCAPath   string `toml:"advisory_client_ca_path"`

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

	// ParentMountCap bounds the registry size for the ADR-053
	// parent-mount loopback table (cmd/vmmd/main.go wires
	// pkg/vmmdmount.Registry). Default 16 matches the worst-case
	// staging parallelism on a one-box fleet (a rebuild + the
	// four child runtimes + headroom); operators can dial it up
	// on boxes that drive larger fleet-wide rebuilds.
	ParentMountCap int `toml:"parent_mount_cap"`

	// ParentMountMaxAge is the orphan-sweep threshold. Anything
	// older when the sweep tick fires is force-umounted. Default
	// 30 min — a normal mkfs.ext4 -d over a ~280 MB debian userland
	// takes seconds; a hung imaged child surfaces long before
	// the sweep kicks in.
	ParentMountMaxAge time.Duration `toml:"parent_mount_max_age"`

	// ParentSweepInterval is the cadence of the orphan sweep
	// goroutine. Default 30 s; tight enough to keep the worst-case
	// "hung imaged + orphan mount" window bounded without burning
	// CPU on idle fleets.
	ParentSweepInterval time.Duration `toml:"parent_sweep_interval"`

	// EgressOperatorAllowlist is the path to a TOML file vmmd
	// reads at startup AND on SIGHUP (issue #679 / PR-A). Every
	// CIDR in the file is additive to every tenant's
	// apps.egress_allowlist — operators can ONLY ADD
	// reachability, never subtract. Empty disables the bundle
	// (default — preserves today's behaviour). vmmd issues a
	// Warn at startup if the path is set but the file does
	// not exist (operator may have misconfigured the path).
	//
	// See pkg/vmmd/egress_bundle.go for the bundle loader and
	// /etc/faas/egress/operator_allowlist.toml for the on-disk
	// shape.
	EgressOperatorAllowlist string `toml:"egress_operator_allowlist"`

	// NodeKeyPath is the on-disk path to the slice-3 per-node
	// ECDSA P-256 signing key vmmd uses to sign CapacityReport
	// (ADR-053). Defaults to defaultNodeKeyPath
	// (/etc/faas/secrets/vmmd/node.key); operators can override
	// here when the canonical install path is wrong (e.g.
	// Air-gapped fleet whose PKI is rooted on a read-only mount,
	// or a CI fixture that wants the key in a tmpdir). Mode is
	// strictly 0400 root:root — loadNodeSigningKey refuses
	// anything looser.
	//
	// Wired alongside TLSCertPath on purpose: both are
	// daemon-level secrets (not per-tenant, not per-app), and
	// the canonical install puts both under /etc/faas/secrets/.
	// Env override `FAAS_VMMD_NODE_KEY_PATH` continues to win
	// over the toml value for the containerised-deploys path
	// (no toml in those images).
	NodeKeyPath string `toml:"node_key_path"`

	// Role is the box shape this vmmd inhabits (Gate-B; env
	// override FAAS_VMMD_ROLE wins when set). vmmd is a
	// compute-only daemon — it refuses to start under
	// RoleControlPlane. RoleSingleBox is the default and lets
	// single-box dev boot unmoved. The host_vars setting
	// `faas_box_role: compute-only` propagates through ansible
	// to FAAS_VMMD_ROLE on the vmmd unit; a missing env keeps
	// the field at RoleSingleBox.
	Role role.Role `toml:"role"`
}

// ComputeNodeConfig is the [compute_node] TOML section. Field naming
// tracks pkg/state.ComputeNode 1:1; values flow into the upsert
// after the resource sizing checks (VPCPUs > 0, MemMB > 0, etc.).
//
// TargetURL is the dial target schedd/gatewayd use to reach this
// vmmd (the value written to compute_nodes.target_url on
// self-registration). It is intentionally separate from ListenAddr
// (the bind address) — a sensible bind like `tcp://0.0.0.0:50051`
// is NOT a routable dial target, and using the bind form as the
// dial target silently routes wakes to the local host's own vmmd
// instead of the second box. Operators on a multi-box fleet MUST
// set this to a routable FQDN or IP (e.g. `tcp://vmmd-2.faas:50051`)
// or set OverlayIP (auto-detected via tailscale) and leave
// TargetURL empty to let ResolveTargetURL derive it. The empty
// fallback (legacy default-local single-box path) is
// `unix://`+SocketPath.
type ComputeNodeConfig struct {
	NodeName           string `toml:"name"`                 // defaults to short hostname when empty
	TargetURL          string `toml:"target_url"`           // dial target written to compute_nodes.target_url
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

// ResolveTargetURL returns the dial target schedd/gatewayd use to
// reach this vmmd. Explicit TargetURL wins; otherwise falls back to
// `tcp://`+OverlayIP+`:50051` when OverlayIP is set; otherwise
// unix://+SocketPath for single-box default-local. The resolved
// string is wire.ParseTarget-compatible. Pair this with
// ResolveListenTarget — they answer different questions:
// ResolveListenTarget is "what do I bind to",
// ResolveTargetURL is "what do others dial to reach me".
func (c *Config) ResolveTargetURL() string {
	if url := strings.TrimSpace(c.ComputeNode.TargetURL); url != "" {
		return url
	}
	if ip := strings.TrimSpace(c.ComputeNode.OverlayIP); ip != "" {
		return "tcp://" + ip + ":50051"
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

// LoadServerTLSWithVerifier is the ADR-056 variant of LoadServerTLS
// that attaches a wire.NodeVerifier to the server's TLS config. A
// nil verifier is the single-box / pre-slice-3 case (no verifier
// installed; stdlib trust alone runs).
func (c *Config) LoadServerTLSWithVerifier(v wire.NodeVerifier) (*tls.Config, error) {
	return wire.LoadServerTLSConfigWithVerifier(c.TLSCertPath, c.TLSKeyPath, c.TLSCAPath, v)
}

// LoadScheddClientTLS returns the client mTLS config vmmd uses to
// dial schedd for the capacity publisher (ADR-052). Empty cluster
// returns (nil, nil); partial cluster is rejected.
func (c *Config) LoadScheddClientTLS() (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefix("schedd_client_", c.ScheddClientCertPath, c.ScheddClientKeyPath, c.ScheddClientCAPath)
}

// LoadScheddClientTLSWithVerifier is the ADR-056 variant of
// LoadScheddClientTLS. Same contract: nil verifier → no hook
// installed; prefix ("schedd_client_") names missing fields.
func (c *Config) LoadScheddClientTLSWithVerifier(v wire.NodeVerifier) (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefixAndVerifier("schedd_client_", c.ScheddClientCertPath, c.ScheddClientKeyPath, c.ScheddClientCAPath, v)
}

// LoadAdvisoryClientTLS returns the client mTLS config vmmd uses to
// dial apid's advisory listener (ADR-052). Empty cluster returns
// (nil, nil); partial cluster is rejected.
func (c *Config) LoadAdvisoryClientTLS() (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefix("advisory_client_", c.AdvisoryClientCertPath, c.AdvisoryClientKeyPath, c.AdvisoryClientCAPath)
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
		// Parent-mount registry defaults — see ADR-053 / field docs.
		// Cap=DefaultCap (16), MaxAge=ParentMountMaxAge (30m),
		// Sweep=ParentSweepInterval (30s). Operators dial these
		// for larger fleets; the constants live in pkg/vmmdmount
		// so any other consumer (e.g. a hypothetical test helper)
		// shares the same baseline.
		ParentMountCap:      vmmdmount.DefaultCap,
		ParentMountMaxAge:   vmmdmount.ParentMountMaxAge,
		ParentSweepInterval: 30 * time.Second,
		ComputeNode: ComputeNodeConfig{
			// Defaults match the synthetic default-local row seeded
			// by migration 00024 so single-box dev (no overlay)
			// still has a coherent self-registration on first boot.
			// Operators scaling beyond one box override every
			// [compute_node] field explicitly via vmmd.toml.
			// PR scale-out readiness #4: AdmissionCeilingMB routes
			// through api.DefaultComputeNodeCeilingMB so the
			// MemStore seed (pkg/state/memstore.go) and vmmd
			// share a single source of truth. Resolves to 47_600.
			VPCPUs:             160,
			MemMB:              56000,
			MaxConcurrency:     200,
			AdmissionCeilingMB: api.DefaultComputeNodeCeilingMB(),
		},
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Gate-B: even on the missing-file path, resolve Role
			// against FAAS_VMMD_ROLE so env wins over the empty
			// TOML default. role.FromConfig falls back to
			// RoleSingleBox when the env is unset.
			c.Role = role.FromConfig(string(c.Role), "FAAS_VMMD_ROLE")
			return c, nil
		}
		return nil, fmt.Errorf("vmmd: read %q: %w", path, err)
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("vmmd: parse %q: %w", path, err)
	}
	// Gate-B: resolve Role AFTER toml.Unmarshal so the post-decode
	// c.Role is consulted against FAAS_VMMD_ROLE. Setting Role in
	// the defaults-struct literal lets toml.Unmarshal overwrite it,
	// which would silently make the env override dead. The role
	// gate at boot calls role.Require to refuse to start under the
	// wrong box shape.
	c.Role = role.FromConfig(string(c.Role), "FAAS_VMMD_ROLE")
	return c, nil
}
