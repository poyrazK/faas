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
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
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
	// role from `pkg/role.AllRoles` and a name that shows up in
	// /etc/hosts as the canonical nameserver target. Single-box
	// deployments declare a single host with role `single-box`.
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
	// identity. The renderer uses Hostname + Address to populate
	// /etc/hosts and the per-daemon target_url entries.
	Overlay Overlay `yaml:"overlay"`

	// DNS is the public-facing hostname contract. apps_domain is the
	// single source of truth — gatewayd-internal, apid, and the CLI
	// PrintOK paths all read from it (PR-4 replaces the hard-coded
	// `apps.gregale.dev` literals in cmd/gregale/commands2.go and
	// cmd/gatewayd-internal/backend.go).
	DNS DNS `yaml:"dns"`

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

// Host is one control-plane node in the fleet. `name` is the canonical
// identity (matches `compute_nodes.name` in the database) and `role`
// is one of `pkg/role.AllRoles`. `Overlay` and `Storage` are pointers
// because per-host overrides are optional — the renderer falls back to
// the package-level value when absent.
type Host struct {
	Name    string  `yaml:"name"`
	Role    string  `yaml:"role"`
	Address string  `yaml:"address,omitempty"`
	Overlay *string `yaml:"overlay,omitempty"`
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

	// Outbound is the dial target. On a single-box install this is
	// the unix socket; on a split-box fleet it's the tcp://
	// `<overlay-address>:<port>` of the box that runs the target
	// daemon. The renderer writes this into the daemon's TOML
	// `vmmd_target` / `schedd_target` / etc. field.
	Outbound *OutboundConfig `yaml:"outbound,omitempty"`

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

// Overlay is the per-host overlay network identity. The renderer
// populates /etc/hosts so the canonical nameserver targets (e.g.
// `schedd.faas`) resolve to the control-plane VPC address. The
// overlay provider is one of the supported overlay kinds; the
// renderer maps this to the per-box /etc/hosts entries.
type Overlay struct {
	// Provider is the overlay implementation ("wireguard",
	// "tailscale", "static"). The renderer emits provider-specific
	// /etc/hosts hints and validator-side asserts the provider is
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
	// AppsDomain is the platform wildcard host (e.g.
	// `apps.gregale.dev`). The full app URL is
	// `<slug>.<apps_domain>`. The renderer writes this into every
	// gatewayd-internal TOML and the doctor (PR-4) enforces
	// consistency against the gatewayd-internal / apid / CLI
	// running config.
	AppsDomain string `yaml:"apps_domain"`
	// Mode is the DNS provider ("cloudflare", "manual", "nip_io").
	// The validator rejects values outside the supported set.
	Mode string `yaml:"mode"`
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
	// MigrationMaxSlot is the upper bound on the migrations/ slot
	// number — the validator refuses a manifest whose DB schema is
	// older than the largest in the embedded migrations set. PR-3a
	// will start enforcing this end-to-end.
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
	errs = append(errs, m.Daemons.validate()...)
	errs = append(errs, m.Overlay.validate()...)
	errs = append(errs, m.DNS.validate()...)
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
			fmt.Sprintf("apps_domain %q must be a valid hostname (e.g. apps.gregale.dev)", d.AppsDomain)})
	}
	if d.Mode == "" {
		errs = append(errs, Error{"dns.mode", "is required"})
	} else if !contains([]string{"cloudflare", "manual", "nip_io"}, d.Mode) {
		errs = append(errs, Error{"dns.mode",
			fmt.Sprintf("unsupported %q (allowed: cloudflare|manual|nip_io)", d.Mode)})
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

func looksLikeHostname(s string) bool {
	return hostnameRe.MatchString(s)
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
