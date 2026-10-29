package main

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealsig"
)

// runtimeSecretSignatureChecker builds the checker that makes vmmd verify
// apid's writer signature on every runtime secret before it opens one (ADR
// 20261028161502, amendment 1); nil when no verify key is configured. The
// signatures live in the control-plane database, so a configured verify key
// needs the compute-node database configuration.
func runtimeSecretSignatureChecker(cfg *Config, store any, reg prometheus.Registerer, log *slog.Logger) (*sealsig.RuntimeChecker, error) {
	if cfg.RuntimeSecretSignatureKeysFile == "" {
		sealdomain.RegisterSignatureEnforcement(reg, false, false)
		return nil, nil
	}
	keys, err := sealsig.LoadVerifyKeys(cfg.RuntimeSecretSignatureKeysFile)
	if err != nil {
		return nil, fmt.Errorf("vmmd: runtime secret signature keys: %w", err)
	}
	lookup, ok := store.(sealsig.RuntimeLookup)
	if !ok {
		return nil, errors.New("vmmd: runtime secret signatures need the compute-node database configuration ([db_url] or FAAS_VMMD_DBURL)")
	}
	checker, err := sealsig.NewRuntimeChecker(lookup, sealsig.NewVerifier(keys, cfg.RequireRuntimeSecretSignatures), log)
	if err != nil {
		return nil, err
	}
	sealdomain.RegisterSignatureEnforcement(reg, true, cfg.RequireRuntimeSecretSignatures)
	log.Info("vmmd: runtime secret signatures checked (ADR 20261028161502)", "keys", len(keys), "required", cfg.RequireRuntimeSecretSignatures)
	return checker, nil
}
