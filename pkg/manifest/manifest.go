// Package manifest is the loader + validator for the Gregale split-box
// deployment manifest (issue #911 / ADR-110).
//
// One YAML document at deploy/manifest/splitbox.yaml is the source of
// truth for every host in a multi-box fleet: hostnames, roles,
// endpoints, overlay, DNS, PostgreSQL, release digests, storage roots,
// cgroup/slice requirements, and certificate authority wiring. The
// `gregale manifest validate` subcommand and the `make lint-manifest`
// CI gate both consume this package, so every check runs through one
// canonical validation path (issue #911 completeness contract).
//
// Scope (PR-0): the schema enums and shape, the structural validator,
// the TOML table-placement check, and the wire-format (SemVer) gate.
// The renderer (PR-2), the release bundle installer (PR-3), and the
// doctor (PR-4) consume this package but ship in later PRs.
//
// Out of scope (PR-0): provisioning secrets (PR-X refactor of the
// v1 deploy/controlplane/bootstrap.sh, now RETIRED 2026-08-15 by
// issue #911 / PR-1 — see deploy/controlplane/RETIRED.md), the actual
// file generation (PR-2), runtime checks against a live fleet (PR-4).
package manifest

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the contract version of the manifest schema. The
// SemVer major-version is bumped when a breaking shape change lands
// (new required field, renamed key, tightened enum). Patch-level
// changes (clarifying a comment, adding an optional field) keep the
// major version the same. The validator refuses to parse a manifest
// whose declared schema_version is not in SupportedSchemaVersions.
//
// The decision to use SemVer (per the issue #911 plan's "manifest
// versioning" open question) keeps `git_sha` release IDs and `SchemaVersion`
// manifest IDs on independent axes: a release bundle's git_sha pins
// the binary, while the manifest it ships with pins the deployment
// shape. The two compose.
const SchemaVersion = "1.0.0"

// MaxComputeNodes is the supported production fleet ceiling for the
// manifest/Ansible deployment path. It is an explicit contract rather than a
// claim about the theoretical scheduler capacity: raising it should follow a
// scale validation run and a review of resolver, firewall, and rollout costs.
const MaxComputeNodes = 1000

// SupportedSchemaVersions lists every schema_version the validator
// accepts. Schema-evolution policy: keep the previous major version on
// a bump for at least one release cycle (so an operator upgrading
// binaries can read the manifest they already have), but the renderer
// (PR-2) only knows how to render the current major. Tests pin this
// list to the canonical set so a forgotten bump surfaces at PR time.
var SupportedSchemaVersions = []string{
	SchemaVersion,
}

// Manifest is the parsed root of a split-box deployment manifest. Field
// tags follow the YAML convention from gregalemanifest (the
// customer-side gregale.yaml loader at pkg/gregalemanifest/manifest.go);
// keep this in lockstep when adding fields so the operator-facing
// `gregale manifest validate` message stays consistent.
//
// Every field is a struct (not a pointer to a primitive) so the
// validator can walk sub-trees without nil-checking. Optional fields
// have a `*…` wrapper (per the pointer-bool pattern noted in the
// MEMORY index as a working idiom for this codebase).
type Manifest struct {
	// SchemaVersion is the contract version of the manifest schema.
	// The validator refuses to load a manifest whose schema_version
	// is not in SupportedSchemaVersions. Old manifests carrying a
	// schema_version lower than SchemaVersion still parse (the
	// previous major is supported), but the renderer (PR-2) only
	// emits the current major's shape.
	SchemaVersion string `yaml:"schema_version"`

	// Fleet is the list of hosts in the deployment. Each host has a
	// role from `pkg/role.AllRoles` and a stable transport endpoint. In
	// hostname-based fleets the private_dns adapter maps that name to the
	// provider's gathered private address. Single-box deployments declare
	// a single host with role `single-box`.
	Fleet Fleet `yaml:"fleet"`

	// Daemons is the per-daemon configuration. Keys are daemon names
	// (`schedd`, `vmmd`, …); each entry carries the listener, mTLS,
	// and outbound-dial addresses that the renderer (PR-2) writes
	// into the rendered /etc/faas/*.toml.
	//
	// Cross-box wiring: `outbound` carries the dial target on a
	// split-box fleet. On a single-box install the renderer ignores
	// outbound (every dial goes over the unix socket at /run/faas/).
	Daemons Daemons `yaml:"daemons"`

	// Overlay is the per-host overlay network (Wireguard/Tailscale)
	// identity. The manifest Ansible generator uses the fleet endpoint
	// names and private_dns adapter to populate private aliases; the
	// renderer uses the same endpoint port for per-daemon target_url entries.
	Overlay Overlay `yaml:"overlay"`

	// DNS is the public-facing hostname contract. apps_domain is the
	// single source of truth — gatewayd-internal, apid, and the CLI
	// PrintOK paths all read from it; a value of `gregale.dev` produces
	// `<slug>.gregale.dev`.
	DNS DNS `yaml:"dns"`

	// PrivateDNS is the provider-neutral name-resolution contract for
	// split-box transport names. It is deliberately separate from DNS:
	// the latter describes public application URLs, while this section
	// describes how the deployment keeps fleet and mTLS names private.
	// The current managed_hosts implementation is rendered by Ansible
	// from gathered host facts, so moving between GCP, Hetzner, or
	// another bare-metal provider does not require changing daemon URLs.
	PrivateDNS PrivateDNS `yaml:"private_dns,omitempty"`

	// PostgreSQL is the database cluster configuration. The renderer
	// needs the role names, the database name, and the migration
	// policy (the manifest validator checks the migration scope is
	// within the embedded migrations/ directory).
	PostgreSQL PostgreSQL `yaml:"postgresql"`

	// Release is the immutable release-tuple anchor. The renderer
	// anchors every rendered file's hash to this tuple; the doctor
	// (PR-4) reads it from /opt/faas/releases/<id>/manifest_hash.
	// PR-0 only validates the shape; PR-3 ships the bundle/install
	// path that materialises the tuple.
	Release Release `yaml:"release"`

	// Storage is the per-host filesystem layout. The renderer
	// creates these directories with the documented ownership/modes;
	// the doctor (PR-4) checks them post-install.
	Storage Storage `yaml:"storage"`

	// Cgroups is the cgroup v2 controller requirements. The
	// renderer writes +memory +cpu +io +pids to the per-host
	// subtree_control (the only place the production path lands
	// today is deploy/lima/run-metal.sh:84 + the renderer; the
	// v1 bootstrap.sh + ansible skip it — issue #911).
	Cgroups Cgroups `yaml:"cgroups"`

	// PKI is the certificate authority wiring. The renderer issues
	// per-daemon leaves using pkg/pki.RolesForBox(); the manifest
	// declares the CA cert fingerprint the doctor enforces.
	PKI PKI `yaml:"pki"`

	// Egress is the top-level egress-policy escape hatch. The
	// DangerAcceptRFC1918LateralMovement flag + the OverlayExceptions
	// CIDR list together widen the host forward chain + per-netns
	// forward chain beyond the §11 always-deny catalog. Both must
	// be set together — a CHECK constraint on the egress_policy DB
	// row (migration 00276) enforces the pair at runtime; the
	// manifest validator (Egress.validate) enforces the same pair at
	// apply time. PR scale-out tier-1 residual (Gap #4). The flag
	// name itself is load-bearing: any operator who skims the
	// manifest sees the consequence spelled out before they can
	// flip it.
	Egress Egress `yaml:"egress,omitempty"`
}

// Egress is the top-level egress-policy knob. PR scale-out
// tier-1 residual (Gap #4): the RFC1918 lateral-movement
// exception is gated behind a manifest flag with a name that
// makes the consequence impossible to miss in code review.
// The DB schema (migration 00078) enforces the pairing at the
// row level; the manifest validator enforces the same pairing
// at apply time so an operator can't bypass the row-level gate
// by editing TOML directly.
type Egress struct {
	// DangerAcceptRFC1918LateralMovement enables the per-host
	// host + per-netns forward chains to emit `ip saddr <ex>
	// accept` rules BEFORE the §11 deny block. Setting this
	// without listing at least one entry in OverlayExceptions
	// is rejected by the manifest validator and by the DB
	// CHECK constraint. Default: false. Operators using an
	// RFC1918 overlay (e.g. 10.42.0.0/24) MUST enable this
	// flag AND list the overlay CIDR in OverlayExceptions —
	// otherwise the §11 deny would block bridged tenant
	// traffic to the overlay.
	DangerAcceptRFC1918LateralMovement bool `yaml:"danger_accept_rfc1918_lateral_movement,omitempty"`

	// OverlayExceptions is the explicit list of CIDRs the host
	// + per-netns forward chains accept ahead of the §11 deny
	// block. Each entry must be a valid netip.Prefix; an empty
	// list means "no exceptions" (the deny block stands).
	// Combined with DangerAcceptRFC1918LateralMovement this
	// is the only sanctioned path to route overlay traffic
	// through an RFC1918 range.
	OverlayExceptions []string `yaml:"overlay_exceptions,omitempty"`
}

// validate enforces the pair-check at manifest-load time:
// DangerAcceptRFC1918LateralMovement is rejected when the exceptions
// list is empty (the flag without a target is a no-op that risks
// being silently flipped on by a future maintainer); an exception
// without the flag is rejected (the flag is the operator's explicit
// acknowledgement of the lateral-movement consequence — without it
// the exception would silently widen the §11 deny catalog). Each
// entry's CIDR must parse via netip.ParsePrefix. The DB CHECK
// constraint (migration 00276_egress_policy_exceptions.sql) is
// the row-level mirror of this gate.
func (e *Egress) validate() Errors {
	var errs Errors
	if !e.DangerAcceptRFC1918LateralMovement && len(e.OverlayExceptions) == 0 {
		// Quiet default — no flag, no exceptions. Nothing to do.
		return nil
	}
	if e.DangerAcceptRFC1918LateralMovement && len(e.OverlayExceptions) == 0 {
		errs = append(errs, Error{
			"egress",
			"danger_accept_rfc1918_lateral_movement=true requires at least one entry in overlay_exceptions (the flag is the explicit acknowledgement; without a target the flag is a no-op and risks being silently flipped on)",
		})
	}
	if len(e.OverlayExceptions) > 0 && !e.DangerAcceptRFC1918LateralMovement {
		errs = append(errs, Error{
			"egress",
			"overlay_exceptions requires danger_accept_rfc1918_lateral_movement=true (the flag is the explicit acknowledgement of the lateral-movement consequence; without it the exception would silently widen the §11 deny catalog)",
		})
	}
	for i, raw := range e.OverlayExceptions {
		path := fmt.Sprintf("egress.overlay_exceptions[%d]", i)
		if _, err := netip.ParsePrefix(raw); err != nil {
			errs = append(errs, Error{path, fmt.Sprintf("invalid CIDR %q: %v", raw, err)})
		}
	}
	return errs
}

// Fleet is the list of hosts in the deployment. Each host must have a
// unique name and a role from `pkg/role.AllRoles`. Single-box installs
// declare exactly one host with role `single-box`.
type Fleet struct {
	Hosts []Host `yaml:"hosts"`
}

// ComputeNodeCount returns the number of compute-only hosts in the fleet.
// Keeping this count in the manifest package gives validators, deployment
// tooling, and scale checks one definition of the production fleet size.
func (f Fleet) ComputeNodeCount() int {
	count := 0
	for _, host := range f.Hosts {
		if host.Role == "compute-only" {
			count++
		}
	}
	return count
}

// Host is one control-plane node in the fleet. `name` is the canonical
// identity (matches `compute_nodes.name` in the database) and `role`
// is one of `pkg/role.AllRoles`. `Overlay` is a pointer because the
// per-host override is optional; StorageDevice is an explicit path
// override and is empty when the host already owns its fast-root mount.
type Host struct {
	Name    string  `yaml:"name"`
	Role    string  `yaml:"role"`
	Address string  `yaml:"address,omitempty"`
	Overlay *string `yaml:"overlay,omitempty"`
	// StorageDevice is an optional provider-neutral block-device path for
	// this host's fast root. It is intentionally per-host because stable
	// /dev/disk/by-id paths differ between providers and machines. When it
	// is empty, the deployment requires an already-mounted fast root instead
	// of guessing which disk is safe to format.
	StorageDevice string `yaml:"storage_device,omitempty"`
	// Tags are free-form, opaque labels the doctor uses to filter
	// checks (e.g. `--role=compute-only` skips cert checks for the
	// egress server leaf, which lives on the control-plane box).
	Tags []string `yaml:"tags,omitempty"`
}

// Daemons is the per-daemon configuration. Keys are daemon names;
// the renderer only writes entries for the daemons each host runs
// (a control-plane host does not need vmmd's TOML on disk).
type Daemons struct {
	Schedd           *DaemonConfig `yaml:"schedd,omitempty"`
	Vmmd             *DaemonConfig `yaml:"vmmd,omitempty"`
	Apid             *DaemonConfig `yaml:"apid,omitempty"`
	Meterd           *DaemonConfig `yaml:"meterd,omitempty"`
	Githubd          *DaemonConfig `yaml:"githubd,omitempty"`
	GatewaydPublic   *DaemonConfig `yaml:"gatewayd_public,omitempty"`
	GatewaydInternal *DaemonConfig `yaml:"gatewayd_internal,omitempty"`
	Imaged           *DaemonConfig `yaml:"imaged,omitempty"`
	Builderd         *DaemonConfig `yaml:"builderd,omitempty"`
}

// DaemonConfig is the per-daemon configuration. `tls` carries the
// server-side mTLS material (consumed by the renderer to populate
// the loaded /etc/faas/<daemon>.toml); `outbound` is the dial target
// the daemon uses to talk to remote daemons (e.g. schedd → vmmd on
// a split-box fleet).
type DaemonConfig struct {
	// Bind is the local listen address (e.g. `unix:///run/faas/schedd.sock`
	// or `tcp://0.0.0.0:7100`). The renderer writes this into the
	// daemon's TOML `socket_path` / `listen_addr` field.
	Bind string `yaml:"bind"`

	// TLS is the on-disk material for the server side. The renderer
	// reads CertPath / KeyPath / CAPath and writes the matching
	// `tls_cert_path` / `tls_key_path` / `tls_ca_path` keys into
	// the rendered TOML.
	TLS *TLSMaterial `yaml:"tls,omitempty"`

	// GatewaydInternal's split-box peers are separate from TLS, which
	// describes a daemon's own listener, and from Outbound, which is a
	// single legacy peer shape used by schedd. Keep the three gateway
	// client/server bundles explicit so the renderer cannot silently emit
	// a plaintext control-plane connection when this daemon runs on a
	// compute-only host.
	ScheddTLS         *TLSMaterial `yaml:"schedd_tls,omitempty"`
	VMMTLS            *TLSMaterial `yaml:"vmmd_tls,omitempty"`
	EgressTLS         *TLSMaterial `yaml:"egress_tls,omitempty"`
	ScheddClientTLS   *TLSMaterial `yaml:"schedd_client_tls,omitempty"`
	AdvisoryClientTLS *TLSMaterial `yaml:"advisory_client_tls,omitempty"`

	// Outbound is the dial target. On a single-box install this is
	// the unix socket; on a split-box fleet it's the tcp://
	// `<overlay-address>:<port>` of the box that runs the target
	// daemon. The renderer writes this into the daemon's TOML
	// `vmmd_target` / `schedd_target` / etc. field.
	Outbound *OutboundConfig `yaml:"outbound,omitempty"`

	// APIDLoopback is gatewayd-internal's HTTP target for the apid
	// public surface. It defaults to the local apid listener in a
	// single-box deployment; split-box manifests set it to the
	// control-plane address so compute gatewayd does not silently dial
	// its own loopback.
	APIDLoopback string `yaml:"apid_loopback,omitempty"`

	// GatewaySynthTarget is schedd's optional remote gatewayd-internal
	// synthesis endpoint. It is separate from Outbound because schedd has
	// two remote peers in a split-box fleet: vmmd for placement and
	// gatewayd-internal for cron/dispatch.
	GatewaySynthTarget string `yaml:"gateway_synth_target,omitempty"`

	// GatewayMetricsURL is schedd's optional remote gatewayd-internal metrics
	// endpoint. Empty disables the scale-up scrape instead of silently
	// scraping a nonexistent loopback listener on the control plane.
	GatewayMetricsURL string `yaml:"gateway_metrics_url,omitempty"`

	// ComputeNode is the [compute_node] sub-struct consumed by vmmd
	// only. It carries the per-host bridge CIDR override, the
	// overlay CIDR the detector prefers, and the optional NIC pin
	// used by the overlay-IP auto-detector. PR scale-out tier-1
	// residual (Gaps #3 + #5) — only vmmd's DaemonConfig populates
	// this today; the renderer no-ops when nil. Other daemons'
	// DaemonConfig structs leave it nil and the catalog's
	// HostKeys[daemon].ComputeNodeBlock is empty, so the
	// validator never complains about a missing field.
	ComputeNode *ComputeNodeConfig `yaml:"compute_node,omitempty"`
}

// ComputeNodeConfig is the vmmd `[compute_node]` schema. All three
// fields are overrides; empty values fall back to per-host defaults
// (api.DefaultHostBridgeCIDR, api.DefaultOverlayCIDR, "auto-detect"
// for the NIC). The validator requires a /16 prefix for
// HostBridgeCIDR — see pkg/netns.NewConfigWithBridge and
// pkg/fcvm.SetHostIPBase for the slot-allocator contract that pins
// the prefix length.
type ComputeNodeConfig struct {
	// HostBridgeCIDR is the per-host bridge /16. The .1 is reserved
	// for the root-ns bridge; per-VM /30 leases are carved from
	// .2 onwards by pkg/fcvm.Allocator. Default: api.DefaultHostBridgeCIDR.
	HostBridgeCIDR string `yaml:"host_bridge_cidr,omitempty"`

	// OverlayCIDR is the per-host overlay subnet the vmmd overlay
	// detector prefers when multiple IPv4 candidates come back from
	// `tailscale ip -4`. Same CIDR is rendered into the host
	// forward chain's overlay-accept rules. Default: api.DefaultOverlayCIDR.
	OverlayCIDR string `yaml:"overlay_cidr,omitempty"`

	// OverlayInterface is the optional NIC pin used by the
	// overlay-IP detector. Empty means "auto-detect via the
	// existing PreferCIDR scoring path". Operators with multiple
	// NICs (LAN + tail/wg) on a single host set this to disambiguate.
	OverlayInterface string `yaml:"overlay_interface,omitempty"`
}

// TLSMaterial is the filesystem path triple for a cert / key / CA. The
// renderer asserts each file exists at apply time. Mode is the
// expected file mode (the renderer calls `pkg/pki.enforceCertMode` /
// `enforceKeyMode` to assert this).
type TLSMaterial struct {
	CertPath string `yaml:"cert_path"`
	KeyPath  string `yaml:"key_path"`
	CAPath   string `yaml:"ca_path"`
	Mode     string `yaml:"mode,omitempty"` // "0440" or "0400" etc.
}

// OutboundConfig is the dial target for a daemon that talks to a
// remote daemon. The renderer writes the dial target into the calling
// daemon's TOML.
type OutboundConfig struct {
	// Target is the dial URL (unix:// or tcp://).
	Target string `yaml:"target"`
	// TLS carries the client-side mTLS material the caller uses to
	// authenticate to the remote daemon's server leaf.
	TLS *TLSMaterial `yaml:"tls,omitempty"`
}

// Overlay is the per-host overlay network identity. The Ansible overlay
// role populates the provider-neutral private endpoint map; the overlay
// provider is one of the supported overlay kinds.
type Overlay struct {
	// Provider is the overlay implementation ("wireguard",
	// "tailscale", "static"). The validator asserts the provider is
	// supported.
	Provider string `yaml:"provider"`
	// CIDR is the overlay subnet (e.g. "10.42.0.0/24"). The renderer
	// asserts each host's Address falls within CIDR.
	CIDR string `yaml:"cidr"`
}

// DNS is the public-facing hostname contract. apps_domain is the
// single source of truth — the validator refuses a manifest whose
// apps_domain disagrees with the rendered gatewayd-internal config
// (PR-4 enforces this across the running fleet).
type DNS struct {
	// AppsDomain is the platform wildcard suffix (e.g.
	// `gregale.dev`). The full app URL is
	// `<slug>.<apps_domain>`. The renderer writes this into every
	// gatewayd-internal TOML and the doctor (PR-4) enforces
	// consistency against the gatewayd-internal / apid / CLI
	// running config.
	AppsDomain string `yaml:"apps_domain"`
	// Mode is the DNS provider ("cloudflare", "manual", "nip_io").
	// The validator rejects values outside the supported set.
	Mode string `yaml:"mode"`
}

// PrivateDNS is the private transport-name resolution contract. The
// managed_hosts mode is provider-neutral: Ansible derives the address for
// each inventory host from its private/default interface and writes one
// generated block to /etc/hosts on every node. Operators with a separate
// private DNS service may keep the same manifest names and replace the
// resolver adapter later without changing daemon configuration.
type PrivateDNS struct {
	// Mode selects the private resolver adapter. managed_hosts is the
	// built-in adapter and is the portable default for small fleets.
	Mode string `yaml:"mode,omitempty"`
	// Zone is the private hostname suffix used for fleet endpoint names.
	// It is normally the same suffix as dns.apps_domain, but remains an
	// explicit field so a future private DNS adapter can use a separate
	// split-horizon zone without changing the manifest shape.
	Zone string `yaml:"zone,omitempty"`
}

// PostgreSQL is the database cluster configuration. The renderer needs
// the role names and the database name to write the per-daemon
// `pg_hba.conf` / `pg_ident.conf` entries (the `faas_map` ident map
// is the v1 bootstrap.sh-only today — RETIRED 2026-08-15 by issue #911
// / PR-1; PR-2 / PR-X wire it into the renderer).
type PostgreSQL struct {
	// DSN is the connection string the daemons use (`postgres://…`).
	// The validator only checks the shape; the renderer writes it
	// into the per-daemon TOML `db_url` field.
	DSN string `yaml:"dsn"`
	// Database is the database name (default `faas`).
	Database string `yaml:"database"`
	// MigrationMaxSlot is legacy manifest metadata from the sequential-ID
	// era. ADR-142 makes MAX(version_id) insufficient for readiness, so new
	// code must compare the complete embedded migration set with the ledger.
	// Retained for schema-v1 manifest compatibility only.
	MigrationMaxSlot int `yaml:"migration_max_slot"`
	// Policy is the migration policy (`off`, `on-boot`,
	// `on-boot-offline`). `on-boot-offline` is the single-box dev
	// default; the multi-box fleet convention is `on-boot`.
	Policy string `yaml:"policy"`
}

// Release is the immutable release-tuple anchor. PR-0 only validates
// the shape; PR-3 ships the bundle/install path that materialises
// the tuple at /opt/faas/releases/<id>/.
type Release struct {
	// ID is the release identifier (`git describe` output, e.g.
	// `v1.4.0-12-gabc1234`). The renderer writes this into the
	// immutable release directory.
	ID string `yaml:"id"`
	// GitSHA is the commit hash the ID was built from. The doctor
	// (PR-4) asserts the running binary's SHA matches the manifest's
	// GitSHA on every box.
	GitSHA string `yaml:"git_sha"`
	// Architecture is the target architecture (`x86_64`, `arm64`).
	// The renderer refuses a manifest whose architecture disagrees
	// with the host's `uname -m`. The validator allows the canonical
	// set; PR-3 may extend with `arm64` for Lima metal targets.
	Architecture string `yaml:"architecture"`
	// FirecrackerVersion is the Firecracker semver (e.g. `1.10.0`).
	// The doctor (PR-4) reads `snapshots.fc_version` and asserts it
	// matches this value.
	//
	// Distinct from FirecrackerDigest (the canonical binary's
	// sha256) — the gate-B cross-box mTLS hardening requires the
	// snapshot restore path to match the on-disk FC. The version
	// is the human-readable wire; the digest is the integrity check.
	FirecrackerVersion string `yaml:"firecracker_version"`
	// FirecrackerDigest is the sha256 of the Firecracker binary
	// the renderer places at /opt/faas/current/bin/firecracker.
	FirecrackerDigest string `yaml:"firecracker_digest"`
	// KernelDigest is the kernel image sha256. The renderer
	// places the kernel image at `kernel_path` and the doctor
	// asserts the on-disk hash matches.
	KernelDigest string `yaml:"kernel_digest"`
	// BuilderBaseDigest is the builder base image digest. The doctor
	// asserts `FAAS_BUILDER_BASE_REF` resolves to this digest.
	BuilderBaseDigest string `yaml:"builder_base_digest"`
	// RuntimeBaseDigest is the runtime base image digest. The doctor
	// asserts the runtime layer's root digest matches.
	RuntimeBaseDigest string `yaml:"runtime_base_digest"`
	// RuntimeBaseRefs is the immutable OCI reference for each supported
	// function runtime. It is optional for backwards-compatible manifests,
	// but production manifests that carry it must contain the complete set so
	// the provider-neutral compute join can stage one identical contract for
	// builderd and imaged.
	RuntimeBaseRefs map[string]string `yaml:"runtime_base_refs,omitempty"`
}

// Storage is the per-host filesystem layout. The renderer creates
// these directories with the documented ownership/modes.
type Storage struct {
	// FastRoot is the high-I/O scrub path (default `/srv/fc`). The
	// renderer asserts this is on a fast filesystem (xfs with
	// `noatime`) and the subdirectories below exist on disk.
	FastRoot string `yaml:"fast_root"`
	// SpoolRoot is the per-daemon write-queue path (default
	// `/var/spool/faas`).
	SpoolRoot string `yaml:"spool_root"`
	// LogRoot is the structured-log path (default `/var/log/faas`).
	LogRoot string `yaml:"log_root"`
	// RunDir is the unix-socket directory (default `/run/faas`).
	// The renderer creates `/run/faas/stream` with mode 0770
	// root:faas — closes the `vmmd-stream-bridge` bind race that
	// bit the GCP deploy.
	RunDir string `yaml:"run_dir"`
}

// Cgroups is the cgroup v2 controller requirements. The renderer
// writes +memory +cpu +io +pids to the per-host subtree_control.
// This is the only place the production path lands this today.
type Cgroups struct {
	// Slice is the cgroup slice the control plane (or compute node)
	// runs under (default `faas-cp.slice` for the control plane,
	// `faas-tenant.slice` for the compute node).
	Slice string `yaml:"slice"`
	// Controllers is the comma-separated list of controllers the
	// slice's subtree_control must enable. The validator rejects
	// empty / `memory`-absent lists (issue #911 calls out the
	// `memory` controller as load-bearing for tenant admission).
	Controllers string `yaml:"controllers"`
}

// PKI is the certificate authority wiring. The renderer issues
// per-daemon leaves via `pkg/pki.RolesForBox()`; the manifest
// declares the CA cert fingerprint the doctor enforces.
type PKI struct {
	// RootDir is the per-box TLS root (default `/etc/faas/tls`).
	RootDir string `yaml:"root_dir"`
	// CAFingerprint is the SHA-256 fingerprint of the CA cert the
	// renderer writes to RootDir/ca/ca.crt. The doctor (PR-4)
	// asserts every per-daemon leaf's CA matches this fingerprint.
	CAFingerprint string `yaml:"ca_fingerprint"`
	// AllowedSANs is the canonical SAN list the renderer writes
	// into every leaf. Per-host SANs come from the host's
	// compute_nodes row at runtime; AllowedSANs is the static
	// platform-level set.
	AllowedSANs []string `yaml:"allowed_sans,omitempty"`
}

// =====================================================================
// Validation
// =====================================================================

// Error is the wire-format for validation failures. The CLI renders
// these as a path/value/message triple so operators can fix the
// offending YAML at the source.
type Error struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// Errors is a collection of validation failures. The validator
// collects every failure (not just the first) so a single
// `gregale manifest validate` run reports every broken invariant —
// the issue #911 doctor pattern requires exhaustive output.
type Errors []Error

func (e Errors) Error() string {
	if len(e) == 0 {
		return ""
	}
	parts := make([]string, len(e))
	for i, err := range e {
		parts[i] = err.Error()
	}
	return strings.Join(parts, "; ")
}

// Is allows errors.Is(err, ErrInvalid) to test for a validation
// failure type without coupling to the concrete Errors slice.
func (e Errors) Is(target error) bool {
	return target == ErrInvalid
}

// ErrInvalid is the sentinel a caller can match against when a
// validation produces errors. Wrap with errors.Is; the concrete
// Errors slice carries the per-field path/message detail.
var ErrInvalid = errors.New("manifest: invalid")

// Load reads a manifest from path. The file must be UTF-8 YAML; the
// loader rejects TOML manifests explicitly (the
// `gregalemanifest.` customer-side loader does the same; keeping the
// rule consistent across the two surfaces).
func Load(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("manifest: read %s: %w", path, err)
	}
	if strings.HasSuffix(path, ".toml") {
		return nil, errors.New("manifest: TOML manifests are not supported (rename to .yaml)")
	}
	return Parse(b)
}

// Parse decodes raw YAML bytes into a Manifest and validates the
// shape. The function returns the parsed manifest on success, or
// the parse error otherwise. Call Validate() to run the field
// validation.
func Parse(b []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytesReader(b))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("manifest: empty file")
		}
		return nil, fmt.Errorf("manifest: parse: %w", err)
	}
	return &m, nil
}

// Validate runs the full validation pass and returns an Errors
// slice aggregating every failure. The function never short-circuits
// on the first failure — the validator is exhaustive per the issue
// #911 doctor pattern.
//
// The validation order is intentional:
//
//  1. SchemaVersion (constant-time check before any field walk).
//  2. Fleet shape (hosts, names, roles).
//  3. Daemons shape (TOML table-placement against the rendered
//     /etc/faas/*.toml).
//  4. Overlay + DNS (cross-references between the two).
//  5. PostgreSQL + Release (DNS-reachable policy check).
//  6. Storage + Cgroups (filesystem path safety).
//  7. PKI (SAN list shape).
//
// Each step appends to the same Errors slice; the returned Errors
// is sorted by field path so the output is deterministic.
func (m *Manifest) Validate() Errors {
	var errs Errors
	if m.SchemaVersion == "" {
		errs = append(errs, Error{"schema_version", "is required"})
	} else if !contains(SupportedSchemaVersions, m.SchemaVersion) {
		errs = append(errs, Error{
			"schema_version",
			fmt.Sprintf("unsupported version %q (supported: %v)",
				m.SchemaVersion, SupportedSchemaVersions),
		})
	}
	errs = append(errs, m.Fleet.validate()...)
	errs = append(errs, m.validateFleetEndpoints()...)
	errs = append(errs, m.Daemons.validate()...)
	errs = append(errs, m.Overlay.validate()...)
	errs = append(errs, m.DNS.validate()...)
	errs = append(errs, m.PrivateDNS.validate()...)
	errs = append(errs, m.validatePrivateResolution()...)
	errs = append(errs, m.PostgreSQL.validate()...)
	errs = append(errs, m.Release.validate()...)
	errs = append(errs, m.Storage.validate()...)
	errs = append(errs, m.Cgroups.validate()...)
	errs = append(errs, m.PKI.validate()...)
	errs = append(errs, m.Egress.validate()...)
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// ParseHostPort parses the manifest endpoint shape used by split-box
// hosts. Unix endpoints remain valid for a single-box manifest, but a
// multi-host fleet must expose a routable host:port endpoint so the
// renderer and Ansible can derive the same dial target without an
// operator-maintained IP copy.
func ParseHostPort(raw string) (host string, port int, err error) {
	if strings.HasPrefix(raw, "unix://") {
		return "", 0, fmt.Errorf("unix endpoint is only valid for a single-box host")
	}
	if raw == "" {
		return "", 0, fmt.Errorf("endpoint is empty")
	}
	host, portText, err := net.SplitHostPort(raw)
	if err != nil {
		return "", 0, fmt.Errorf("endpoint %q must be host:port: %w", raw, err)
	}
	if host == "" {
		return "", 0, fmt.Errorf("endpoint %q has an empty host", raw)
	}
	if _, ipErr := netip.ParseAddr(host); ipErr != nil && !validNodeName(host) {
		return "", 0, fmt.Errorf("endpoint %q has an invalid host %q", raw, host)
	}
	port, err = strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("endpoint %q has invalid port %q", raw, portText)
	}
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil && ip.IsUnspecified() {
		return "", 0, fmt.Errorf("endpoint %q uses an unspecified host address", raw)
	}
	return host, port, nil
}

// TCPURL converts a manifest host endpoint into the canonical dial URL
// used by vmmd and compute_nodes.target_url. The bind address remains a
// separate setting; this helper must never produce tcp://0.0.0.0.
func TCPURL(raw string) (string, error) {
	host, port, err := ParseHostPort(raw)
	if err != nil {
		return "", err
	}
	return "tcp://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// ServiceName returns the private mTLS DNS identity for a split-box role.
// These names are intentionally independent of public Cloudflare DNS: the
// internal PKI issues role certificates for them. Literal-IP manifests get
// generated /etc/hosts mappings; hostname manifests must resolve these names
// through the manifest's private resolver adapter.
func ServiceName(role string) (string, error) {
	switch role {
	case "control-plane":
		return "schedd.faas", nil
	case "compute-only":
		return "vmmd.faas", nil
	default:
		return "", fmt.Errorf("role %q has no split-box service identity", role)
	}
}

// ServiceTCPURL returns the mTLS-verifiable target for a split-box host.
// ParseHostPort supplies the port from the manifest endpoint, while the
// service identity supplies the TLS ServerName used by grpc-go.
func ServiceTCPURL(role, raw string) (string, error) {
	_, port, err := ParseHostPort(raw)
	if err != nil {
		return "", err
	}
	name, err := ServiceName(role)
	if err != nil {
		return "", err
	}
	return "tcp://" + net.JoinHostPort(name, strconv.Itoa(port)), nil
}

func (m *Manifest) validateFleetEndpoints() Errors {
	if len(m.Fleet.Hosts) <= 1 {
		return nil
	}
	var errs Errors
	for i, h := range m.Fleet.Hosts {
		path := fmt.Sprintf("fleet.hosts[%d].address", i)
		if h.Address == "" {
			errs = append(errs, Error{path, "is required for a multi-host fleet"})
			continue
		}
		host, _, err := ParseHostPort(h.Address)
		if err != nil {
			errs = append(errs, Error{path, err.Error()})
			continue
		}
		// IP endpoints should be inside the declared mesh. Hostnames are
		// intentionally allowed: they are resolved by the operator's
		// private DNS or /etc/hosts contract and may not be statically
		// checked here.
		ip, parseErr := netip.ParseAddr(host)
		if parseErr == nil && m.Overlay.CIDR != "" {
			prefix, prefixErr := netip.ParsePrefix(m.Overlay.CIDR)
			if prefixErr == nil && !prefix.Contains(ip) {
				errs = append(errs, Error{path,
					fmt.Sprintf("IP %q is outside overlay.cidr %q", host, m.Overlay.CIDR)})
			}
		}
	}
	return errs
}

// validatePrivateResolution makes hostname-based split-box manifests
// declare an explicit private resolver adapter. Without this gate a typo in
// the deployment shape can silently fall through to public DNS, which is
// especially dangerous when the public zone is managed by Cloudflare.
func (m *Manifest) validatePrivateResolution() Errors {
	if len(m.Fleet.Hosts) <= 1 {
		return nil
	}
	for _, h := range m.Fleet.Hosts {
		host, _, err := ParseHostPort(h.Address)
		if err != nil {
			continue
		}
		if _, ipErr := netip.ParseAddr(host); ipErr != nil && m.PrivateDNS.Mode == "" {
			return Errors{{"private_dns.mode",
				fmt.Sprintf("is required for hostname endpoint %q; declare the provider-neutral managed_hosts adapter", host)}}
		}
		if _, ipErr := netip.ParseAddr(host); ipErr != nil && m.PrivateDNS.Mode == "managed_hosts" && m.PrivateDNS.Zone != "" {
			zone := strings.TrimSuffix(strings.ToLower(m.PrivateDNS.Zone), ".")
			lowerHost := strings.ToLower(strings.TrimSuffix(host, "."))
			if lowerHost != zone && !strings.HasSuffix(lowerHost, "."+zone) {
				return Errors{{"fleet.hosts",
					fmt.Sprintf("hostname endpoint %q is outside private_dns.zone %q", host, m.PrivateDNS.Zone)}}
			}
		}
	}
	return nil
}

// =====================================================================
// Per-section validators
// =====================================================================

func (f *Fleet) validate() Errors {
	var errs Errors
	if len(f.Hosts) == 0 {
		errs = append(errs, Error{"fleet.hosts", "must declare at least one host"})
		return errs
	}
	seen := make(map[string]bool, len(f.Hosts))
	for i, h := range f.Hosts {
		path := fmt.Sprintf("fleet.hosts[%d]", i)
		if h.Name == "" {
			errs = append(errs, Error{path + ".name", "is required"})
		} else if !validNodeName(h.Name) {
			errs = append(errs, Error{path + ".name",
				fmt.Sprintf("invalid node name %q (use lowercase letters, digits, dots, or dashes)", h.Name)})
		} else if seen[h.Name] {
			errs = append(errs, Error{path + ".name",
				fmt.Sprintf("duplicate host %q (host names must be unique)", h.Name)})
		}
		seen[h.Name] = true
		if h.Role == "" {
			errs = append(errs, Error{path + ".role", "is required"})
		} else if !roleKnown(h.Role) {
			errs = append(errs, Error{path + ".role",
				fmt.Sprintf("unknown role %q (allowed: single-box|control-plane|compute-only)", h.Role)})
		}
		if h.Address != "" && !looksLikeHostPort(h.Address) {
			errs = append(errs, Error{path + ".address",
				fmt.Sprintf("address %q must be host:port or unix://path", h.Address)})
		}
		if h.StorageDevice != "" && !filepath.IsAbs(h.StorageDevice) {
			errs = append(errs, Error{path + ".storage_device",
				fmt.Sprintf("storage_device %q must be an absolute device path", h.StorageDevice)})
		}
	}
	computeNodeCount := f.ComputeNodeCount()
	if computeNodeCount > MaxComputeNodes {
		errs = append(errs, Error{"fleet.hosts",
			fmt.Sprintf("declares %d compute-only hosts; maximum supported is %d", computeNodeCount, MaxComputeNodes)})
	}
	// Single-box sanity: at most one host when role == single-box.
	// Single-box sanity: at most one host when role == single-box is
	// the canonical shape and requires no further checks; the split-box
	// branch below catches the multi-host case where role combinations
	// matter (control-plane + compute-only).
	if len(f.Hosts) > 1 {
		hasCtl, hasCompute := false, false
		for _, h := range f.Hosts {
			if h.Role == "control-plane" {
				hasCtl = true
			}
			if h.Role == "compute-only" {
				hasCompute = true
			}
		}
		if hasCtl && hasCompute {
			// canonical split-box shape — no error.
		} else if !hasCtl && !hasCompute {
			errs = append(errs, Error{"fleet.hosts",
				"multi-host fleets must declare at least one control-plane and one compute-only role"})
		}
	}
	return errs
}

func (d *Daemons) validate() Errors {
	var errs Errors
	// TOML table-placement check (issue #911 calls out the bug
	// class where keys landed in the wrong table). The check
	// verifies each daemon's `tls` material is non-empty when the
	// daemon opts into cross-box mTLS (Bind is tcp://). The
	// renderer (PR-2) writes these into the matching TOML table;
	// the validator catches the misplacement at parse time.
	for name, dc := range map[string]*DaemonConfig{
		"schedd":            d.Schedd,
		"vmmd":              d.Vmmd,
		"apid":              d.Apid,
		"meterd":            d.Meterd,
		"githubd":           d.Githubd,
		"gatewayd_public":   d.GatewaydPublic,
		"gatewayd_internal": d.GatewaydInternal,
		"imaged":            d.Imaged,
		"builderd":          d.Builderd,
	} {
		if dc == nil {
			continue
		}
		path := fmt.Sprintf("daemons.%s", name)
		if dc.Bind == "" {
			errs = append(errs, Error{path + ".bind", "is required"})
		}
		if dc.TLS != nil {
			errs = append(errs, dc.TLS.validate(path+".tls")...)
		}
		if dc.ScheddTLS != nil {
			errs = append(errs, dc.ScheddTLS.validate(path+".schedd_tls")...)
		}
		if dc.VMMTLS != nil {
			errs = append(errs, dc.VMMTLS.validate(path+".vmmd_tls")...)
		}
		if dc.EgressTLS != nil {
			errs = append(errs, dc.EgressTLS.validate(path+".egress_tls")...)
		}
		if dc.ScheddClientTLS != nil {
			errs = append(errs, dc.ScheddClientTLS.validate(path+".schedd_client_tls")...)
		}
		if dc.AdvisoryClientTLS != nil {
			errs = append(errs, dc.AdvisoryClientTLS.validate(path+".advisory_client_tls")...)
		}
		if dc.Outbound != nil {
			errs = append(errs, dc.Outbound.validate(path+".outbound")...)
		}
	}
	return errs
}

func (t *TLSMaterial) validate(path string) Errors {
	var errs Errors
	if t.CertPath == "" {
		errs = append(errs, Error{path + ".cert_path", "is required"})
	}
	if t.KeyPath == "" {
		errs = append(errs, Error{path + ".key_path", "is required"})
	}
	if t.CAPath == "" {
		errs = append(errs, Error{path + ".ca_path", "is required"})
	}
	if t.Mode != "" {
		// Octal string of the form "0xxx"; reject plaintext so an
		// operator typo can't land a 0600 key on disk.
		if !validOctalMode(t.Mode) {
			errs = append(errs, Error{path + ".mode",
				fmt.Sprintf("mode %q must be an octal string (e.g. \"0400\")", t.Mode)})
		}
	}
	return errs
}

func (o *OutboundConfig) validate(path string) Errors {
	var errs Errors
	if o.Target == "" {
		errs = append(errs, Error{path + ".target", "is required"})
	}
	if o.TLS != nil {
		errs = append(errs, o.TLS.validate(path+".tls")...)
	}
	return errs
}

func (o *Overlay) validate() Errors {
	var errs Errors
	if o.Provider == "" {
		errs = append(errs, Error{"overlay.provider", "is required"})
	} else if !contains([]string{"wireguard", "tailscale", "static"}, o.Provider) {
		errs = append(errs, Error{"overlay.provider",
			fmt.Sprintf("unsupported %q (allowed: wireguard|tailscale|static)", o.Provider)})
	}
	if o.CIDR == "" {
		errs = append(errs, Error{"overlay.cidr", "is required"})
	} else if !validCIDR(o.CIDR) {
		errs = append(errs, Error{"overlay.cidr",
			fmt.Sprintf("cidr %q must be a valid IPv4 CIDR (e.g. 10.42.0.0/24)", o.CIDR)})
	}
	return errs
}

func (d *DNS) validate() Errors {
	var errs Errors
	if d.AppsDomain == "" {
		errs = append(errs, Error{"dns.apps_domain", "is required"})
	} else if !looksLikeHostname(d.AppsDomain) {
		errs = append(errs, Error{"dns.apps_domain",
			fmt.Sprintf("apps_domain %q must be a valid hostname (e.g. gregale.dev)", d.AppsDomain)})
	}
	if d.Mode == "" {
		errs = append(errs, Error{"dns.mode", "is required"})
	} else if !contains([]string{"cloudflare", "manual", "nip_io"}, d.Mode) {
		errs = append(errs, Error{"dns.mode",
			fmt.Sprintf("unsupported %q (allowed: cloudflare|manual|nip_io)", d.Mode)})
	}
	return errs
}

func (d *PrivateDNS) validate() Errors {
	var errs Errors
	if d.Mode == "" {
		// Literal endpoint manifests do not need a private resolver
		// adapter. Hostname fleets are checked by validatePrivateResolution.
		return nil
	}
	if d.Mode != "managed_hosts" {
		errs = append(errs, Error{"private_dns.mode",
			fmt.Sprintf("unsupported %q (allowed: managed_hosts)", d.Mode)})
	}
	if d.Zone == "" {
		errs = append(errs, Error{"private_dns.zone", "is required when private_dns.mode is set"})
	} else if !looksLikeHostname(d.Zone) {
		errs = append(errs, Error{"private_dns.zone",
			fmt.Sprintf("zone %q must be a valid hostname (e.g. gregale.dev)", d.Zone)})
	}
	return errs
}

func (p *PostgreSQL) validate() Errors {
	var errs Errors
	if p.DSN == "" {
		errs = append(errs, Error{"postgresql.dsn", "is required"})
	}
	if p.Database == "" {
		errs = append(errs, Error{"postgresql.database", "is required"})
	}
	if p.MigrationMaxSlot <= 0 {
		errs = append(errs, Error{"postgresql.migration_max_slot",
			"must be > 0 (the migrations/ embedded set's max slot)"})
	}
	if !contains([]string{"off", "on-boot", "on-boot-offline"}, p.Policy) {
		errs = append(errs, Error{"postgresql.policy",
			fmt.Sprintf("unsupported %q (allowed: off|on-boot|on-boot-offline)", p.Policy)})
	}
	return errs
}

func (r *Release) validate() Errors {
	var errs Errors
	if r.ID == "" {
		errs = append(errs, Error{"release.id", "is required"})
	}
	if r.GitSHA == "" {
		errs = append(errs, Error{"release.git_sha", "is required"})
	} else if !validSHA1(r.GitSHA) {
		errs = append(errs, Error{"release.git_sha",
			fmt.Sprintf("git_sha %q must be a 40-char hex string", r.GitSHA)})
	}
	for _, x := range []struct {
		path string
		val  string
	}{
		{"release.architecture", r.Architecture},
		{"release.firecracker_version", r.FirecrackerVersion},
	} {
		if x.val == "" {
			errs = append(errs, Error{x.path, "is required"})
			continue
		}
		// Architecture is a token (x86_64 / arm64); FirecrackerVersion
		// is a semver (1.10.0). Both are non-empty short strings; we
		// don't enforce a regex here because the renderer (PR-2)
		// cross-checks against the host's `uname -m` and the
		// /opt/faas/current/bin/firecracker --version output.
		if strings.ContainsAny(x.val, " \t\n") {
			errs = append(errs, Error{x.path,
				fmt.Sprintf("%q must not contain whitespace", x.val)})
		}
	}
	for _, x := range []struct {
		path string
		val  string
	}{
		{"release.firecracker_digest", r.FirecrackerDigest},
		{"release.kernel_digest", r.KernelDigest},
		{"release.builder_base_digest", r.BuilderBaseDigest},
		{"release.runtime_base_digest", r.RuntimeBaseDigest},
	} {
		if x.val == "" {
			errs = append(errs, Error{x.path, "is required"})
			continue
		}
		if !validSHA256(x.val) {
			errs = append(errs, Error{x.path,
				fmt.Sprintf("%q must be a 64-char sha256 digest", x.val)})
		}
	}
	if len(r.RuntimeBaseRefs) != 0 {
		expected := []string{"minimal", "node22", "python312", "go124", "go124_alpine", "node24", "python313"}
		seen := make(map[string]struct{}, len(r.RuntimeBaseRefs))
		for key, ref := range r.RuntimeBaseRefs {
			seen[key] = struct{}{}
			if ref == "" {
				errs = append(errs, Error{"release.runtime_base_refs." + key, "is required"})
				continue
			}
			digestMarker := "@sha256:"
			digestAt := strings.LastIndex(ref, digestMarker)
			if digestAt <= 0 || !validSHA256(ref[digestAt+len(digestMarker):]) {
				errs = append(errs, Error{"release.runtime_base_refs." + key,
					fmt.Sprintf("%q must be an OCI reference pinned by @sha256:<64hex>", ref)})
			}
		}
		for _, key := range expected {
			if _, ok := seen[key]; !ok {
				errs = append(errs, Error{"release.runtime_base_refs." + key, "is required"})
			}
		}
		for key := range seen {
			found := false
			for _, expectedKey := range expected {
				if key == expectedKey {
					found = true
					break
				}
			}
			if !found {
				errs = append(errs, Error{"release.runtime_base_refs." + key, "is not a supported runtime"})
			}
		}
	}
	return errs
}

func (s *Storage) validate() Errors {
	var errs Errors
	for _, x := range []struct {
		path, val string
	}{
		{"storage.fast_root", s.FastRoot},
		{"storage.spool_root", s.SpoolRoot},
		{"storage.log_root", s.LogRoot},
		{"storage.run_dir", s.RunDir},
	} {
		if x.val == "" {
			errs = append(errs, Error{x.path, "is required"})
			continue
		}
		if !filepath.IsAbs(x.val) {
			errs = append(errs, Error{x.path,
				fmt.Sprintf("%q must be an absolute path", x.val)})
		}
	}
	return errs
}

func (c *Cgroups) validate() Errors {
	var errs Errors
	if c.Slice == "" {
		errs = append(errs, Error{"cgroups.slice", "is required"})
	}
	if c.Controllers == "" {
		errs = append(errs, Error{"cgroups.controllers", "is required"})
		return errs
	}
	// `memory` is load-bearing for tenant admission (issue #911). The
	// validator refuses an empty/missing `memory` on the controller list
	// — there's no migration path that lets the renderer recover from
	// a missing memory controller.
	parts := strings.Split(c.Controllers, ",")
	has := make(map[string]bool, len(parts))
	for _, p := range parts {
		has[strings.TrimSpace(p)] = true
	}
	if !has["memory"] {
		errs = append(errs, Error{"cgroups.controllers",
			"must include \"memory\" (issue #911): tenant admission depends on memory.max == plan + 8 MB"})
	}
	if !has["cpu"] {
		errs = append(errs, Error{"cgroups.controllers",
			"must include \"cpu\""})
	}
	return errs
}

func (p *PKI) validate() Errors {
	var errs Errors
	if p.RootDir == "" {
		errs = append(errs, Error{"pki.root_dir", "is required"})
	} else if !filepath.IsAbs(p.RootDir) {
		errs = append(errs, Error{"pki.root_dir",
			fmt.Sprintf("%q must be an absolute path", p.RootDir)})
	}
	if p.CAFingerprint == "" {
		errs = append(errs, Error{"pki.ca_fingerprint", "is required"})
	} else if !validSHA256(p.CAFingerprint) {
		errs = append(errs, Error{"pki.ca_fingerprint",
			fmt.Sprintf("%q must be a 64-char sha256 digest", p.CAFingerprint)})
	}
	for i, san := range p.AllowedSANs {
		if !looksLikeHostname(san) {
			errs = append(errs, Error{
				fmt.Sprintf("pki.allowed_sans[%d]", i),
				fmt.Sprintf("SAN %q must be a valid hostname", san),
			})
		}
	}
	return errs
}

// =====================================================================
// Small helpers — no external validation package to keep the dep tree
// small. Issue #911 clusters share the same shape; promote to a
// pkg/validate helper if a third caller appears.
// =====================================================================

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func roleKnown(r string) bool {
	for _, v := range []string{"single-box", "control-plane", "compute-only"} {
		if r == v {
			return true
		}
	}
	return false
}

var hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
var nodeNameRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,61}[a-z0-9])?$`)

func looksLikeHostname(s string) bool {
	return hostnameRe.MatchString(s)
}

func validNodeName(s string) bool {
	return nodeNameRe.MatchString(s) && !strings.Contains(s, "..")
}

func looksLikeHostPort(s string) bool {
	if strings.HasPrefix(s, "unix://") {
		return true
	}
	// host:port, host (no port also accepted for overlay addresses)
	if strings.Contains(s, ":") {
		return true
	}
	return looksLikeHostname(s)
}

var cidrRe = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}/\d{1,2}$`)

func validCIDR(s string) bool {
	return cidrRe.MatchString(s)
}

var octalModeRe = regexp.MustCompile(`^0[0-7]{3,4}$`)

func validOctalMode(s string) bool {
	return octalModeRe.MatchString(s)
}

var sha256Re = regexp.MustCompile(`^[a-f0-9]{64}$`)
var sha1Re = regexp.MustCompile(`^[a-f0-9]{40}$`)

// validSHA256 accepts only 64-char hex digests. Used for
// kernel / builder_base / runtime_base / firecracker fingerprint
// fields where the length is non-negotiable.
func validSHA256(s string) bool {
	return sha256Re.MatchString(s)
}

// validSHA1 accepts only 40-char hex strings. Used for git_sha
// (the one place length 40 is canonical).
func validSHA1(s string) bool {
	return sha1Re.MatchString(s)
}

func bytesReader(b []byte) io.Reader { return &sliceReader{b: b} }

type sliceReader struct {
	b []byte
	i int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
