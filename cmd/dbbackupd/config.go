package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/onebox-faas/faas/pkg/dbarchive"
)

// Config is dbbackupd's TOML configuration (ADR-1027). Secrets live in
// separate owner-only files named here, never inline.
type Config struct {
	// SocketPath is where vmmd's relays connect. Only vmmd (root) reaches
	// it: the directory is the daemon's own.
	SocketPath string `toml:"socket_path"`
	// StoreDir holds WAL and base backups on the node's local disk.
	StoreDir string `toml:"store_dir"`
	// OpsAddr serves /healthz, /readyz and /metrics on loopback.
	OpsAddr string `toml:"ops_addr"`
	// MinRetention is how long every database's archive restores, whatever
	// its restore window; a longer window keeps more (pkg/dbarchive).
	MinRetention time.Duration `toml:"min_retention"`
	// PruneInterval is how often archives are pruned to their retention.
	PruneInterval time.Duration `toml:"prune_interval"`
	// Remote ships the archive off the node; without it the archive lives
	// only on this node's disk.
	Remote *RemoteConfig `toml:"remote"`
}

// RemoteConfig reaches an S3-compatible bucket.
type RemoteConfig struct {
	Endpoint  string `toml:"endpoint"`
	Region    string `toml:"region"`
	Bucket    string `toml:"bucket"`
	Prefix    string `toml:"prefix"`
	PathStyle bool   `toml:"path_style"`
	// AccessKeyIDPath and SecretAccessKeyPath are owner-only files.
	AccessKeyIDPath     string        `toml:"access_key_id_path"`
	SecretAccessKeyPath string        `toml:"secret_access_key_path"`
	UploadInterval      time.Duration `toml:"upload_interval"`
}

// DefaultOpsAddr is dbbackupd's loopback ops listener.
const DefaultOpsAddr = "127.0.0.1:9110"

// LoadConfig reads and validates the config file.
func LoadConfig(path string) (*Config, error) {
	cfg := &Config{SocketPath: "/run/faas-dbbackupd/dbbackupd.sock", StoreDir: "/srv/db/archive", OpsAddr: DefaultOpsAddr,
		MinRetention: 24 * time.Hour, PruneInterval: time.Hour}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("dbbackupd: read %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if !filepath.IsAbs(c.SocketPath) || !filepath.IsAbs(c.StoreDir) {
		return errors.New("dbbackupd: socket_path and store_dir must be absolute")
	}
	if c.MinRetention < time.Hour || c.MinRetention > 400*24*time.Hour {
		return errors.New("dbbackupd: min_retention must be between 1h and 400 days")
	}
	if c.PruneInterval < time.Minute || c.PruneInterval > 24*time.Hour {
		return errors.New("dbbackupd: prune_interval must be between 1m and 24h")
	}
	r := c.Remote
	if r == nil {
		return nil
	}
	if r.Bucket == "" || r.AccessKeyIDPath == "" || r.SecretAccessKeyPath == "" {
		return errors.New("dbbackupd: remote needs bucket, access_key_id_path and secret_access_key_path")
	}
	if strings.Trim(r.Prefix, "/") == "" {
		return errors.New("dbbackupd: remote prefix is required (the bucket may hold other data)")
	}
	if r.UploadInterval == 0 {
		r.UploadInterval = 10 * time.Second
	}
	if r.UploadInterval < time.Second || r.UploadInterval > 10*time.Minute {
		return errors.New("dbbackupd: remote upload_interval must be between 1s and 10m")
	}
	return nil
}

// s3Config resolves the remote's credentials.
func (r *RemoteConfig) s3Config() (dbarchive.S3Config, error) {
	keyID, err := readSecret(r.AccessKeyIDPath)
	if err != nil {
		return dbarchive.S3Config{}, err
	}
	secret, err := readSecret(r.SecretAccessKeyPath)
	if err != nil {
		return dbarchive.S3Config{}, err
	}
	return dbarchive.S3Config{Endpoint: r.Endpoint, Region: r.Region, Bucket: r.Bucket, Prefix: r.Prefix,
		PathStyle: r.PathStyle, KeyID: keyID, Secret: secret}, nil
}

// readSecret reads a secret file, refusing one other users can read.
func readSecret(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("dbbackupd: %s must not be readable by group or others", path)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // operator-configured secret path
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", fmt.Errorf("dbbackupd: %s is empty", path)
	}
	return value, nil
}
