// Package main's config — parsed from /etc/faas/vmmd.toml (or the path
// passed via --config). Each field is independent of every other so a
// partial config file plus defaults produces a working daemon.

package main

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Config is the on-disk representation of the daemon's TOML config.
// File reads use pelletier/go-toml/v2 (already a transitive dep of
// many tools; pinning it here makes the daemon's config story
// explicit).
type Config struct {
	// SocketPath is the unix-domain socket the gRPC server binds.
	// Defaults to /run/faas/vmmd.sock. ADR-015 dictates mode 0660
	// group `faas`.
	SocketPath string `toml:"socket_path"`

	// MetricsAddr is the optional bind address for /metrics.
	// Empty disables the metrics endpoint.
	MetricsAddr string `toml:"metrics_addr"`

	// OwnerUser is the uid that owns the socket file. Defaults to
	// the daemon's own uid (lookups by name first).
	OwnerUser string `toml:"owner_user"`

	// KernelPath mirrors pkg/fcvm.Paths.Kernel. The daemon refuses to
	// start if the file does not exist.
	KernelPath string `toml:"kernel_path"`
}

// LoadConfig reads a TOML file at path and returns the parsed Config with
// defaults filled in. A missing file is not an error if defaults suffice;
// in that case an empty config is returned.
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		SocketPath: "/run/faas/vmmd.sock",
		KernelPath: "/srv/fc/base/vmlinux-6.1",
		OwnerUser:  "faas-vmmd",
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("vmmd: read %q: %w", path, err)
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("vmmd: parse %q: %w", path, err)
	}
	return c, nil
}
