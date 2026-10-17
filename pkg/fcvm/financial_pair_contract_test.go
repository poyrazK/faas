//go:build linux && metal

package fcvm

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinancialPairPeerCommandValidation(t *testing.T) {
	t.Setenv("FAAS_FINANCIAL_PAIR_BUNDLE", "/var/tmp/faas-financial-pair-"+strings.Repeat("a", 40)+"-fixture")
	for _, tc := range []struct{ target, operation, argument string }{
		{"-oProxyCommand=unexpected", "start", "instance"},
		{"peer;unexpected", "start", "instance"},
		{"user@peer", "start", "$(unexpected)"},
		{"peer", "start", "instance;unexpected"},
		{"peer", "unsupported", "instance"},
	} {
		if _, err := financialPeerCommand(context.Background(), tc.target, tc.operation, tc.argument); err == nil {
			t.Fatalf("accepted remote shell input: %+v", tc)
		}
	}
	cmd, err := financialPeerCommand(context.Background(), "user@peer", "start", "abc-123")
	if err != nil || !strings.Contains(strings.Join(cmd.Args, " "), "BatchMode=yes") {
		t.Fatalf("valid fixed peer command: %v %v", cmd, err)
	}
	t.Setenv("FAAS_FINANCIAL_PAIR_BUNDLE", "/tmp/unowned")
	if _, err := financialPeerCommand(context.Background(), "peer", "kill", "abc-123"); err == nil {
		t.Fatal("accepted an unowned remote script")
	}
}

func TestFinancialPairOwnerPathAndDatabaseGuard(t *testing.T) {
	t.Setenv("FAAS_FINANCIAL_PAIR", "1")
	root := t.TempDir()
	t.Setenv("FAAS_FINANCIAL_ACCEPTANCE_ROOT", root)
	t.Setenv("FAAS_FINANCIAL_PAIR_JAIL_ROOT", root)
	cfg := financialNativeConfig{Root: filepath.Join(root, "owned"), JailRoot: filepath.Join(root, "jail"), Database: "faas_test_clone_contract", ProxyHost: "127.0.0.1", ProxyPort: 5432}
	if err := financialPairOwnedPaths(cfg); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*financialNativeConfig){
		func(c *financialNativeConfig) { c.Root = filepath.Join(root, "..", "outside") },
		func(c *financialNativeConfig) { c.JailRoot = root },
		func(c *financialNativeConfig) { c.Database = "faas" },
		func(c *financialNativeConfig) { c.ProxyHost = "" },
		func(c *financialNativeConfig) { c.ProxyPort = 0 },
	} {
		bad := cfg
		change(&bad)
		if err := financialPairOwnedPaths(bad); err == nil {
			t.Fatalf("accepted unowned fixture: %+v", bad)
		}
	}
}

func TestFinancialPairProxyHasNoUnpartitionedFallback(t *testing.T) {
	cfg := financialNativeConfig{
		DBURL: "postgres://fixture@127.0.0.1:5432/shared?sslmode=prefer", Database: "faas_test_clone_contract",
		RuntimeParams: map[string]string{"search_path": "isolated,public"}, ProxyHost: "192.0.2.1", ProxyPort: 5433,
	}
	pool, err := financialNativePoolConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.ConnConfig.Fallbacks) == 0 {
		t.Fatal("fixture must exercise a real TLS fallback")
	}
	if pool.ConnConfig.Host != cfg.ProxyHost || pool.ConnConfig.Port != cfg.ProxyPort || pool.ConnConfig.Database != cfg.Database || pool.ConnConfig.RuntimeParams["search_path"] != "isolated,public" {
		t.Fatal("proxy configuration lost the private database/schema or transport")
	}
	for _, fallback := range pool.ConnConfig.Fallbacks {
		if fallback.Host != cfg.ProxyHost || fallback.Port != cfg.ProxyPort {
			t.Fatal("PostgreSQL fallback can bypass the native partition")
		}
	}
}
