// meterd config — parsed from /etc/faas/meterd.toml. Mirrors the schedd
// pattern (cmd/schedd/config.go): every field has a working default so a
// missing or partial file still yields a runnable daemon.

package main

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/meter"
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
}

// LoadConfig reads a TOML file at path with defaults filled in. A missing
// file is not an error — the defaults produce a working daemon.
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		SocketPath: "/run/faas/schedd.sock",
		Meter:      &meter.Config{},
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
