package main

import (
	"fmt"
	"strconv"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// ADR-20261110210000 slice 3d-5: how sealed snapshots are written and opened.
const (
	// envSealedFormat is v1 (default, one age stream) or v2 (chunked,
	// opened in parallel without its zero pages). Set v2 only once every
	// vmmd that may restore a capture reads v2.
	envSealedFormat = "FAAS_VMMD_SEALED_FORMAT"
	// envSealedPlainCacheDir keeps decrypted sealed snapshots on this node
	// so only the first wake per capture decrypts; empty turns it off.
	envSealedPlainCacheDir = "FAAS_VMMD_SEALED_PLAIN_CACHE_DIR"
	// envSealedPlainCacheMB bounds that cache's disk use.
	envSealedPlainCacheMB = "FAAS_VMMD_SEALED_PLAIN_CACHE_MB"

	defaultSealedPlainCacheMB = 20 << 10
)

type sealedSnapshotConfig struct {
	chunked    bool
	cacheDir   string
	cacheBytes int64
}

func sealedSnapshotConfigFromEnv(getenv func(string) string) (sealedSnapshotConfig, error) {
	var cfg sealedSnapshotConfig
	switch v := getenv(envSealedFormat); v {
	case "", "v1":
	case "v2":
		cfg.chunked = true
	default:
		return cfg, fmt.Errorf("vmmd: %s=%q: want v1 or v2", envSealedFormat, v)
	}
	cfg.cacheDir = getenv(envSealedPlainCacheDir)
	mb := int64(defaultSealedPlainCacheMB)
	if raw := getenv(envSealedPlainCacheMB); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("vmmd: %s=%q: want a positive number of MiB", envSealedPlainCacheMB, raw)
		}
		mb = n
	}
	cfg.cacheBytes = mb << 20
	return cfg, nil
}

// apply wires cfg into the manager and the jailer.
func (cfg sealedSnapshotConfig) apply(mgr interface{ SetSealChunked(bool) }, jailer *fcvm.JailerVMM) error {
	mgr.SetSealChunked(cfg.chunked)
	if cfg.cacheDir == "" {
		return nil
	}
	cache, err := fcvm.NewSealedPlainCache(cfg.cacheDir, cfg.cacheBytes, 0)
	if err != nil {
		return err
	}
	jailer.WithSealedPlainCache(cache)
	return nil
}
