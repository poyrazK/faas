// Package main's config — parsed from /etc/faas/githubd.toml (or the path
// passed via --config). Each field is independent of every other so a
// partial config file plus defaults produces a working daemon.

package main

import (
	"crypto/tls"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Config is the on-disk representation of the daemon's TOML config.
// File reads use BurntSushi/toml (already a transitive dep of many
// tools; pinning it here makes the daemon's config story explicit).
type Config struct {
	// HTTPAddr is the loopback bind address the plain HTTP webhook
	// listener uses. Defaults to 127.0.0.1:8083 (spec §11: githubd
	// is loopback-only, gatewayd reverse-proxies /webhooks/github).
	HTTPAddr string `toml:"http_addr"`

	// SocketPath is the unix-domain socket the gRPC server binds when
	// ListenAddr is empty. Defaults to /run/faas/githubd.sock
	// (ADR-015 dictates mode 0660 group `faas`).
	SocketPath string `toml:"socket_path"`

	// ListenAddr is the location-transparent gRPC listen target
	// (issue #95, ADR-025). Accepts unix:///path or tcp://host:port.
	// When empty, falls back to unix://+SocketPath. tcp targets
	// require all TLS paths to be set.
	ListenAddr string `toml:"listen_addr"`

	// Server-mTLS material (issue #95). All three paths empty =>
	// no TLS; all three set => RequireAndVerifyClientCert. Partial
	// cluster => startup error naming the missing fields.
	TLSCertPath string `toml:"tls_cert_path"`
	TLSKeyPath  string `toml:"tls_key_path"`
	TLSCAPath   string `toml:"tls_ca_path"`
}

// ResolveListenTarget returns the gRPC target the server should bind.
// ListenAddr wins when set; otherwise unix://+SocketPath. The returned
// string is wire.ParseTarget-compatible.
func (c *Config) ResolveListenTarget() string {
	if c.ListenAddr != "" {
		return c.ListenAddr
	}
	return "unix://" + c.SocketPath
}

// LoadServerTLS returns the server's mTLS config when all three paths
// are set, or (nil, nil) when none are set. A partial cluster is
// rejected — the wire helper returns the error verbatim so callers see
// which fields are missing.
func (c *Config) LoadServerTLS() (*tls.Config, error) {
	return wire.LoadServerTLSConfig(c.TLSCertPath, c.TLSKeyPath, c.TLSCAPath)
}

// LoadConfig reads a TOML file at path and returns the parsed Config
// with defaults filled in. A missing file is not an error if defaults
// suffice; in that case a default config is returned.
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		HTTPAddr:   "127.0.0.1:8083",
		SocketPath: "/run/faas/githubd.sock",
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("githubd: read %q: %w", path, err)
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("githubd: parse %q: %w", path, err)
	}
	return c, nil
}