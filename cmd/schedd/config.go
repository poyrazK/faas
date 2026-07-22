// schedd config — parsed from /etc/faas/schedd.toml. Every field has a working
// default so a missing or partial file still yields a runnable daemon.

package main

import (
	"crypto/tls"
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
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

	// Server-mTLS material for the gatewayd-facing gRPC surface (issue
	// #95). All three paths empty => no TLS; all three set =>
	// RequireAndVerifyClientCert. Partial cluster => startup error.
	TLSCertPath string `toml:"tls_cert_path"`
	TLSKeyPath  string `toml:"tls_key_path"`
	TLSCAPath   string `toml:"tls_ca_path"`

	// GatewaySynthSocket is the unix-domain socket schedd dials to
	// fire synthetic cron requests through gatewayd (spec §4.4, M7).
	// Mode 0660 group `faas` (ADR-015). Defaults to
	// /run/faas/gatewayd-internal.sock.
	GatewaySynthSocket string `toml:"gateway_synth_socket"`

	// OwnerUser owns the socket file (looked up by name). Defaults to
	// faas-schedd. Only consulted when the resolved listen target is
	// a unix socket.
	OwnerUser string `toml:"owner_user"`

	// MetricsAddr is the optional bind address for /metrics. Empty disables it.
	MetricsAddr string `toml:"metrics_addr"`

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
}

// ResolveListenTarget returns the gRPC target schedd should bind.
// ListenAddr wins when set; otherwise unix://+SocketPath.
func (c *Config) ResolveListenTarget() string {
	if c.ListenAddr != "" {
		return c.ListenAddr
	}
	return "unix://" + c.SocketPath
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

// LoadVMMTLS returns the client mTLS config schedd uses to dial vmmd.
// Empty cluster returns (nil, nil) — single-box default. Partial
// cluster is rejected with the vmmd_tls_* field names (not the
// generic tls_*) so an operator can map the error straight to a TOML
// key.
func (c *Config) LoadVMMTLS() (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefix("vmmd_", c.VMMTLSCertPath, c.VMMTLSKeyPath, c.VMMTLSCAPath)
}

// LoadConfig reads a TOML file at path with defaults filled in. A missing file
// is not an error — the defaults produce a working daemon.
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		SocketPath:         "/run/faas/schedd.sock",
		VMMDSocket:         "/run/faas/vmmd.sock",
		GatewaySynthSocket: "/run/faas/gatewayd-internal.sock",
		OwnerUser:          "faas-schedd",
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("schedd: read %q: %w", path, err)
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("schedd: parse %q: %w", path, err)
	}
	return c, nil
}
