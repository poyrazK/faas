// adr: 521 — startup cannot widen a scoped Operations preview cohort.
package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationPreviewGatewayConfiguration(t *testing.T) {
	policy := filepath.Join(t.TempDir(), "preview.json")
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(Config{
		OperationsPreviewPolicyPath: policy,
		RateLimit:                   TOMLRateLimitConfig{Mode: "central"},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gatewayd.toml")
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil || cfg.OperationsPreviewPolicyPath != policy {
		t.Fatal("preview TOML round trip", err)
	}
	handler := gateway.NewHandlerWith(nil, gateway.NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := configureOperationRoutes(handler, runDeps{config: cfg}); err == nil {
		t.Fatal("preview accepted without durable storage")
	}
	// A missing/invalid policy closes new work without preventing a daemon
	// from restarting and serving already admitted operations.
	if err := configureOperationRoutes(handler, runDeps{config: cfg, pgStore: state.NewPgStore(nil)}); err != nil {
		t.Fatal("missing policy stopped retained work", err)
	}
	if err := os.WriteFile(policy, []byte(`{"version":1,"enabled":`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := configureOperationRoutes(handler, runDeps{config: cfg, pgStore: state.NewPgStore(nil)}); err != nil {
		t.Fatal("malformed policy stopped retained work", err)
	}
	if err := configureOperationRoutes(handler, runDeps{}); err != nil {
		t.Fatal(err)
	}
	if err := configureOperationRoutes(handler, runDeps{config: &Config{OperationsPreviewPolicyPath: "relative.json"}, pgStore: state.NewPgStore(nil)}); err == nil {
		t.Fatal("relative preview policy path accepted")
	}
}
