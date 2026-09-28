// Package main's config — parsed from /etc/faas/githubd.toml (or the path
// passed via --config). Each field is independent of every other so a
// partial config file plus defaults produces a working daemon.

package main

import (
	"crypto/tls"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Config is the on-disk representation of the daemon's TOML config.
// File reads use BurntSushi/toml (already a transitive dep of many
// tools; pinning it here makes the daemon's config story explicit).
type Config struct {
	// HTTPAddr is the loopback bind address the plain HTTP webhook
	// listener uses. Defaults to 127.0.0.1:8083 (spec §11: githubd
	// is loopback-only, gatewayd-public reverse-proxies /webhooks/github).
	HTTPAddr string `toml:"http_addr"`

	// SocketPath is the local unix-domain gRPC socket. It is the primary
	// listener when ListenAddr is empty and remains available alongside a
	// TCP listener so same-box apid keeps its local transport. Defaults to
	// /run/faas/githubd.sock (ADR-015 dictates mode 0660 group `faas`).
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

	// Role is the box shape this githubd inhabits (Gate-B; env
	// override FAAS_GITHUBD_ROLE wins when set). githubd is a
	// control-plane daemon — it refuses to start under
	// RoleComputeOnly. RoleSingleBox is the default and lets
	// single-box dev boot unmoved.
	Role role.Role `toml:"role"`

	// NodeName is the multi-box identity for the githubd process
	// (issue #678 / ADR-093 PR-0). When non-empty, githubd is in
	// multi-box mode: PR-B constructs PGNodeVerifier and threads
	// it through every Load*WithVerifier helper. When empty,
	// the verifier stays nil and stdlib trust alone runs (the
	// single-box dev back-compat path). Operator seeds the
	// matching row in compute_nodes via the existing
	// POST /v1/compute-nodes flow (no new apid handler — reuses
	// UpsertComputeNodeFromOperator). Defaults to "".
	//
	// Also reused for the githubd → apid bridge dialer (PR-C1
	// wires the bridge tlsCfg alongside the server-side verifier).
	NodeName string `toml:"node_name"`
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
			// Gate-B: even on the missing-file path, resolve Role
			// against FAAS_GITHUBD_ROLE so env wins over the
			// empty TOML default. role.FromConfig falls back to
			// RoleSingleBox when the env is unset.
			c.Role = role.FromConfig(string(c.Role), "FAAS_GITHUBD_ROLE")
			c.applyEnvironment()
			return c, nil
		}
		return nil, fmt.Errorf("githubd: read %q: %w", path, err)
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("githubd: parse %q: %w", path, err)
	}
	// Gate-B: resolve Role AFTER toml.Unmarshal so the post-decode
	// c.Role is consulted against FAAS_GITHUBD_ROLE. Setting Role
	// in the defaults-struct literal lets toml.Unmarshal overwrite
	// it, which would silently make the env override dead. The
	// role gate at boot calls role.Require to refuse to start
	// under the wrong box shape.
	c.Role = role.FromConfig(string(c.Role), "FAAS_GITHUBD_ROLE")
	c.applyEnvironment()
	return c, nil
}

// applyEnvironment overlays deployment-owned listener settings. Single-box
// development keeps the TOML defaults, while split-box Ansible sets the TCP
// listener and mTLS leaves through the systemd drop-in.
func (c *Config) applyEnvironment() {
	if v := os.Getenv("FAAS_GITHUBD_LISTEN_ADDR"); v != "" {
		c.ListenAddr = v
	}
	if v := os.Getenv("FAAS_GITHUBD_TLS_CERT_PATH"); v != "" {
		c.TLSCertPath = v
	}
	if v := os.Getenv("FAAS_GITHUBD_TLS_KEY_PATH"); v != "" {
		c.TLSKeyPath = v
	}
	if v := os.Getenv("FAAS_GITHUBD_TLS_CA_PATH"); v != "" {
		c.TLSCAPath = v
	}
	// Mega-PR-A (issue #911 / ADR-110 PR-1): env-var overlay for
	// NodeName so the systemd drop-in (deploy/ansible/roles/
	// githubd_service/files/faas-githubd.service.d/
	// 99-faas-node-name.conf) can override the TOML node_name on
	// every box. Empty keeps the TOML value (single-box dev).
	if v := os.Getenv("FAAS_NODE_NAME"); v != "" {
		c.NodeName = v
	}
}
