package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// loadWorkloadIdentitySigner reads the operator-managed RSA key. An empty key
// path intentionally disables federation while leaving the rest of vmmd
// usable; this supports a rolling upgrade where guest-init has the endpoint
// before every node has received the shared issuer key.
func loadWorkloadIdentitySigner(configOpt ...*Config) (*workloadidentity.Signer, error) {
	var cfg Config
	if len(configOpt) > 0 && configOpt[0] != nil {
		cfg = *configOpt[0]
	}
	path := strings.TrimSpace(cfg.WorkloadIdentityKeyPath)
	if envPath := strings.TrimSpace(os.Getenv("FAAS_WORKLOAD_IDENTITY_KEY_PATH")); envPath != "" {
		path = envPath
	}
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workload identity key %s: %w", path, err)
	}
	key, err := workloadidentity.ParseRSAPrivateKeyPEM(data)
	if err != nil {
		return nil, err
	}
	issuer := strings.TrimSpace(cfg.WorkloadIdentityIssuer)
	if envIssuer := strings.TrimSpace(os.Getenv("FAAS_WORKLOAD_IDENTITY_ISSUER")); envIssuer != "" {
		issuer = envIssuer
	}
	if issuer == "" {
		issuer = workloadidentity.DefaultIssuer
	}
	keyID := strings.TrimSpace(cfg.WorkloadIdentityKeyID)
	if envKeyID := strings.TrimSpace(os.Getenv("FAAS_WORKLOAD_IDENTITY_KEY_ID")); envKeyID != "" {
		keyID = envKeyID
	}
	ttl := workloadidentity.DefaultTokenTTL
	if cfg.WorkloadIdentityTTL > 0 {
		ttl = cfg.WorkloadIdentityTTL
		if ttl < 30*time.Second || ttl > time.Hour {
			return nil, fmt.Errorf("workload identity: workload_identity_ttl must be 30s..1h")
		}
	}
	if raw := strings.TrimSpace(os.Getenv("FAAS_WORKLOAD_IDENTITY_TTL_SECONDS")); raw != "" {
		seconds, parseErr := strconv.Atoi(raw)
		if parseErr != nil || seconds < 30 || seconds > 3600 {
			return nil, fmt.Errorf("workload identity: FAAS_WORKLOAD_IDENTITY_TTL_SECONDS must be 30..3600")
		}
		ttl = time.Duration(seconds) * time.Second
	}
	return workloadidentity.NewSigner(key, issuer, keyID, ttl)
}
