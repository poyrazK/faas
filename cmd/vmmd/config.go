// Package main's config — parsed from /etc/faas/vmmd.toml (or the path
// passed via --config). Each field is independent of every other so a
// partial config file plus defaults produces a working daemon.

package main

import (
	"crypto/tls"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Config is the on-disk representation of the daemon's TOML config.
// File reads use pelletier/go-toml/v2 (already a transitive dep of
// many tools; pinning it here makes the daemon's config story
// explicit).
type Config struct {
	// PreparedNetworks bounds the optional cache of unused namespaces.
	// Zero disables it; FAAS_PREPARED_NETWORKS overrides TOML for canaries.
	PreparedNetworks int `toml:"prepared_networks"`
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
	// Metrics listener timeouts (ADR-122). Each knob falls back to
	// the corresponding api.Metrics*SecondsDefault when zero.
	// MaxHeaderBytes is int64 to mirror api.DefaultMaxHeaderBytes.
	MetricsReadTimeout    time.Duration `toml:"metrics_read_timeout"`
	MetricsWriteTimeout   time.Duration `toml:"metrics_write_timeout"`
	MetricsIdleTimeout    time.Duration `toml:"metrics_idle_timeout"`
	MetricsMaxHeaderBytes int64         `toml:"metrics_max_header_bytes"`

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

	// StaticEgressIPBundlePath (ADR-119 redesign) is the path to
	// the operator-supplied static egress IP TOML. vmmd reads it
	// at startup AND on SIGHUP; each entry is an (account_id,
	// app_id, ip) tuple the operator has pre-provisioned on the
	// host's AS (Hetzner additional IP, AWS EIP, etc.). The
	// bundle drives three things:
	//
	//   1. The bridge alias set on br-tenants (existing
	//      SetStaticEgressIPAliases path).
	//   2. The host renderer's StaticEgressRules list (the
	//      authoritative SNAT source — pkg/netns/policy.go).
	//   3. The Postgres gate table provisioned_static_egress_ips
	//      that the apid PUT path reads to validate the
	//      customer's pin belongs to an operator-provisioned IP.
	//
	// Empty disables the bundle (default — no static egress IPs
	// until the operator provisions them). vmmd warns at startup
	// if the path is set but the file does not exist.
	//
	// See cmd/vmmd/egress_static_ip_bundle.go for the loader +
	// SIGHUP watcher; /etc/faas/egress/static_egress_ips.toml
	// for the on-disk shape.
	StaticEgressIPBundlePath string `toml:"static_egress_ip_bundle"`

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
	// VCPUBudget is the per-node vCPU admission ceiling (migration 00123,
	// Tier A2). schedd's NodeLedger checks vCPU against this value rather
	// than the legacy fleet-wide api.VCPUSlots constant. Defaults to
	// api.VCPUSlots (160) when 0; operators on heterogeneous fleets dial
	// it per-host via [compute_node].vcpu_budget or FAAS_VCPU_BUDGET. The
	// SQL CHECK constraint (vcpu_budget > 0) means non-positive values
	// would also fail at upsert — fail fast at LoadConfig instead.
	VCPUBudget int    `toml:"vcpu_budget"`
	OverlayIP  string `toml:"overlay_ip"` // Tailscale/Wireguard IP; auto-detected when empty
	// HostBridgeCIDR is the per-host bridge CIDR (the /16 the veth
	// host-side addresses live in). Mega-PR-B Commit 1 supersedes
	// the former pkg/netns Go const; defaults to
	// api.DefaultHostBridgeCIDR() (10.100.0.0/16) when empty. The
	// bridge IP is the .1 of whatever CIDR the operator ships.
	// Single-host dev keeps the default; multi-host deployments
	// override per-host via env-overlay or TOML.
	HostBridgeCIDR string `toml:"host_bridge_cidr"`
	// OverlayCIDR is the per-host overlay subnet the vmmd overlay
	// detector prefers when multiple IPv4 candidates come back from
	// `tailscale ip -4` (Mega-PR-B Commit 3). Defaults to
	// api.DefaultOverlayCIDR() (Tailscale 100.64.0.0/10) when empty.
	// The same CIDR is rendered into the host forward chain's
	// overlay-accept rules (Commit 2) so mesh traffic survives the
	// §11 RFC1918 deny.
	OverlayCIDR string `toml:"overlay_cidr"`
	// OverlayInterface is the optional NIC pin used by
	// cmd/vmmd/overlay_detect.go::OverlayDetector. When set, the
	// detector shells out to `ip -4 -o addr show dev <iface>` and
	// returns that interface's IPv4 address, bypassing the
	// CIDR-preference scoring path. When the pinned NIC has no
	// IPv4 address the detector falls back to PreferCIDR scoring
	// (preserves the v1 contract). Empty keeps the existing
	// auto-detect behavior. Operators with multiple NICs (LAN +
	// tail/wg) on a single host use this to disambiguate.
	OverlayInterface string `toml:"overlay_interface"`
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

// LoadServerTLSWithPrefixAndVerifierAndReload is the ADR-052 §5
// / PR-E variant: per-handshake verifier + SIGHUP-driven cert
// rotation. nil v and nil reload are tolerated and degrade to
// LoadServerTLS (no hook, no callback) — same back-compat shape
// as LoadServerTLSWithVerifier. The reload closure is consulted
// by stdlib's per-handshake GetConfigForClient callback.
func (c *Config) LoadServerTLSWithPrefixAndVerifierAndReload(v wire.NodeVerifier, reload wire.ReloadFunc) (*tls.Config, error) {
	return wire.LoadServerTLSConfigWithPrefixAndVerifierAndReload("", c.TLSCertPath, c.TLSKeyPath, c.TLSCAPath, v, reload)
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

// LoadScheddClientTLSWithPrefixAndVerifierAndReload is the
// ADR-052 §5 / PR-E variant. Same nil-tolerance contract as the
// server variant: nil v / nil reload degrade to the no-hook /
// no-callback shape (LoadScheddClientTLS). The reload closure
// re-issues the client leaf on every handshake via stdlib's
// GetClientCertificate; trust root is fixed at construction per
// ADR-052 §Risks "CA rotation pain".
func (c *Config) LoadScheddClientTLSWithPrefixAndVerifierAndReload(v wire.NodeVerifier, reload wire.ReloadFunc) (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefixAndVerifierAndReload("schedd_client_", c.ScheddClientCertPath, c.ScheddClientKeyPath, c.ScheddClientCAPath, v, reload)
}

// LoadAdvisoryClientTLS returns the client mTLS config vmmd uses to
// dial apid's advisory listener (ADR-052). Empty cluster returns
// (nil, nil); partial cluster is rejected.
func (c *Config) LoadAdvisoryClientTLS() (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefix("advisory_client_", c.AdvisoryClientCertPath, c.AdvisoryClientKeyPath, c.AdvisoryClientCAPath)
}

// MetricsListener returns the *http.Server timeouts + MaxHeaderBytes
// for vmmd's metrics listener (ADR-122). Each knob falls back to the
// corresponding api.Metrics*SecondsDefault when the TOML field is
// zero. Same shape as cmd/{meterd,schedd}/config.go::MetricsListener
// so a future daemon can lift the helper verbatim.
func (c *Config) MetricsListener() (read, write, idle time.Duration, maxHeaderBytes int64) {
	read = c.MetricsReadTimeout
	if read == 0 {
		read = time.Duration(api.MetricsReadTimeoutSecondsDefault) * time.Second
	}
	write = c.MetricsWriteTimeout
	if write == 0 {
		write = time.Duration(api.MetricsWriteTimeoutSecondsDefault) * time.Second
	}
	idle = c.MetricsIdleTimeout
	if idle == 0 {
		idle = time.Duration(api.MetricsIdleTimeoutSecondsDefault) * time.Second
	}
	maxHeaderBytes = c.MetricsMaxHeaderBytes
	if maxHeaderBytes == 0 {
		maxHeaderBytes = api.DefaultMaxHeaderBytes
	}
	return
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
			// Issue #938 / PR-A: VCPUBudget defaults to api.VCPUSlots
			// (160) so the upsert satisfies the migration 00123 CHECK
			// constraint (vcpu_budget > 0) without operator action on
			// single-box dev. Heterogeneous fleets override per-host
			// via [compute_node].vcpu_budget or FAAS_VCPU_BUDGET.
			VPCPUs:             160,
			MemMB:              56000,
			MaxConcurrency:     200,
			AdmissionCeilingMB: api.DefaultComputeNodeCeilingMB(),
			VCPUBudget:         api.VCPUSlots,
		},
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("vmmd: read %q: %w", path, err)
		}
		// Missing-file path: fall through with c as the defaults-only
		// config. The env-overlay block below applies FAAS_NODE_NAME,
		// FAAS_HOST_BRIDGE_CIDR, FAAS_OVERLAY_INTERFACE,
		// FAAS_VCPU_BUDGET, and FAAS_VMMD_ROLE just as it does on the
		// parsed-TOML path; the role-against-env call happens here too.
		// (Historically this branch early-returned before the env-
		// overlays ran — issue #938 / PR-A exposes that as a bug
		// because FAAS_VCPU_BUDGET=0 silently bypassed the
		// non-positive validator.)
	} else if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("vmmd: parse %q: %w", path, err)
	}
	// Gate-B: resolve Role AFTER toml.Unmarshal so the post-decode
	// c.Role is consulted against FAAS_VMMD_ROLE. Setting Role in
	// the defaults-struct literal lets toml.Unmarshal overwrite it,
	// which would silently make the env override dead. The role
	// gate at boot calls role.Require to refuse to start under the
	// wrong box shape.
	c.Role = role.FromConfig(string(c.Role), "FAAS_VMMD_ROLE")
	// Mega-PR-A (issue #911 / ADR-110 PR-1): env-var overlay for
	// [compute_node].name so the systemd drop-in (deploy/ansible/
	// roles/vmmd_service/files/faas-vmmd.service.d/
	// 99-faas-node-name.conf) can override the TOML value on every
	// box. The vmmd ComputeNode self-registration (cmd/vmmd/register.go)
	// writes this name into compute_nodes.name at startup, so the
	// env overlay is the load-bearing identity for the multi-box
	// handshake. Empty keeps the TOML value (single-box dev back-
	// compat — short hostname).
	if v := os.Getenv("FAAS_NODE_NAME"); v != "" {
		c.ComputeNode.NodeName = v
	}
	// Public multi-box deployment overlay: keep the bind target and the
	// routable dial target in deployment-owned systemd drop-ins rather than
	// asking an operator to patch compute_nodes.target_url by hand. The two
	// values intentionally remain separate: vmmd may bind to 0.0.0.0 while
	// schedd/gatewayd must dial a routable address for this host.
	if v := os.Getenv("FAAS_VMMD_LISTEN_ADDR"); v != "" {
		c.ListenAddr = v
	}
	if v := os.Getenv("FAAS_VMMD_TARGET_URL"); v != "" {
		c.ComputeNode.TargetURL = v
	}
	// Mega-PR-B (issue #911 / ADR-110 Tier-1 BLOCKING Commit 1):
	// env-var overlay for [compute_node].host_bridge_cidr so the
	// per-host bridge CIDR is configurable without a TOML edit
	// (mirrors the FAAS_NODE_NAME pattern above). The default
	// single-host bridge CIDR lives in pkg/api.DefaultHostBridgeCIDR().
	// Empty keeps the TOML value (or the api default when TOML is
	// also empty).
	if v := os.Getenv("FAAS_HOST_BRIDGE_CIDR"); v != "" {
		c.ComputeNode.HostBridgeCIDR = v
	}
	// PR scale-out tier-1 residual (issue #911 / ADR-110 / Gap #5):
	// env-var overlay for [compute_node].overlay_interface so
	// operators with multiple NICs can pin the overlay detector
	// without editing vmmd.toml on every box. Mirrors the
	// FAAS_HOST_BRIDGE_CIDR pattern above. Empty keeps the TOML
	// value (or the auto-detect default when TOML is also empty).
	if v := os.Getenv("FAAS_OVERLAY_INTERFACE"); v != "" {
		c.ComputeNode.OverlayInterface = v
	}
	// Issue #938 / PR-A: env-var overlay for [compute_node].vcpu_budget
	// so heterogeneous fleets can dial the per-host vCPU ceiling via the
	// systemd drop-in without editing vmmd.toml on every box. Mirrors
	// the FAAS_NODE_NAME / FAAS_HOST_BRIDGE_CIDR / FAAS_OVERLAY_INTERFACE
	// pattern. Non-positive values are rejected at LoadConfig so the
	// migration 00123 CHECK constraint (vcpu_budget > 0) can't trip the
	// self-registration upsert later. Empty keeps the TOML value (or
	// api.VCPUSlots when both are empty).
	if v := os.Getenv("FAAS_VCPU_BUDGET"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n <= 0 {
			return nil, fmt.Errorf("vmmd: FAAS_VCPU_BUDGET %q must be a positive integer", v)
		}
		c.ComputeNode.VCPUBudget = n
	}
	if v := os.Getenv("FAAS_PREPARED_NETWORKS"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 || n > api.MaxPreparedNetworkCacheSize {
			return nil, fmt.Errorf("vmmd: FAAS_PREPARED_NETWORKS must be between 0 and %d", api.MaxPreparedNetworkCacheSize)
		}
		c.PreparedNetworks = n
	}
	// Issue #938 / PR-A: reject non-positive TOML values for
	// [compute_node].vcpu_budget at LoadConfig rather than letting them
	// reach the upsert (where migration 00123's CHECK constraint would
	// trip the self-registration fail-closed path). The env-overlay
	// block above already enforces the same rule for FAAS_VCPU_BUDGET.
	// Zero is rejected here even though the struct-default path silently
	// defaults to api.VCPUSlots — a TOML `vcpu_budget = 0` is an
	// explicit operator mistake, not an omitted field.
	//
	// Asymmetry with cmd/vmmd/register.go (review finding #4 on PR #940):
	// LoadConfig rejects <= 0 because that's a valid Go zero value in
	// the TOML-decoded struct; registerComputeNode accepts 0 and falls
	// back to api.VCPUSlots because the test seam
	// (register_test.go:TestRegisterComputeNode_DefaultsVCPUBudgetFromAPI)
	// calls the function directly with the struct-default zero value
	// to pin the omitted-field fallback. The two layers are
	// therefore deliberately inconsistent: LoadConfig = "explicit
	// zero is an operator error", register = "zero is the omitted-
	// field-default sentinel". Unifying them is fine, but pin the
	// test that exercises the fallback before doing so.
	if c.ComputeNode.VCPUBudget <= 0 {
		return nil, fmt.Errorf("vmmd: [compute_node].vcpu_budget must be > 0 (got %d)", c.ComputeNode.VCPUBudget)
	}
	// Issue #900: reject a wildcard [compute_node].target_url at
	// LoadConfig. The bind form `tcp://0.0.0.0:50051` (or `tcp://[::]:50051`)
	// is a valid listen target but NOT a routable dial target — schedd/
	// gatewayd on the second box would dial 0.0.0.0:50051 and resolve to
	// the local host, silently routing wakes to the wrong daemon. The
	// fallback chain in ResolveTargetURL is correct for unset
	// target_url; this gate fires only on the explicit-override slot
	// where the operator copy-pasted the bind form. The error lands
	// where the operator is editing (vmmd.toml) and exits before FC
	// detect, host-key load, or DB connect have spent the operator's
	// time. FQDNs are accepted as-is (the verifier and the dialer
	// resolve them); only an Unspecified IP host is rejected.
	if raw := strings.TrimSpace(c.ComputeNode.TargetURL); raw != "" {
		if wildcard, err := targetURLIsWildcardDial(raw); err != nil {
			return nil, fmt.Errorf("vmmd: [compute_node].target_url %q invalid: %w", raw, err)
		} else if wildcard {
			return nil, fmt.Errorf(
				"vmmd: [compute_node].target_url %q is a bind form (0.0.0.0 / ::) — "+
					"set a routable FQDN or IP for multi-box routing, or use [compute_node].overlay_ip to auto-derive. "+
					"Docs: docs/runbooks/multi-host-rollout.md §3.5",
				raw)
		}
	}
	// Mega-PR-B review M3: validate [compute_node].overlay_cidr
	// against the §11 deny set at config-load time, BEFORE vmmd
	// accepts any traffic or registers with schedd. The render-time
	// panic in pkg/netns.HostPolicy.Render stays as belt-and-braces
	// for programmatic misuse (a future caller could mutate the
	// HostPolicy struct post-load), but operators editing vmmd.toml
	// get a clear startup error naming both CIDRs instead of a
	// crash-loop on the first pg_notify EgressPolicyChanged reload.
	// The validator is the same one the renderer uses — single
	// source of truth for the subset check, no drift between the
	// load-time and render-time gates.
	if overlay := strings.TrimSpace(c.ComputeNode.OverlayCIDR); overlay != "" {
		if err := netns.ValidateOverlayCIDRs([]string{overlay}, netns.NewDefaultDenySet()); err != nil {
			return nil, fmt.Errorf("vmmd: [compute_node].overlay_cidr %q invalid: %w", overlay, err)
		}
	}
	// PR scale-out tier-1 residual (Gap #3): validate the bridge
	// CIDR against the slot-allocator contract — must be a /16 (the
	// only prefix length that leaves room for `fcvm.Allocator` to
	// hand out per-VM /30 leases without spilling into the next
	// host's range). Reject at config-load time with a clear startup
	// error naming the offending CIDR.
	if bridge := strings.TrimSpace(c.ComputeNode.HostBridgeCIDR); bridge != "" {
		if err := validateHostBridgeCIDR(bridge); err != nil {
			return nil, fmt.Errorf("vmmd: [compute_node].host_bridge_cidr %q invalid: %w", bridge, err)
		}
	}
	return c, nil
}

// validateHostBridgeCIDR is the Gap #3 wiring gate for the bridge
// CIDR override. Returns an error when:
//   - the CIDR is unparseable
//   - the prefix length is not /16 (the only size that maps cleanly
//     to per-VM /30 leases; smaller leaves gaps, larger would overflow
//     the slot space)
//   - the input is not in network form (e.g. "10.42.0.5/16" rather
//     than "10.42.0.0/16"). The slot allocator re-masks the input
//     before assigning .2 / .3 / .4 /etc., so a non-network host
//     would silently re-anchor to the masked form — rejecting at
//     load time surfaces the operator's intent.
//
// Empty input is treated as "use the default" — the caller is
// expected to skip the call when the TOML/env value is empty so the
// default branch in cmd/vmmd/main.go::runWithDeps can apply
// api.DefaultHostBridgeCIDR() after the bridge-CIDR byte identity is
// confirmed against the slot allocator.
//
// NOTE: the §11 deny catalog applies to TENANT egress, not host
// infrastructure. The host bridge IP is a /30 gateway from the
// physical NIC into the per-VM netns; the deny rules live on the
// per-netns forward chain and the host forward chain AFTER the
// bridge, so the bridge IP itself is never subject to the deny
// catalog. Operators are expected to use RFC1918 ranges for the
// per-host bridge (the canonical default is 10.100.0.0/16) —
// rejecting those would defeat the entire Gap #3 use case.
func validateHostBridgeCIDR(cidr string) error {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if prefix.Bits() != 16 {
		return fmt.Errorf("prefix length must be /16 (got /%d); per-VM /30 leases only fit a /16", prefix.Bits())
	}
	if prefix.Masked() != prefix {
		return fmt.Errorf("CIDR %q is not in network form (the host bits must be zero); use %q", cidr, prefix.Masked().String())
	}
	return nil
}

// targetURLIsWildcardDial is the issue #900 load-time gate for
// [compute_node].target_url. It returns true when raw is a tcp://
// URL whose host is an Unspecified IPv4 (0.0.0.0) or IPv6 (::) address
// — the bind form copy-pasted into the dial-target slot. The error
// return is for parse failures the caller should surface (the load
// path wraps it with the offending field name).
//
// Non-tcp schemes (unix://, dns://) never have an IP host:
//   - unix:// — no host at all.
//   - dns:// — authority is a hostname; the dialer resolves it.
//
// FQDNs on tcp:// are accepted as-is. The verifier and the
// dialer both resolve names; assuming routability matches the
// v1 wire contract (ADR-025 / pkg/wire/grpc.go).
//
// wire.ParseTarget strips the IPv6 brackets in the parsed Address
// (e.g. Address=":::50051" for tcp://[::]:50051 — see
// pkg/wire/grpc.go:103). net.SplitHostPort does NOT understand
// that unbracketed IPv6 form, so we split on the LAST colon to
// peel off the port. This matches the v1 wire contract: the
// authority is always host:port, and the port is the rightmost
// `:`. The only multi-colon host form is IPv6 literal, which
// netip.ParseAddr will accept either way.
//
// The helper is package-private; the only caller is LoadConfig
// above.
func targetURLIsWildcardDial(raw string) (bool, error) {
	t, err := wire.ParseTarget(raw)
	if err != nil {
		return false, fmt.Errorf("parse: %w", err)
	}
	if t.Scheme != wire.SchemeTCP {
		return false, nil
	}
	host := t.Address
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return ipLiteralIsWildcard(host), nil
}

// ipLiteralIsWildcard classifies an address string as a wildcard
// (bind-form) address. Returns false for any value that is not an
// IP literal (FQDNs, named hosts); those cannot be a bind form and
// are the verifier/dialer's job to resolve.
//
// Factored out of targetURLIsWildcardDial so the
// netip.ParseAddr error path lives in a helper whose `(bool, error)`
// return is never bound — the wildcard caller does not propagate
// the parse error (a non-IP host isn't an error; see comment on
// targetURLIsWildcardDial).
func ipLiteralIsWildcard(host string) bool {
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return !ip.IsValid() || ip.IsUnspecified()
}
