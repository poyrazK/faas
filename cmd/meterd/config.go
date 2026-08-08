// meterd config — parsed from /etc/faas/meterd.toml. Mirrors the schedd
// pattern (cmd/schedd/config.go): every field has a working default so a
// missing or partial file still yields a runnable daemon.

package main

import (
	"crypto/tls"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/gateway/egresssocket"
	"github.com/onebox-faas/faas/pkg/meter"
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
	GatewayEgressSocket string `toml:"gateway_egress_socket"`

	// GatewayEgressTLSCertPath / Key / CA are the deprecated (PR-C+D)
	// aliases for EgressTLS*. Supported for one release cycle so
	// single-box deployments that haven't updated /etc/faas/meterd.toml
	// keep working. PR-E + a follow-up PR removes these fields.
	GatewayEgressTLSCertPath string `toml:"gateway_egress_tls_cert_path"`
	GatewayEgressTLSKeyPath  string `toml:"gateway_egress_tls_key_path"`
	GatewayEgressTLSCAPath   string `toml:"gateway_egress_tls_ca_path"`
}

// LoadScheddTLS returns the client mTLS config meterd uses to dial
// schedd. Empty cluster returns (nil, nil); partial cluster is rejected.
func (c *Config) LoadScheddTLS() (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefix("schedd_", c.ScheddTLSCertPath, c.ScheddTLSKeyPath, c.ScheddTLSCAPath)
}

// LoadEgressTLS returns the client mTLS config meterd uses to dial the
// egress byte-counter listener. Empty cluster returns (nil, nil);
// partial cluster is rejected.
func (c *Config) LoadEgressTLS() (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefix("egress_", c.EgressTLSCertPath, c.EgressTLSKeyPath, c.EgressTLSCAPath)
}

// LoadGatewayEgressTLS returns the client mTLS config meterd uses to
// dial the egress listener through the deprecated gateway_egress_*
// field set. Empty cluster returns (nil, nil); partial cluster is
// rejected. Deprecated: use LoadEgressTLS. Supported for one release
// cycle so single-box deployments that haven't updated
// /etc/faas/meterd.toml keep working.
func (c *Config) LoadGatewayEgressTLS() (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefix("gateway_egress_", c.GatewayEgressTLSCertPath, c.GatewayEgressTLSKeyPath, c.GatewayEgressTLSCAPath)
}

// LoadConfig reads a TOML file at path with defaults filled in. A missing
// file is not an error — the defaults produce a working daemon.
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		SocketPath:          "/run/faas/schedd.sock",
		EgressSocket:        egresssocket.DefaultSocketPath,
		GatewayEgressSocket: egresssocket.LegacySocketPath,
		Meter:               &meter.Config{},
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
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
	return c, nil
}
