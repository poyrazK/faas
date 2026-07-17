// schedd config — parsed from /etc/faas/schedd.toml. Every field has a working
// default so a missing or partial file still yields a runnable daemon.

package main

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Config is the on-disk representation of schedd's TOML config.
type Config struct {
	// SocketPath is the unix-domain socket schedd's gRPC server binds (ADR-018,
	// mode 0660 group `faas`). Defaults to /run/faas/schedd.sock.
	SocketPath string `toml:"socket_path"`

	// VMMDSocket is the vmmd gRPC socket schedd dials to drive the microVM
	// lifecycle (ADR-014). Defaults to /run/faas/vmmd.sock.
	VMMDSocket string `toml:"vmmd_socket"`

	// GatewaySynthSocket is the unix-domain socket schedd dials to
	// fire synthetic cron requests through gatewayd (spec §4.4, M7).
	// Mode 0660 group `faas` (ADR-015). Defaults to
	// /run/faas/gatewayd-internal.sock.
	GatewaySynthSocket string `toml:"gateway_synth_socket"`

	// OwnerUser owns the socket file (looked up by name). Defaults to
	// faas-schedd.
	OwnerUser string `toml:"owner_user"`

	// MetricsAddr is the optional bind address for /metrics. Empty disables it.
	MetricsAddr string `toml:"metrics_addr"`

	// DBURL is the Postgres DSN; empty falls back to $DATABASE_URL (db.Open).
	DBURL string `toml:"db_url"`
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
