// meterd config — parsed from /etc/faas/meterd.toml. Mirrors the schedd
// pattern (cmd/schedd/config.go): every field has a working default so a
// missing or partial file still yields a runnable daemon.

package main

import (
	"crypto/tls"
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/egresssocket"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Config is the on-disk representation of meterd's TOML config.
type Config struct {
	// SocketPath is the schedd unix socket meterd dials to call ParkInstance
	// on Free-tier hard stop (slice 4 adds the RPC, ADR-019).
	SocketPath string `toml:"schedd_socket"`
	// DBURL is the Postgres DSN; empty falls back to $DATABASE_URL.
	DBURL string `toml:"db_url"`
	// MetricsAddr is the optional bind address for /metrics. Empty disables it.
	MetricsAddr string `toml:"metrics_addr"`
	// Metrics listener timeouts (ADR-122). Defaults fall back to
	// api.Metrics*SecondsDefault when zero — keep the type as
	// time.Duration to match the apid precedent (cmd/apid/config.go
	// :133-136). MaxHeaderBytes is int64 seconds to mirror
	// api.DefaultMaxHeaderBytes.
	MetricsReadTimeout    time.Duration `toml:"metrics_read_timeout"`
	MetricsWriteTimeout   time.Duration `toml:"metrics_write_timeout"`
	MetricsIdleTimeout    time.Duration `toml:"metrics_idle_timeout"`
	MetricsMaxHeaderBytes int64         `toml:"metrics_max_header_bytes"`
	// Meter is the pkg/meter timer cadence + behavior block.
	Meter *meter.Config `toml:"meter"`

	// ScheddTLS is the client mTLS material meterd uses to dial schedd
	// (ADR-052 / issue #95 slice 2). All three paths empty => no TLS,
	// single-box default; all three set => mTLS to remote schedd. Partial
	// cluster => startup error naming the missing fields.
	ScheddTLSCertPath string `toml:"schedd_tls_cert_path"`
	ScheddTLSKeyPath  string `toml:"schedd_tls_key_path"`
	ScheddTLSCAPath   string `toml:"schedd_tls_ca_path"`

	// EgressSocket is the egress byte-counter dial target meterd
	// dials to read tx_bytes (ADR-046). Defaults to
	// egresssocket.DefaultSocketPath (/run/faas/egress.sock); the
	// daemon-independent "egress" token mirrors the post-PR-B wire
	// package (onebox.faas.egress.v1) and the post-Tier-A7 daemon
	// split (ADR-070). Multi-box deployments override with tcp://
	// or dns:// plus the egress_tls_* cluster.
	EgressSocket string `toml:"egress_socket"`

	// EgressTLSCertPath / Key / CA configure the mTLS material meterd
	// uses to dial the egress listener when it lives on a remote
	// compute node (ADR-052). All three empty => no TLS (single-box
	// path uses the unix socket above); partial cluster => startup
	// error. Field names are prefixed with egress_ so an operator
	// can map the error straight to a TOML key.
	EgressTLSCertPath string `toml:"egress_tls_cert_path"`
	EgressTLSKeyPath  string `toml:"egress_tls_key_path"`
	EgressTLSCAPath   string `toml:"egress_tls_ca_path"`

	// GatewayEgressSocket is the deprecated (PR-C+D) alias for
	// EgressSocket. Operators on pre-PR-C+D deployments keep using
	// the gateway_egress_socket TOML key for one release cycle; the
	// resolver in pkg/gateway/egresssocket gives EgressSocket
	// (egress_socket) precedence, then falls back to this legacy
	// field. PR-E + a follow-up PR removes this field.
	//
	// PR-0 (issue #678): the corresponding GatewayEgressTLS* field
	// set was deprecated in PR-C+D but never used (the alias
	// served an operator-side migration path for single-box
	// deployments that hadn't migrated egress_socket yet; the TLS
	// side never carried any traffic). Removing the field set
	// and its LoadGatewayEgressTLS helper is safe — see the
	// grep-pinned `GatewayEgressTLS` audit (cmd/meterd/config.go
	// had no callers). The PR-0 scope closes this dead surface.
	GatewayEgressSocket string `toml:"gateway_egress_socket"`

	// NodeName is the multi-box identity for the meterd process
	// (issue #678 / ADR-093 PR-0). When non-empty, meterd is in
	// multi-box mode: PR-B constructs PGNodeVerifier and threads
	// it through every Load*WithVerifier helper. When empty,
	// the verifier stays nil and stdlib trust alone runs (the
	// single-box dev back-compat path). Operator seeds the
	// matching row in compute_nodes via the existing
	// POST /v1/compute-nodes flow (no new apid handler — reuses
	// UpsertComputeNodeFromOperator). Defaults to "".
	NodeName string `toml:"node_name"`

	// Role is the box shape this meterd inhabits (Gate-B; env
	// override FAAS_METERD_ROLE wins when set). meterd is a
	// control-plane daemon — it refuses to start under
	// RoleComputeOnly. RoleSingleBox is the default and lets
	// single-box dev boot unmoved.
	Role role.Role `toml:"role"`
}

// LoadScheddTLS returns the client mTLS config meterd uses to dial
// schedd. Empty cluster returns (nil, nil); partial cluster is rejected.
func (c *Config) LoadScheddTLS() (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefix("schedd_", c.ScheddTLSCertPath, c.ScheddTLSKeyPath, c.ScheddTLSCAPath)
}

// MetricsListener returns the *http.Server timeouts + MaxHeaderBytes
// for meterd's metrics listener (ADR-122). Each knob falls back to
// the corresponding api.Metrics*SecondsDefault when the TOML field
// is zero. The signature is a single helper rather than four
// getters because the listener builds a single struct at one call
// site (cmd/meterd/main.go:metricsListenAndServe factory).
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

// LoadEgressTLS returns the client mTLS config meterd uses to dial the
// egress byte-counter listener. Empty cluster returns (nil, nil);
// partial cluster is rejected.
func (c *Config) LoadEgressTLS() (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefix("egress_", c.EgressTLSCertPath, c.EgressTLSKeyPath, c.EgressTLSCAPath)
}

// LoadConfig reads a TOML file at path with defaults filled in. A missing
// file is not an error — the defaults produce a working daemon.
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		Role:                role.RoleSingleBox,
		SocketPath:          "/run/faas/schedd.sock",
		EgressSocket:        egresssocket.DefaultSocketPath,
		GatewayEgressSocket: egresssocket.LegacySocketPath,
		Meter:               &meter.Config{},
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Gate-B: even on the missing-file path, resolve Role
			// against FAAS_METERD_ROLE so env wins over the
			// empty TOML default. role.FromConfig falls back to
			// RoleSingleBox when the env is unset.
			c.Role = role.FromConfig(string(c.Role), "FAAS_METERD_ROLE")
			if err := c.overlayQuietPauseUnbilled(); err != nil {
				return nil, err
			}
			return c, nil
		}
		return nil, fmt.Errorf("meterd: read %q: %w", path, err)
	}
	if _, err := toml.Decode(string(b), c); err != nil {
		return nil, fmt.Errorf("meterd: parse %q: %w", path, err)
	}
	if c.Meter == nil {
		c.Meter = &meter.Config{}
	}
	c.Meter.Defaults()
	// Gate-B: resolve Role AFTER toml.Decode so the post-decode
	// c.Role is consulted against FAAS_METERD_ROLE. Setting Role
	// in the defaults-struct literal lets toml.Decode overwrite
	// it, which would silently make the env override dead. The
	// role gate at boot calls role.Require to refuse to start
	// under the wrong box shape.
	c.Role = role.FromConfig(string(c.Role), "FAAS_METERD_ROLE")
	// Mega-PR-A (issue #911 / ADR-110 PR-1): env-var overlay for
	// NodeName so the systemd drop-in (deploy/ansible/roles/
	// control_plane_service/templates/99-faas-node-name.conf.j2)
	// can override the TOML node_name on every control-plane box.
	// Empty keeps the TOML value (single-box dev back-compat).
	if v := os.Getenv("FAAS_NODE_NAME"); v != "" {
		c.NodeName = v
	}
	if err := c.overlayQuietPauseUnbilled(); err != nil {
		return nil, err
	}
	return c, nil
}

// overlayQuietPauseUnbilled applies FAAS_QUIET_PAUSE_UNBILLED over
// [meter] quiet_pause_unbilled (ADR-20261023171905). A value other than off,
// shadow or on refuses to start rather than guessing how to bill.
func (c *Config) overlayQuietPauseUnbilled() error {
	if v := os.Getenv("FAAS_QUIET_PAUSE_UNBILLED"); v != "" {
		c.Meter.QuietPauseUnbilled = v
	}
	if _, err := meter.ParseQuietPauseMode(c.Meter.QuietPauseUnbilled); err != nil {
		return fmt.Errorf("meterd: %w", err)
	}
	return nil
}
