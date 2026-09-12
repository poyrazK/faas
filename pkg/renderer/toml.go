package renderer

import (
	"bytes"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/manifest"
)

// renderTOML produces the per-daemon TOML body for daemon. The flatMap
// is the merged "table.leaf" key/value map the validator consumes
// (ValidateTOMLPlacement in pkg/manifest/toml_check.go is the
// cross-table placement gate). Returns the rendered TOML bytes, the
// flatMap (for caller-side diagnostics), and an error if the
// validator flags a tombstone or mis-placed key.
//
// The shape is:
//
//	# Top-level (private) keys.
//	socket_path = "/run/faas/schedd.sock"
//	metrics_addr = "127.0.0.1:9091"
//	db_url = "postgres://..."
//	apps_domain = "gregale.dev"
//
//	# [compute_node] table-block (vmmd only today).
//	[compute_node]
//	name = "vmmd-1"
//	target_url = "unix:///run/faas/vmmd.sock"
//	overlay_ip = "10.42.0.5"
//	...
//
// The renderer writes the values pulled from manifest.Daemons.<d>
// (Bind, TLS, Outbound). It does NOT hard-code the daemon's TOML
// schema — pkg/manifest.HostKeys is the source of truth, and the
// renderer copies the keys into the flatMap. New keys land in the
// catalog first, then in the renderer-emitting code via this loop.
//
// tomlRenderCtx bundles the per-daemon inputs renderTOML consumes.
// AppsDomain comes from the manifest's DNS block (global per host);
// the per-daemon Bind / TLS / Outbound come from the DaemonConfig.
// HostSANs is the per-host SAN list (vmmd's [compute_node] uses it).
// Both AppsDomain and hostSANs are optional — empty values render
// fine (writeTOMLKV omits empty values; the daemon's loader uses
// its built-in default).
type tomlRenderCtx struct {
	Daemon string
	DC     *manifest.DaemonConfig
	// DBURL is the fleet PostgreSQL endpoint from manifest.postgresql.dsn.
	// Control-plane TOMLs use the local socket override below, while
	// compute-only TOMLs leave this key empty and receive the shared endpoint
	// through the root-owned environment file.
	DBURL       string
	AppsDomain  string
	HostSANs    []string
	HostName    string
	HostAddress string
	// HostRole controls whether a rendered TOML may carry the shared
	// PostgreSQL DSN. Compute-only boxes receive the DSN through their
	// root-owned systemd EnvironmentFile; embedding it in every daemon
	// TOML would duplicate a credential into world-readable config.
	HostRole string
}

// controlPlanePostgresSocketDSN is intentionally credential-free. The
// control plane hosts PostgreSQL and uses peer authentication over the local
// socket; only compute-only boxes receive the password-authenticated TCP DSN
// through their root-owned compute-db.env.
const controlPlanePostgresSocketDSN = "postgres:///faas?host=/run/postgresql&user=faas"

// The flatMap is what ValidateTOMLPlacement walks. The validation
// happens BEFORE the renderer returns to its caller — a tombstone
// hit aborts the publish.
func renderTOML(ctx tomlRenderCtx) ([]byte, map[string]string, error) {
	if ctx.DC == nil {
		return nil, nil, fmt.Errorf("renderer: %s: nil DaemonConfig", ctx.Daemon)
	}
	// Daemon may arrive in either registry form (gatewayd-internal)
	// or HostKeys form (gatewayd_internal). Translate once at the
	// boundary; everything below sees the HostKeys form.
	hostKey := registryToHostKey(ctx.Daemon)
	host, ok := manifest.HostKeys[hostKey]
	if !ok {
		return nil, nil, fmt.Errorf("renderer: %s: no HostKeys descriptor (add a row to pkg/manifest.HostKeys)", ctx.Daemon)
	}

	// FlatMap = "table.leaf" key/value. Top-level keys use the leaf
	// alone (table == ""). Table-block keys use "table.leaf".
	flat := make(map[string]string, len(host.PrivateKeys)+len(host.ComputeNodeBlock))

	// Top-level private keys. These are read from manifest.Daemons.<d>
	// by name. The renderer uses the canonical schema names from
	// HostKeys.PrivateKeys; unknown keys (a renderer bug) are caught
	// by the validator at the "key not in catalog" check.
	dbURL := ctx.DBURL
	if ctx.HostRole == "control-plane" {
		dbURL = controlPlanePostgresSocketDSN
	}
	if ctx.HostRole == "compute-only" {
		dbURL = ""
	}
	if err := emitPrivateKeys(ctx.Daemon, ctx.DC, dbURL, ctx.AppsDomain, ctx.HostAddress, ctx.HostRole, host.PrivateKeys, flat); err != nil {
		return nil, nil, err
	}

	// [compute_node] table-block (vmmd only today). The renderer
	// pulls the values from the manifest's DaemonConfig (Outbound
	// for the dial target, hostSANs for the per-host SAN overlay).
	if err := emitComputeNodeBlock(ctx.Daemon, ctx.DC, host.ComputeNodeBlock, ctx.HostSANs, ctx.HostName, ctx.HostAddress, flat); err != nil {
		return nil, nil, err
	}

	// Validator gate. The renderer calls the SAME validator the
	// CLI's `manifest validate` runs (PR-0 carve-out). A tombstone
	// hit or a private-key-in-table hit aborts the publish.
	if errs := manifest.ValidateTOMLPlacement(hostKey, flat); errs != nil {
		return nil, nil, fmt.Errorf("renderer: %s: %s", ctx.Daemon, errs.Error())
	}

	// Serialise. Hand-rolled emitter — the schema is fixed and small
	// (~10 keys per daemon) so a pellucid-toml dep is overkill.
	body := serialiseTOML(host, flat)
	return body, flat, nil
}

// emitPrivateKeys copies the manifest's DaemonConfig values into the
// flatMap under their leaf names. Missing manifest values (e.g. a
// schedd with no apps_domain) yield an empty value; the validator +
// daemon-load path handle the absent-key shape.
func emitPrivateKeys(daemon string, dc *manifest.DaemonConfig, dbURL, appsDomain, hostAddress, hostRole string, keys []string, flat map[string]string) error {
	for _, k := range keys {
		v, err := privateKeyValue(daemon, dc, dbURL, appsDomain, hostAddress, hostRole, k)
		if err != nil {
			return fmt.Errorf("renderer: %s: %s: %w", daemon, k, err)
		}
		flat[k] = v
	}
	return nil
}

// privateKeyValue returns the rendered value for a top-level
// (private) TOML key from the manifest's DaemonConfig.
//
// Bind is consumed for the keys that map to a listener (socket_path
// for unix://, listen_addr for tcp://). Anything else is pulled from
// the DaemonConfig's typed fields explicitly rather than via a
// reflection table — the schema is small and the failure mode of an
// unhandled key is loud (empty value flows into the validator as
// "key not in catalog").
//
// The appsDomain arg comes from the manifest's global DNS block
// (DNS.AppsDomain) — schedd / apid / gatewayd-internal publish it
// into their TOML so the daemons can serve tenant requests with
// the canonical apps_domain. Empty appsDomain is acceptable (the
// daemon falls back to FAAS_APPS_DOMAIN env var).
func privateKeyValue(daemon string, dc *manifest.DaemonConfig, dbURL, appsDomain, hostAddress, hostRole, key string) (string, error) {
	switch key {
	case "socket_path":
		// unix:///run/faas/<daemon>.sock → /run/faas/<daemon>.sock
		if strings.HasPrefix(dc.Bind, "unix://") {
			return strings.TrimPrefix(dc.Bind, "unix://"), nil
		}
		// tcp:// binds use listen_addr, not socket_path.
		return "", nil
	case "listen_addr":
		if strings.HasPrefix(dc.Bind, "tcp://") {
			// gRPC daemons consume location-transparent wire targets and
			// require the scheme at runtime. HTTP daemons consume a plain
			// net.Listen address, so retain their existing host:port shape.
			if daemon == "schedd" || daemon == "vmmd" {
				return dc.Bind, nil
			}
			return strings.TrimPrefix(dc.Bind, "tcp://"), nil
		}
		// unix:// binds use socket_path, not listen_addr.
		return "", nil
	case "metrics_addr":
		// Per-daemon Prometheus endpoint. The mapping is hardcoded
		// against the 8 daemons in pkg/daemonunitspec; a refactor
		// would carry the port on the daemonunitspec.Entry struct
		// itself, but for PR-2 the in-renderer table is the source
		// of truth and pkg/daemonunitspec is the audit target.
		if dc.Bind == "" {
			return "", nil
		}
		return metricsAddrForHost(daemon, hostRole, hostAddress)
	case "db_url":
		// The manifest is the source of truth for the shared database
		// endpoint. Leaving this empty makes db.Open fall back to the
		// local Unix socket, which is correct only for a one-box host
		// and causes split-box compute daemons to crash-loop.
		return dbURL, nil
	case "apps_domain":
		// schedd + apid + gatewayd-internal carry this. The daemon
		// loader uses the empty value as a "use the env var" signal
		// (FAAS_APPS_DOMAIN). Passing through the manifest's
		// DNS.AppsDomain makes the renderer's output deterministic
		// — no surprise per-host env-var overrides.
		return appsDomain, nil
	case "apid_loopback":
		// gatewayd-internal normally reaches apid over loopback. A
		// compute-only gatewayd must instead use the control-plane
		// listener rendered by the split-box manifest.
		return dc.APIDLoopback, nil
	case "route_metrics_enabled":
		return boolConfigValue(dc.RouteMetricsEnabled, true), nil
	case "streaming_enabled":
		return boolConfigValue(dc.StreamingEnabled, true), nil
	case "schedd_tls_cert_path", "schedd_tls_key_path", "schedd_tls_ca_path":
		return tlsMaterialValue(dc.ScheddTLS, key, "schedd_tls_"), nil
	case "egress_tls_cert_path", "egress_tls_key_path", "egress_tls_ca_path":
		return tlsMaterialValue(dc.EgressTLS, key, "egress_tls_"), nil
	case "vmmd_socket":
		// schedd's legacy unix fallback. TCP targets are rendered into
		// vmmd_target instead so the daemon cannot silently dial its local
		// socket on a split-box deployment.
		if dc.Outbound != nil && strings.HasPrefix(dc.Outbound.Target, "unix://") {
			return strings.TrimPrefix(dc.Outbound.Target, "unix://"), nil
		}
		return "", nil
	case "vmmd_target":
		// M9 gives every compute host a node-local schedd. Its
		// scheduler must drive the vmmd on the same host rather than
		// inheriting the manifest's control-plane compatibility target
		// (often vmmd.faas, which is intentionally only an alias for
		// the first compute host). Preserve an explicit unix target for
		// local development by leaving vmmd_target empty so vmmd_socket
		// carries the path; production compute nodes use vmmd's local
		// TCP listener.
		if daemon == "schedd" && hostRole == "compute-only" {
			if dc.Outbound != nil && strings.HasPrefix(dc.Outbound.Target, "unix://") {
				return "", nil
			}
			return "tcp://127.0.0.1:50051", nil
		}
		if daemon == "builderd" && hostAddress != "" {
			return manifest.TCPURL(hostAddress)
		}
		if dc.Outbound != nil && !strings.HasPrefix(dc.Outbound.Target, "unix://") {
			return dc.Outbound.Target, nil
		}
		return "", nil
	case "vmmd_tls_cert_path", "vmmd_tls_key_path", "vmmd_tls_ca_path":
		if daemon == "gatewayd-internal" {
			return tlsMaterialValue(dc.VMMTLS, key, "vmmd_tls_"), nil
		}
		if dc.Outbound == nil || dc.Outbound.TLS == nil {
			return "", nil
		}
		switch key {
		case "vmmd_tls_cert_path":
			return dc.Outbound.TLS.CertPath, nil
		case "vmmd_tls_key_path":
			return dc.Outbound.TLS.KeyPath, nil
		default:
			return dc.Outbound.TLS.CAPath, nil
		}
	case "schedd_client_cert_path", "schedd_client_key_path", "schedd_client_ca_path":
		return tlsMaterialValue(dc.ScheddClientTLS, key, "schedd_client_"), nil
	case "advisory_client_cert_path", "advisory_client_key_path", "advisory_client_ca_path":
		return tlsMaterialValue(dc.AdvisoryClientTLS, key, "advisory_client_"), nil
	case "gateway_synth_socket":
		if dc.GatewaySynthTarget == "" && dc.Outbound != nil && strings.HasPrefix(dc.Outbound.Target, "unix://") {
			return strings.TrimPrefix(dc.Outbound.Target, "unix://"), nil
		}
		return "", nil
	case "gateway_synth_target":
		// Synthetic cron dispatch from a compute-owned schedd stays on
		// the same host. The split-box manifest's public target belongs
		// to the control-plane scheduler and would otherwise send this
		// node's cron traffic across the fleet edge.
		if daemon == "schedd" && hostRole == "compute-only" {
			return "unix:///run/faas/gatewayd-internal.sock", nil
		}
		return dc.GatewaySynthTarget, nil
	case "gateway_metrics_url":
		return dc.GatewayMetricsURL, nil
	case "owner_user", "kernel_path":
		// vmmd-only. The renderer does not own these: the vmmd
		// systemd unit files declare them. They appear in
		// HostKeys.PrivateKeys because the renderer writes the
		// key=value pair into the TOML for runtime consumption,
		// not because the renderer populates them. Empty value is
		// acceptable — the validator passes, the vmmd load path
		// will fail loudly if the field is required.
		return "", nil
	case "tls_cert_path", "tls_key_path", "tls_ca_path":
		material := dc.TLS
		if daemon == "builderd" && dc.Outbound != nil {
			material = dc.Outbound.TLS
		}
		if material == nil {
			return "", nil
		}
		switch key {
		case "tls_cert_path":
			return material.CertPath, nil
		case "tls_key_path":
			return material.KeyPath, nil
		case "tls_ca_path":
			return material.CAPath, nil
		}
	}
	return "", nil
}

func boolConfigValue(configured *bool, defaultValue bool) string {
	if configured == nil {
		return strconv.FormatBool(defaultValue)
	}
	return strconv.FormatBool(*configured)
}

func tlsMaterialValue(material *manifest.TLSMaterial, key, prefix string) string {
	if material == nil {
		return ""
	}
	switch key {
	case prefix + "cert_path":
		return material.CertPath
	case prefix + "key_path":
		return material.KeyPath
	case prefix + "ca_path":
		return material.CAPath
	default:
		return ""
	}
}

// defaultMetricsAddrForDaemon returns the listener used by the daemon's
// dependency-aware readiness endpoint. The daemon registry is the lifecycle
// source of truth; deriving the address here prevents rendered TOML from
// drifting onto an old metrics port while fleet_verify probes a newer one.
func defaultMetricsAddrForDaemon(daemon string) string {
	for _, entry := range daemonunitspec.Registry {
		if entry.Name != daemon {
			continue
		}
		addr := strings.TrimPrefix(entry.Lifecycle.ReadyzURL, "http://")
		if slash := strings.IndexByte(addr, '/'); slash >= 0 {
			addr = addr[:slash]
		}
		return addr
	}
	return ""
}

func metricsAddrForHost(daemon, hostRole, hostAddress string) (string, error) {
	listener := defaultMetricsAddrForDaemon(daemon)
	if hostRole != "compute-only" || (daemon != "vmmd" && daemon != "imaged" && daemon != "builderd") {
		return listener, nil
	}
	if hostAddress == "" {
		return listener, nil
	}
	host, _, err := net.SplitHostPort(hostAddress)
	if err != nil {
		return "", fmt.Errorf("compute metrics require a host:port address: %w", err)
	}
	_, port, err := net.SplitHostPort(listener)
	if err != nil {
		return "", fmt.Errorf("invalid canonical metrics address %q: %w", listener, err)
	}
	return net.JoinHostPort(host, port), nil
}

// emitComputeNodeBlock populates the flatMap with the [compute_node]
// keys from the manifest's DaemonConfig + hostSANs sidecar. The
// ComputeNodeBlock catalog row is the source of truth for which keys
// belong here (only vmmd's [compute_node] table is populated today).
func emitComputeNodeBlock(daemon string, dc *manifest.DaemonConfig, keys []manifest.TableKey, hostSANs []string, hostName, hostAddress string, flat map[string]string) error {
	if len(keys) == 0 {
		return nil
	}
	if dc == nil {
		return nil
	}
	for _, k := range keys {
		v, err := computeNodeValue(daemon, dc, k, hostSANs, hostName, hostAddress)
		if err != nil {
			return fmt.Errorf("renderer: %s: %s.%s: %w", daemon, k.Table, k.Key, err)
		}
		flat[k.Table+"."+k.Key] = v
	}
	return nil
}

// computeNodeValue returns the rendered value for a [compute_node]
// table-block key. Today the catalog only has vmmd's [compute_node].
// Each key's value flows from the manifest's DaemonConfig + the
// hostSANs sidecar.
func computeNodeValue(daemon string, dc *manifest.DaemonConfig, k manifest.TableKey, hostSANs []string, hostName, hostAddress string) (string, error) {
	switch k.Key {
	case "name":
		// vmmd's self-registered identity is the manifest host name.
		// This keeps the database identity and the rendered systemd
		// unit on the same declarative source of truth.
		_ = daemon
		return hostName, nil
	case "target_url":
		// vmmd's dial target is the host endpoint, never its bind
		// address. A bind such as tcp://0.0.0.0:50051 is not routable
		// and would make the control plane dial itself. Use the
		// node-specific private endpoint so every compute row routes to
		// the box named by that row; the renderer adds that endpoint to
		// the host's PKI SAN set.
		if hostAddress != "" {
			return manifest.TCPURL(hostAddress)
		}
		return dc.Bind, nil
	case "overlay_ip":
		// Use the endpoint host when it is a literal IP. FQDN targets
		// remain valid for target_url and are resolved through the
		// operator's private DNS or /etc/hosts contract.
		if hostAddress != "" {
			host, _, err := manifest.ParseHostPort(hostAddress)
			if err != nil {
				return "", err
			}
			if _, err := netip.ParseAddr(host); err == nil {
				return host, nil
			}
		}
		return "", nil
	case "vpcpus", "mem_mb", "max_concurrency", "admission_ceiling_mb":
		// vmmd's compute capacity knobs. The renderer does not
		// own these — they come from the platform-level config
		// (the cgroup slice for memory, the operator's plan for
		// concurrency). Empty values flow through.
		return "", nil
	case "host_bridge_cidr":
		// PR scale-out tier-1 residual (Gap #3). Per-host bridge
		// /16 override — the slot allocator carves per-VM /30
		// leases from this CIDR's .2+ range. Empty falls back to
		// api.DefaultHostBridgeCIDR(). Validator (in
		// cmd/vmmd/config.go::validateHostBridgeCIDR) enforces
		// the /16 prefix length + non-RFC1918 constraints.
		if dc.ComputeNode == nil {
			return "", nil
		}
		return dc.ComputeNode.HostBridgeCIDR, nil
	case "overlay_cidr":
		// Per-host overlay subnet the detector prefers. Empty
		// falls back to api.DefaultOverlayCIDR() (Tailscale
		// 100.64.0.0/10). Validator enforces the §11 deny
		// subset-check via pkg/netns.ValidateOverlayCIDRs.
		if dc.ComputeNode == nil {
			return "", nil
		}
		return dc.ComputeNode.OverlayCIDR, nil
	case "overlay_interface":
		// PR scale-out tier-1 residual (Gap #5). Optional NIC pin
		// used by the overlay-IP detector. Empty means
		// "auto-detect via PreferCIDR scoring" (the v1 contract).
		// Operators with multiple NICs (LAN + tail/wg) on a single
		// host set this to disambiguate.
		if dc.ComputeNode == nil {
			return "", nil
		}
		return dc.ComputeNode.OverlayInterface, nil
	}
	return "", nil
}

// serialiseTOML emits the per-daemon TOML body. Top-level keys first
// (sorted), then each table-block section ([compute_node] for vmmd).
// The shape is the canonical Gregale TOML that the daemons already
// load — schema-enforced by pkg/manifest.HostKeys and
// ValidateTOMLPlacement, so the renderer cannot emit a malformed
// configuration.
func serialiseTOML(host manifest.HostBlock, flat map[string]string) []byte {
	var buf bytes.Buffer

	// Top-level (private) keys: entry's.PrivateKeys in
	// schema-declaration order. The validator guarantees every
	// key in flat belongs somewhere.
	for _, k := range host.PrivateKeys {
		if v, ok := flat[k]; ok {
			writeTOMLKV(&buf, k, v)
		}
	}
	// Any other top-level key (a renderer bug) gets reported here
	// rather than silently lost. This is the second line of defence
	// after the validator.
	for k := range flat {
		if !strings.Contains(k, ".") && !containsString(host.PrivateKeys, k) {
			// Top-level key not in the daemon's PrivateKeys list —
			// emit it on a best-effort basis but flag it in the
			// output header so the operator notices.
			_, _ = fmt.Fprintf(&buf, "# WARN: %s not in HostKeys.PrivateKeys for %s\n", k, host.Daemon)
			writeTOMLKV(&buf, k, flat[k])
		}
	}

	// Table-block keys. The catalog pins one block per daemon
	// today (vmmd's [compute_node]); we iterate the catalog
	// rather than the flatMap so the section order is deterministic.
	for _, k := range host.ComputeNodeBlock {
		section := k.Table
		// First key in a new section → emit the header.
		if !sectionHeaderEmitted(&buf, section) {
			_, _ = buf.WriteString("[")
			_, _ = buf.WriteString(section)
			_, _ = buf.WriteString("]\n")
		}
		fullKey := section + "." + k.Key
		writeTOMLKV(&buf, k.Key, flat[fullKey])
	}
	// Any other table-block key (renderer bug): emit it under the
	// section name + warn. The validator already rejected tombstones,
	// so this is the "we shipped a key under the wrong table" path.
	emittedSections := make(map[string]bool)
	for _, k := range host.ComputeNodeBlock {
		emittedSections[k.Table] = true
	}
	extra := make([]string, 0)
	for k := range flat {
		if !strings.Contains(k, ".") {
			continue
		}
		idx := strings.Index(k, ".")
		section, leaf := k[:idx], k[idx+1:]
		if emittedSections[section] && !containsTableKey(host.ComputeNodeBlock, section, leaf) {
			extra = append(extra, k)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		for _, k := range extra {
			_, _ = fmt.Fprintf(&buf, "# WARN: %s not in HostKeys.ComputeNodeBlock for %s\n", k, host.Daemon)
			idx := strings.Index(k, ".")
			section, leaf := k[:idx], k[idx+1:]
			if !sectionHeaderEmitted(&buf, section) {
				_, _ = buf.WriteString("[")
				_, _ = buf.WriteString(section)
				_, _ = buf.WriteString("]\n")
			}
			writeTOMLKV(&buf, leaf, flat[k])
		}
	}

	// Trailing newline.
	_ = buf.WriteByte('\n')
	return buf.Bytes()
}

// writeTOMLKV writes a typed TOML key/value if value is non-empty. Empty
// values are omitted (the daemon loads the TOML with default values).
func writeTOMLKV(buf *bytes.Buffer, key, value string) {
	if value == "" {
		return
	}
	_, _ = buf.WriteString(key)
	_, _ = buf.WriteString(" = ")
	if isTOMLBoolKey(key) {
		_, _ = buf.WriteString(value)
	} else {
		_, _ = buf.WriteString(tomlQuote(value))
	}
	_ = buf.WriteByte('\n')
}

func isTOMLBoolKey(key string) bool {
	return key == "route_metrics_enabled" || key == "streaming_enabled"
}

// tomlQuote serialises a string value as a TOML basic string. The
// Gregale schema is host:port, unix paths, and DNS names — none of
// which contain control characters or backslashes, so the simple
// escape suffices. The auditor (PR-4) is the load-bearing check
// against malformed values.
func tomlQuote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// sectionHeaderEmitted reports whether the TOML body in buf already
// contains a "[section]" header. Used to avoid duplicating the
// header on consecutive keys in the same table block.
func sectionHeaderEmitted(buf *bytes.Buffer, section string) bool {
	marker := "[" + section + "]"
	body := buf.String()
	idx := strings.LastIndex(body, marker)
	if idx == -1 {
		return false
	}
	// Confirm the header is the last section header (no other
	// section header after it).
	rest := body[idx+len(marker):]
	return !strings.Contains(rest, "[")
}

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func containsTableKey(xs []manifest.TableKey, table, leaf string) bool {
	for _, k := range xs {
		if k.Table == table && k.Key == leaf {
			return true
		}
	}
	return false
}
