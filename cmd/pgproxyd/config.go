package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is pgproxyd's TOML configuration (ADR-1027). Secrets live in
// separate files named here, never inline.
type Config struct {
	// ListenAddr is the public PostgreSQL listener, e.g. ":5432".
	ListenAddr string `toml:"listen_addr"`
	// HostSuffix is the SNI suffix after the database id.
	HostSuffix string `toml:"host_suffix"`
	// TLSCertPath and TLSKeyPath hold the *.<host_suffix> certificate.
	TLSCertPath string `toml:"tls_cert_path"`
	TLSKeyPath  string `toml:"tls_key_path"`
	// DecoySecretPath holds at least 32 random bytes keying decoy
	// verifiers for unknown databases and roles.
	DecoySecretPath string `toml:"decoy_secret_path"`
	// DBURLPath holds the control-plane database URL; pgproxyd only reads
	// role verifiers and connects read-only.
	DBURLPath string `toml:"db_url_path"`
	// ScheddTarget and the schedd_client_* paths reach schedd as
	// pgproxyd.faas; empty TLS paths suit a unix socket.
	ScheddTarget         string `toml:"schedd_target"`
	ScheddClientCertPath string `toml:"schedd_client_cert_path"`
	ScheddClientKeyPath  string `toml:"schedd_client_key_path"`
	ScheddClientCAPath   string `toml:"schedd_client_ca_path"`
	// The vmmd_client_* paths authenticate DatabaseSession streams.
	VmmdClientCertPath string `toml:"vmmd_client_cert_path"`
	VmmdClientKeyPath  string `toml:"vmmd_client_key_path"`
	VmmdClientCAPath   string `toml:"vmmd_client_ca_path"`
	// VerifierCacheTTL bounds how long a removed role keeps working.
	VerifierCacheTTL time.Duration `toml:"verifier_cache_ttl"`
}

// LoadConfig reads and validates the config file.
func LoadConfig(path string) (*Config, error) {
	cfg := &Config{ListenAddr: ":5432", ScheddTarget: "unix:///run/faas/schedd.sock", VerifierCacheTTL: 30 * time.Second}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("pgproxyd: read %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var missing []string
	for name, v := range map[string]string{
		"host_suffix": c.HostSuffix, "tls_cert_path": c.TLSCertPath, "tls_key_path": c.TLSKeyPath,
		"decoy_secret_path": c.DecoySecretPath, "db_url_path": c.DBURLPath, "schedd_target": c.ScheddTarget,
	} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("pgproxyd: missing %s", strings.Join(missing, ", "))
	}
	if c.VerifierCacheTTL < 0 || c.VerifierCacheTTL > 5*time.Minute {
		return errors.New("pgproxyd: verifier_cache_ttl must be between 0 and 5m")
	}
	return nil
}

// readSecret reads a secret file, refusing one other users can read.
func readSecret(path string, minLen int) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("pgproxyd: %s must not be readable by group or others", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) < minLen {
		return nil, fmt.Errorf("pgproxyd: %s holds fewer than %d bytes", path, minLen)
	}
	return raw, nil
}
