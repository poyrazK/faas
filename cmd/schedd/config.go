// schedd config — parsed from /etc/faas/schedd.toml. Every field has a working
// default so a missing or partial file still yields a runnable daemon.

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Config is the on-disk representation of schedd's TOML config.
type Config struct {
	// SocketPath is the unix-domain socket schedd's gRPC server binds when
	// ListenAddr is empty (ADR-018, mode 0660 group `faas`). Defaults to
	// /run/faas/schedd.sock.
	SocketPath string `toml:"socket_path"`

	// ListenAddr is the location-transparent gRPC listen target
	// (issue #95, ADR-025). Accepts unix:///path or tcp://host:port.
	// When empty, falls back to unix://+SocketPath for backwards
	// compatibility. tcp targets require all server TLS paths to be set.
	ListenAddr string `toml:"listen_addr"`

	// VMMDSocket is the vmmd gRPC socket schedd dials when VMMTarget is
	// empty. Defaults to /run/faas/vmmd.sock. (ADR-014)
	VMMDSocket string `toml:"vmmd_socket"`

	// VMMTarget is the location-transparent gRPC dial target for vmmd
	// (issue #95, ADR-025). When non-empty, takes precedence over
	// VMMDSocket and supports the unix|tcp|dns schemes.
	VMMTarget string `toml:"vmmd_target"`

	// VMMTLS* configure the mTLS material schedd uses to dial vmmd
	// (issue #95). All three paths empty => no TLS; all three set =>
	// RequireAndVerifyClientCert. Partial cluster => startup error.
	VMMTLSCertPath string `toml:"vmmd_tls_cert_path"`
	VMMTLSKeyPath  string `toml:"vmmd_tls_key_path"`
	VMMTLSCAPath   string `toml:"vmmd_tls_ca_path"`

	// Server-mTLS material for the gatewayd-internal-facing gRPC surface (issue
	// #95). All three paths empty => no TLS; all three set =>
	// RequireAndVerifyClientCert. Partial cluster => startup error.
	TLSCertPath string `toml:"tls_cert_path"`
	TLSKeyPath  string `toml:"tls_key_path"`
	TLSCAPath   string `toml:"tls_ca_path"`

	// GatewaySynthSocket is the legacy unix-domain socket schedd dials
	// to fire synthetic cron requests through gatewayd-internal (spec §4.4, M7).
	// Mode 0660 group `faas` (ADR-015). Defaults to
	// /run/faas/gatewayd-internal.sock. Deprecated: multi-box schedd
	// uses GatewaySynthTarget (a wire.ParseTarget-style URL). Setting
	// GatewaySynthSocket alone keeps the legacy one-box behaviour;
	// setting GatewaySynthTarget takes precedence.
	GatewaySynthSocket string `toml:"gateway_synth_socket"`

	// GatewaySynthTarget is the wire.ParseTarget-style URL schedd
	// uses to dial gatewayd-internal's listener (placement scheduler
	// PR, ADR-025 axis 3, Q8). Accepts unix://|tcp://|dns://.
	// Multi-box operators set this to
	// tcp://<gatewayd-internal-overlay-ip>:9090 (or https://... when the
	// tailnet ACL isn't enough on its own). Empty GatewaySynthTarget
	// falls back to the legacy GatewaySynthSocket for backwards
	// compatibility — existing tests + the e2e harness rely on the
	// legacy field name. The fallback lives in cmd/schedd/main.go
	// so LoadConfig stays a thin TOML-to-struct mapping.
	GatewaySynthTarget string `toml:"gateway_synth_target"`

	// OwnerUser owns the socket file (looked up by name). Defaults to
	// faas-schedd. Only consulted when the resolved listen target is
	// a unix socket.
	OwnerUser string `toml:"owner_user"`

	// HostAgeIdentityPath is the path to the age X25519 identity
	// schedd uses to open sealed webhook secrets (issue #476 /
	// ADR-076). When non-empty, the dispatcher unseals the row's
	// SecretSealed with this identity before signing the HMAC. When
	// empty, the dispatcher falls back to pkg/secretbox.DefaultHostKeyPath
	// (the same path githubd + apid use). Mirrors cmd/meterd/main.go:939
	// where this pattern first landed.
	HostAgeIdentityPath string `toml:"host_age_identity_path"`

	// MetricsAddr is the optional bind address for /metrics. Empty disables it.
	MetricsAddr string `toml:"metrics_addr"`
	// Metrics listener timeouts (ADR-122). Each knob falls back to
	// the corresponding api.Metrics*SecondsDefault when zero. MaxHeaderBytes
	// is int64 to mirror api.DefaultMaxHeaderBytes (cast at the
	// http.Server field which is int).
	MetricsReadTimeout    time.Duration `toml:"metrics_read_timeout"`
	MetricsWriteTimeout   time.Duration `toml:"metrics_write_timeout"`
	MetricsIdleTimeout    time.Duration `toml:"metrics_idle_timeout"`
	MetricsMaxHeaderBytes int64         `toml:"metrics_max_header_bytes"`

	// DBURL is the Postgres DSN; empty falls back to $DATABASE_URL (db.Open).
	DBURL string `toml:"db_url"`

	// RetentionDuration is the §17 retention sweep window (PR #74).
	// STOPPED/FAILED instances are DELETED this long after entering the
	// terminal state. Zero or negative reverts to
	// api.DefaultInstanceRetention (30d). The sweep itself runs at the
	// api.DefaultRetentionInterval cadence (1h) regardless.
	RetentionDuration int64 `toml:"retention_duration_ns"`

	// HeartbeatInterval is the per-node liveness sweep cadence
	// (issue #97 / ADR-025 axis 3, PR #114). Zero or negative reverts
	// to sched.DefaultHeartbeatInterval (30s). Shorter is fine for
	// dev boxes but raises Postgres write traffic — production
	// should leave it at the default unless ops have a reason.
	HeartbeatInterval time.Duration `toml:"heartbeat_interval"`

	// HeartbeatStaleness is the age threshold at which a stale
	// last_heartbeat_at flips active=false (issue #98 / ADR-028
	// acceptance: "Watchdog marks a node active=false after 90s of
	// missed pings"). Zero or negative reverts to
	// sched.DefaultHeartbeatStaleness (90s). The invariant
	// HeartbeatInterval < HeartbeatStaleness prevents a single
	// missed tick from deactivating a healthy node — keep at
	// least 2 × Interval.
	HeartbeatStaleness time.Duration `toml:"heartbeat_staleness"`

	// GatewayMetricsURL is the absolute URL of gatewayd-internal's /metrics
	// endpoint (issue #169 / #172). The schedd scale-up trigger
	// scrapes this URL every cfg.ScaleUpInterval for
	// `gateway_requests_total{app=...}` so it can compute per-app
	// RPS. Empty disables only this optional scrape; the trigger
	// can still use the provider-independent VMMD activity-counter
	// signal from the instancestats reader. Defaults to
	// http://127.0.0.1:9090/metrics, matching gatewayd-internal's
	// ControlAddr default (cmd/gatewayd-internal/config.go).
	GatewayMetricsURL string `toml:"gateway_metrics_url"`

	// ScaleUpInterval is the per-app reactive scale-up trigger
	// cadence. Zero or negative reverts to
	// api.ScaleUpDecisionIntervalSeconds (1s). 1s is the right
	// balance between "admit Nth instance before the gateway
	// queue builds" and "don't hammer Postgres with a full app
	// list on every tick" — the trigger reads from apps +
	// instances per tick.
	ScaleUpInterval time.Duration `toml:"scaleup_interval"`

	// ReaperAggressive (issue #171) toggles the aggressive-reaper
	// scale-down path. Default ON (true) — schedd parks surplus
	// instances above max(min_instances, desired + 1) on the next
	// 10 s reaper tick when recent-window RPS is below target.
	// Set false via FAAS_REAPER_AGGRESSIVE=false to disable
	// in-place if a regression surfaces; the signal mirror still
	// runs so the metric and the audit row surface for diagnosis.
	// The flag does NOT disable the existing ReapIdle timeout
	// reaper — only the new path.
	ReaperAggressive bool `toml:"reaper_aggressive"`

	// ReaperAggressiveParkCap (issue #171) caps the number of
	// aggressive-path parks per app per 10 s tick. Zero reverts
	// to sched.MaxParksPerTickPerApp (= 8). The cap prevents a
	// single tick from blocking the reaper for `cap × ~150 ms`
	// during a sudden-scale-down storm. The existing
	// ReapIdle / SelectEvictions paths are NOT capped — they
	// already drain at their own cadences.
	ReaperAggressiveParkCap int `toml:"reaper_aggressive_park_cap"`

	// MigratingWatchdogTickLimit (ADR-067) caps the per-tick
	// batch of state='migrating' rows the watchdog self-heals.
	// Zero reverts to api.MigratingWatchdogTickLimit (= 50).
	// The cap prevents a wedged-migration storm from monopolising
	// the schedd worker pool when many rows are stuck at once
	// (e.g. after a multi-node outage).
	MigratingWatchdogTickLimit int `toml:"migrating_watchdog_tick_limit"`

	// MigratingWatchdogIntervalSeconds (ADR-067) is the watchdog
	// tick cadence in seconds. Zero reverts to
	// api.MigratingWatchdogIntervalSeconds (= 1). The 1 s default
	// matches the §6.1 instance watchdog — operators that want
	// less aggressive reconciliation (e.g. to lower DB load)
	// can bump this; the conditional UPDATE on
	// state='migrating' keeps the per-row work idempotent.
	MigratingWatchdogIntervalSeconds int `toml:"migrating_watchdog_interval_seconds"`

	// DeadNodeReconcilerIntervalSeconds is the cadence of the
	// stale-RUNNING billing-leak self-healer in seconds. Zero
	// reverts to api.DeadNodeReconcilerIntervalSeconds (= 30).
	// Deliberately coarser than the §6.1 watchdog's 1 s tick:
	// the staleness window this sweeper enforces is 120 s
	// (api.DeadNodeReconcilerStalenessSeconds), so a 1 s tick
	// would issue ~120 no-op queries per node death before any
	// row is even eligible. Operators that want faster bill-stop
	// on a node death can lower this; doing so under 10 s is not
	// useful because the staleness threshold dominates the
	// earliest-possible reconciliation time.
	DeadNodeReconcilerIntervalSeconds int `toml:"dead_node_reconciler_interval_seconds"`

	// DeadNodeReconcilerStalenessSeconds is how stale a
	// compute_node's last_heartbeat_at must be (OR active=false)
	// before its RUNNING instances are eligible for the
	// failed-transition self-heal. Zero reverts to
	// api.DeadNodeReconcilerStalenessSeconds (= 120). The default
	// is the §6.1 heartbeat staleness (90 s) plus one 30 s
	// tick of slack — short enough that a customer sees the
	// bill stop within two reconciliation cycles after a vmmd
	// dies, long enough that a transient heartbeat hiccup (a
	// single missed 30 s ping) does not spuriously terminate
	// live instances.
	DeadNodeReconcilerStalenessSeconds int `toml:"dead_node_reconciler_staleness_seconds"`

	// NodeName is the multi-box gate (ADR-056, mirrored from vmmd's
	// [compute_node].name). When set, schedd constructs the
	// handshake-layer NodeVerifier and surfaces a populated
	// compute_nodes snapshot to every mTLS leg on listen. Empty
	// (one-box dev / pre-slice-3 schedd) keeps the verifier off
	// entirely — stdlib chain + RFC 6125 SAN + EKU alone run. The
	// synthetic `default-local` row seeded by migration 00024 is
	// always present, so the verifier, when wired, finds at least
	// one entry to bind against.
	//
	// The field is intentionally not backed by [compute_node] TOML
	// subsection for schedd: schedd is the control-plane trust
	// anchor across every compute node, not a self-registrant.
	// Operators set node_name = "schedd-<box>" through this field
	// and the [compute_nodes] row is provisioned by `faas node
	// register` (out of scope for ADR-056).
	NodeName string `toml:"node_name"`

	// Role is the box shape this schedd inhabits (Gate-B; env
	// override FAAS_SCHEDD_ROLE wins when set). schedd is a
	// control-plane daemon — it refuses to start under
	// RoleComputeOnly. RoleSingleBox is the default and lets
	// single-box dev boot unmoved. The host_vars setting
	// `faas_box_role: control-plane` propagates through ansible
	// to FAAS_SCHEDD_ROLE on the schedd unit; a missing env
	// keeps the field at RoleSingleBox.
	Role role.Role `toml:"role"`
}

// ResolveListenTarget returns the gRPC target schedd should bind.
// ListenAddr wins when set; otherwise unix://+SocketPath.
func (c *Config) ResolveListenTarget() string {
	if c.ListenAddr != "" {
		return c.ListenAddr
	}
	return "unix://" + c.SocketPath
}

// ResolveLocalNodeID translates cfg.NodeName → compute_nodes.id
// at startup, returning the durable Phase 2 / Gate A shard key
// this schedd owns. Phase 2 / Gate A, migration 00083. The
// empty-NodeName legacy posture returns ("", nil): the schedd
// is the implicit owner of every app on the box, the ownership
// guard short-circuits, and the single-box install preserves
// bit-for-bit behaviour.
//
// Failures (DB outage, NodeName set but no matching active
// compute_nodes row, NodeName resolves to default-local while
// any non-default-local is active) return a non-nil error so
// cmd/schedd's main exits fast — a misconfigured schedd
// silently falling back to in-process ownership would mask
// the multi-box wiring and route every wake to the local
// schedd instead of the owner.
func (c *Config) ResolveLocalNodeID(ctx context.Context, store state.Store) (string, error) {
	if c.NodeName == "" {
		return "", nil
	}
	// Look up the active compute_nodes row by name. The
	// state.Store exposes ActiveComputeNodes(ctx) but not a
	// per-name lookup, so we resolve via a small helper that
	// matches the (name, active=true) predicate.
	cn, err := store.ComputeNodeByName(ctx, c.NodeName)
	if err != nil {
		return "", fmt.Errorf("schedd: resolve %s: %w", c.NodeName, err)
	}
	if !cn.Active {
		return "", fmt.Errorf("schedd: compute_node %s is inactive", c.NodeName)
	}
	if cn.Name == "default-local" {
		// Multi-box guard: refuse to start schedd as the
		// legacy default-local row while any non-default-local
		// is also active. The synthetic default-local is
		// only meant to carry single-box apps; an operator
		// who set NodeName=default-local on a multi-box
		// install has a config bug that the runbook would
		// silently mask.
		return "", fmt.Errorf("schedd: refusing to start as default-local on a multi-node fleet (NodeName=%q must match a non-default-local active row)", c.NodeName)
	}
	return cn.ID, nil
}

// ResolveVMMTarget returns the gRPC dial target for vmmd. VMMTarget
// wins when set; otherwise unix://+VMMDSocket.
func (c *Config) ResolveVMMTarget() string {
	if c.VMMTarget != "" {
		return c.VMMTarget
	}
	return "unix://" + c.VMMDSocket
}

// LoadServerTLS returns the server's mTLS config when all three TLS
// paths are set, or (nil, nil) when none are set. Partial cluster is
// rejected — wire.LoadServerTLSConfig names the missing fields.
func (c *Config) LoadServerTLS() (*tls.Config, error) {
	return wire.LoadServerTLSConfig(c.TLSCertPath, c.TLSKeyPath, c.TLSCAPath)
}

// LoadServerTLSWithVerifier is the ADR-056 variant of LoadServerTLS.
// Schedd is the control-plane trust anchor, so it wires the
// verifier unconditionally when the multi-box gate is open.
func (c *Config) LoadServerTLSWithVerifier(v wire.NodeVerifier) (*tls.Config, error) {
	return wire.LoadServerTLSConfigWithVerifier(c.TLSCertPath, c.TLSKeyPath, c.TLSCAPath, v)
}

// LoadServerTLSWithPrefixAndVerifierAndReload is the ADR-052 §5
// / PR-E variant: per-handshake verifier + SIGHUP-driven cert
// rotation. nil v and nil reload are tolerated and degrade to
// LoadServerTLS (no hook, no callback) — same shape as the
// LoadServerTLSWithVerifier back-compat path. The reload closure
// is consulted by stdlib on every server-side handshake via
// tls.Config.GetConfigForClient (the canonical stdlib server
// callback — confusingly named; see pkg/wire/grpc.go:639-647).
func (c *Config) LoadServerTLSWithPrefixAndVerifierAndReload(v wire.NodeVerifier, reload wire.ReloadFunc) (*tls.Config, error) {
	return wire.LoadServerTLSConfigWithPrefixAndVerifierAndReload("", c.TLSCertPath, c.TLSKeyPath, c.TLSCAPath, v, reload)
}

// LoadVMMTLS returns the client mTLS config schedd uses to dial vmmd.
// Empty cluster returns (nil, nil) — single-box default. Partial
// cluster is rejected with the vmmd_tls_* field names (not the
// generic tls_*) so an operator can map the error straight to a TOML
// key.
func (c *Config) LoadVMMTLS() (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefix("vmmd_", c.VMMTLSCertPath, c.VMMTLSKeyPath, c.VMMTLSCAPath)
}

// LoadVMMTLSWithVerifier is the ADR-056 variant of LoadVMMTLS.
// Mirrors the prefix semantics (vmmd_ for error naming).
func (c *Config) LoadVMMTLSWithVerifier(v wire.NodeVerifier) (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefixAndVerifier("vmmd_", c.VMMTLSCertPath, c.VMMTLSKeyPath, c.VMMTLSCAPath, v)
}

// LoadVMMTLSWithPrefixAndVerifierAndReload is the ADR-052 §5
// / PR-E variant of LoadVMMTLSWithVerifier. Same back-compat
// contract as LoadServerTLSWithPrefixAndVerifierAndReload:
// nil v / nil reload tolerate the single-box / pre-rotation
// paths. The reload closure re-issues the client leaf on every
// handshake via tls.Config.GetClientCertificate; the trust root
// is fixed at config-build time per ADR-052 §Risks "CA rotation
// pain" (stdlib has no per-handshake client RootCAs callback).
func (c *Config) LoadVMMTLSWithPrefixAndVerifierAndReload(v wire.NodeVerifier, reload wire.ReloadFunc) (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefixAndVerifierAndReload("vmmd_", c.VMMTLSCertPath, c.VMMTLSKeyPath, c.VMMTLSCAPath, v, reload)
}

// MetricsListener returns the *http.Server timeouts + MaxHeaderBytes
// for schedd's metrics listener (ADR-122). Each knob falls back to
// the corresponding api.Metrics*SecondsDefault when the TOML field
// is zero. Same shape as cmd/meterd/config.go::MetricsListener;
// the listener builds a single struct at one call site
// (cmd/schedd/main.go:metricsListenAndServe factory).
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

// LoadConfig reads a TOML file at path with defaults filled in. A missing file
// is not an error — the defaults produce a working daemon.
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		SocketPath:         "/run/faas/schedd.sock",
		VMMDSocket:         "/run/faas/vmmd.sock",
		GatewaySynthSocket: "/run/faas/gatewayd-internal.sock",
		// GatewaySynthTarget stays empty by default so the fallback
		// in cmd/schedd/main.go (synthTarget == "" → "unix://"+
		// GatewaySynthSocket) owns the default-target resolution.
		// That preserves the one-box path (synthTarget resolves to
		// "unix:///run/faas/gatewayd-internal.sock") AND lets the
		// e2e harness's gateway_synth_socket TOML entry actually
		// override the dial — a previous PR landed a non-empty
		// default here, which silently shadowed the legacy socket
		// and broke the drain goroutine in
		// TestE2E_AsyncInvoke_PostEnqueuesRowAndDrainCompletesIt
		// and TestE2E_QueueSend_DrainLongPoll (e2e harness points
		// gateway_synth_socket at /tmp/.../gatewayd-internal.sock).
		// Multi-box operators set gateway_synth_target TOML or
		// FAAS_GATEWAY_SYNTH_TARGET env to take precedence.
		GatewaySynthTarget: "",
		OwnerUser:          "faas-schedd",
		// Issue #169 / #172: default to gatewayd-internal's loopback
		// control listener. Empty disables the trigger (the
		// loop with WithScaleUp(nil) skips the ticker arm).
		GatewayMetricsURL: "http://127.0.0.1:9090/metrics",
		// issue #171: aggressive reaper defaults to ON. Operators
		// can flip FAAS_REAPER_AGGRESSIVE=false to disable in-place
		// without redeploying.
		ReaperAggressive: true,
		// ADR-067: 0 means "use the api.* default"
		// (api.MigratingWatchdogTickLimit = 50,
		// api.MigratingWatchdogIntervalSeconds = 1). cmd/schedd/main.go
		// fills them in at loop-construction time so an unset TOML
		// and an unset env var both resolve to the spec defaults.
		MigratingWatchdogTickLimit:       0,
		MigratingWatchdogIntervalSeconds: 0,
		// Stale-RUNNING billing-leak self-healer. 0 means "use the
		// api.* default" (api.DeadNodeReconcilerIntervalSeconds = 30,
		// api.DeadNodeReconcilerStalenessSeconds = 120).
		// cmd/schedd/main.go fills them in at loop-construction
		// time so an unset TOML and an unset env var both resolve
		// to the spec defaults.
		DeadNodeReconcilerIntervalSeconds:  0,
		DeadNodeReconcilerStalenessSeconds: 0,
	}
	applyRoutingEnv := func() {
		// These overlays are the deployment-safe path for split-box
		// routing: the endpoint is host-specific, while the TOML stays
		// portable across control-plane nodes. LookupEnv is intentional
		// for metrics so an operator can explicitly disable the scrape.
		if v := os.Getenv("FAAS_GATEWAY_SYNTH_TARGET"); v != "" {
			c.GatewaySynthTarget = v
		}
		if v, ok := os.LookupEnv("FAAS_GATEWAY_METRICS_URL"); ok {
			c.GatewayMetricsURL = v
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Gate-B: even on the missing-file path, resolve Role
			// against FAAS_SCHEDD_ROLE so env wins over the empty
			// TOML default. role.FromConfig falls back to
			// RoleSingleBox when the env is unset.
			c.Role = role.FromConfig(string(c.Role), "FAAS_SCHEDD_ROLE")
			applyRoutingEnv()
			return c, nil
		}
		return nil, fmt.Errorf("schedd: read %q: %w", path, err)
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("schedd: parse %q: %w", path, err)
	}
	// Gate-B: resolve Role AFTER toml.Unmarshal so the post-decode
	// c.Role is consulted against FAAS_SCHEDD_ROLE. Setting Role in
	// the defaults-struct literal lets toml.Unmarshal overwrite it,
	// which would silently make the env override dead. The role
	// gate at boot calls role.Require to refuse to start under the
	// wrong box shape.
	c.Role = role.FromConfig(string(c.Role), "FAAS_SCHEDD_ROLE")
	// Mega-PR-A (issue #911 / ADR-110 PR-1): env-var overlay for
	// NodeName so the systemd drop-in (deploy/ansible/roles/
	// control_plane_service/files/faas-schedd.service.d/
	// 99-faas-node-name.conf) can override the TOML node_name on
	// every control-plane box. Empty keeps the TOML value
	// (single-box dev back-compat).
	if v := os.Getenv("FAAS_NODE_NAME"); v != "" {
		c.NodeName = v
	}
	applyRoutingEnv()
	return c, nil
}
