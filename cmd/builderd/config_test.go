// Tests for builderd config loading + issue #95 ResolveVMMTarget / TLS loader.
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestLoadConfig_MissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if cfg.VMMDSocket != "/run/faas/vmmd.sock" {
		t.Errorf("VMMDSocket = %q, want default", cfg.VMMDSocket)
	}
	if cfg.CacheDir != "/var/cache/faas/builds" {
		t.Errorf("CacheDir = %q, want default", cfg.CacheDir)
	}
	wantBase := "/srv/fc/base/runner-builder-" + runtime.GOARCH + ".ext4"
	if cfg.BuilderBase != wantBase {
		t.Errorf("BuilderBase = %q, want default", cfg.BuilderBase)
	}
	if cfg.BuildDriveDir != "/srv/fc/builder/drive" {
		t.Errorf("BuildDriveDir = %q, want split-box staging default", cfg.BuildDriveDir)
	}
	if cfg.BuildExportDir != "/srv/fc/builder/out" {
		t.Errorf("BuildExportDir = %q, want split-box staging default", cfg.BuildExportDir)
	}
	if cfg.SourceMaxAge != 24*time.Hour || cfg.SourceGCSweepInterval != 24*time.Hour {
		t.Errorf("source retention defaults = (%v, %v), want (24h, 24h)", cfg.SourceMaxAge, cfg.SourceGCSweepInterval)
	}
	if cfg.MetricsAddr != "127.0.0.1:9105" {
		t.Errorf("MetricsAddr = %q, want canonical loopback metrics default", cfg.MetricsAddr)
	}
	// Issue #95 fields default empty.
	if cfg.VMMTarget != "" ||
		cfg.TLSCertPath != "" || cfg.TLSKeyPath != "" || cfg.TLSCAPath != "" {
		t.Errorf("issue #95 fields not all empty: %+v", cfg)
	}
}

func TestConfig_ResolveVMMTarget(t *testing.T) {
	c := &Config{VMMDSocket: "/run/faas/vmmd.sock"}
	if got := c.ResolveVMMTarget(); got != "unix:///run/faas/vmmd.sock" {
		t.Errorf("fallback = %q, want unix:///run/faas/vmmd.sock", got)
	}
	c.VMMTarget = "tcp://vmmd.internal:50051"
	if got := c.ResolveVMMTarget(); got != "tcp://vmmd.internal:50051" {
		t.Errorf("explicit = %q, want tcp://vmmd.internal:50051", got)
	}
}

func TestConfig_LoadVMMTLS(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadVMMTLS()
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	c.TLSCertPath = "/some/cert"
	if _, err := c.LoadVMMTLS(); err == nil {
		t.Errorf("partial: expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "tls_key_path") || !strings.Contains(err.Error(), "tls_ca_path") {
		t.Errorf("err = %q, want both tls_key_path and tls_ca_path named", err.Error())
	}
}

func TestLoadConfig_OverridesFromTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "builderd.toml")
	body := `
vmmd_target = "tcp://vmmd.internal:50051"
tls_cert_path = "/etc/faas/tls/builderd.crt"
tls_key_path = "/etc/faas/tls/builderd.key"
tls_ca_path = "/etc/faas/tls/ca.pem"
cache_dir = "/var/cache/faas/builds-test"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.VMMTarget != "tcp://vmmd.internal:50051" {
		t.Errorf("VMMTarget = %q", cfg.VMMTarget)
	}
	if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" || cfg.TLSCAPath == "" {
		t.Errorf("TLS path overrides not all set: %+v", cfg)
	}
	if cfg.CacheDir != "/var/cache/faas/builds-test" {
		t.Errorf("CacheDir = %q", cfg.CacheDir)
	}
}

// TestConfig_MetricsListener_Defaults / OverridesRespected — ADR-122
// canonical shape (mirrors cmd/meterd/config_test.go).
func TestConfig_MetricsListener_Defaults(t *testing.T) {
	c := &Config{}
	read, write, idle, mhb := c.MetricsListener()
	if read != time.Duration(api.MetricsReadTimeoutSecondsDefault)*time.Second {
		t.Errorf("read=%v want %v", read, api.MetricsReadTimeoutSecondsDefault)
	}
	if write != time.Duration(api.MetricsWriteTimeoutSecondsDefault)*time.Second {
		t.Errorf("write=%v want %v", write, api.MetricsWriteTimeoutSecondsDefault)
	}
	if idle != time.Duration(api.MetricsIdleTimeoutSecondsDefault)*time.Second {
		t.Errorf("idle=%v want %v", idle, api.MetricsIdleTimeoutSecondsDefault)
	}
	if mhb != api.DefaultMaxHeaderBytes {
		t.Errorf("mhb=%v want %v", mhb, api.DefaultMaxHeaderBytes)
	}
}

func TestConfig_MetricsListener_OverridesRespected(t *testing.T) {
	c := &Config{
		MetricsReadTimeout:    30 * time.Second,
		MetricsWriteTimeout:   45 * time.Second,
		MetricsIdleTimeout:    120 * time.Second,
		MetricsMaxHeaderBytes: 4 << 20,
	}
	read, write, idle, mhb := c.MetricsListener()
	if read != 30*time.Second || write != 45*time.Second || idle != 120*time.Second || mhb != int64(4<<20) {
		t.Errorf("override lost: read=%v write=%v idle=%v mhb=%v", read, write, idle, mhb)
	}
}
