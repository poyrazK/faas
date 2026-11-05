// imaged config — parsed from /etc/faas/imaged.toml. ADR-122 follow-on:
// imaged was previously env-only (FAAS_IMAGED_METRICS_ADDR); this file
// introduces the canonical TOML config surface that meterd/schedd/vmmd/
// builderd already use. Defaults are kept conservative so a missing or
// partial file still yields a runnable daemon — the env overlay
// (cmd/imaged/main.go) preserves every pre-existing operator knob.

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/role"
)

// Config is the on-disk representation of imaged's TOML config. The
// MetricsAddr field is the canonical bind target; the legacy
// FAAS_IMAGED_METRICS_ADDR env var becomes an overlay (see
// GetMetricsAddr below).
type Config struct {
	// AppsDomain matches public routing for deployment alias activation checks.
	AppsDomain string `toml:"apps_domain"`
	// MetricsAddr is the bind address for /metrics. Empty disables
	// the listener. Defaults to 127.0.0.1:9102 — the same loopback
	// address that the pre-ADR-122 env-only path used. Loopback by
	// convention; a non-loopback bind is unmitigated by the canonical
	// listener shape.
	MetricsAddr string `toml:"metrics_addr"`
	// Metrics listener timeouts (ADR-122). Each knob falls back to
	// the corresponding api.Metrics*SecondsDefault when zero.
	// MaxHeaderBytes is int64 to mirror api.DefaultMaxHeaderBytes.
	MetricsReadTimeout    time.Duration `toml:"metrics_read_timeout"`
	MetricsWriteTimeout   time.Duration `toml:"metrics_write_timeout"`
	MetricsIdleTimeout    time.Duration `toml:"metrics_idle_timeout"`
	MetricsMaxHeaderBytes int64         `toml:"metrics_max_header_bytes"`

	// Role is the box shape this imaged inhabits (Gate-B; env
	// override FAAS_IMAGED_ROLE wins when set). imaged is a
	// control-plane daemon — it refuses to start under
	// RoleComputeOnly. RoleSingleBox is the default and lets
	// single-box dev boot unmoved.
	Role role.Role `toml:"role"`
}

// GetMetricsAddr returns the bind address with env-var overlay
// (FAAS_IMAGED_METRICS_ADDR wins over TOML metrics_addr). Empty
// disables the listener. Mirrors cmd/apid/config.go::GetMetricsAddr.
func (c *Config) GetMetricsAddr(env func(string) string) string {
	if v := env("FAAS_IMAGED_METRICS_ADDR"); v != "" {
		return v
	}
	return c.MetricsAddr
}

func (c *Config) GetAppsDomain(env func(string) string) string {
	if value := env("FAAS_APPS_DOMAIN"); value != "" {
		return value
	}
	return c.AppsDomain
}

// MetricsListener returns the *http.Server timeouts + MaxHeaderBytes
// for imaged's metrics listener (ADR-122). Each knob falls back to
// the corresponding api.Metrics*SecondsDefault when the TOML field
// is zero. Same shape as cmd/{meterd,schedd,vmmd,builderd}/config.go
// ::MetricsListener so a future daemon can lift the helper verbatim.
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

// LoadConfig reads a TOML file at path with defaults filled in. A
// missing file is not an error — the defaults produce a working
// daemon. The pre-ADR-122 env-var surface stays valid via the
// GetMetricsAddr overlay and the per-knob envOr calls in main.go.
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		AppsDomain:  hostidentity.DefaultAppsDomain,
		MetricsAddr: "127.0.0.1:9102", // matches the legacy env-only default
		Role:        role.RoleSingleBox,
		// Seed the canonical shape in the default literal so any
		// code path that reads cfg.MetricsReadTimeout (etc.)
		// directly sees the constants rather than zero. Mirrors
		// cmd/apid/config.go:171-174 (the apid precedent). The
		// MetricsListener() helper still applies the zero→constant
		// fallback for callers that bypass the struct (none today;
		// the helper is the production path).
		MetricsReadTimeout:    time.Duration(api.MetricsReadTimeoutSecondsDefault) * time.Second,
		MetricsWriteTimeout:   time.Duration(api.MetricsWriteTimeoutSecondsDefault) * time.Second,
		MetricsIdleTimeout:    time.Duration(api.MetricsIdleTimeoutSecondsDefault) * time.Second,
		MetricsMaxHeaderBytes: api.DefaultMaxHeaderBytes,
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("imaged: read %q: %w", path, err)
	}
	if _, err := toml.Decode(string(b), c); err != nil {
		return nil, fmt.Errorf("imaged: parse %q: %w", path, err)
	}
	return c, nil
}
