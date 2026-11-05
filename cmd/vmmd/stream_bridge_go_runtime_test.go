package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The stream bridges' Go runtime settings default to Go's own; TOML sets
// them and FAAS_STREAM_BRIDGE_GOMAXPROCS / FAAS_STREAM_BRIDGE_GOGC override
// TOML.
func TestLoadConfig_StreamBridgeGoRuntime(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StreamBridgeGOMAXPROCS != 0 || cfg.StreamBridgeGOGC != 0 {
		t.Fatalf("defaults = GOMAXPROCS %d GOGC %d, want 0 and 0 (Go's defaults)", cfg.StreamBridgeGOMAXPROCS, cfg.StreamBridgeGOGC)
	}
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	if err := os.WriteFile(path, []byte("stream_bridge_gomaxprocs = 2\nstream_bridge_gogc = 150\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                   string
		gomaxprocsEnv, gogcEnv string
		gomaxprocs, gogc       int
	}{
		{name: "TOML", gomaxprocs: 2, gogc: 150},
		{name: "env overrides", gomaxprocsEnv: "1", gogcEnv: "200", gomaxprocs: 1, gogc: 200},
		{name: "env back to Go defaults", gomaxprocsEnv: "0", gogcEnv: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.gomaxprocsEnv != "" {
				t.Setenv("FAAS_STREAM_BRIDGE_GOMAXPROCS", tc.gomaxprocsEnv)
			}
			if tc.gogcEnv != "" {
				t.Setenv("FAAS_STREAM_BRIDGE_GOGC", tc.gogcEnv)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.StreamBridgeGOMAXPROCS != tc.gomaxprocs || cfg.StreamBridgeGOGC != tc.gogc {
				t.Fatalf("got GOMAXPROCS %d GOGC %d, want %d and %d", cfg.StreamBridgeGOMAXPROCS, cfg.StreamBridgeGOGC, tc.gomaxprocs, tc.gogc)
			}
		})
	}
	for _, tc := range []struct{ env, value string }{
		{"FAAS_STREAM_BRIDGE_GOMAXPROCS", "two"},
		{"FAAS_STREAM_BRIDGE_GOMAXPROCS", "-1"},
		{"FAAS_STREAM_BRIDGE_GOGC", "off"},
		{"FAAS_STREAM_BRIDGE_GOGC", "-5"},
	} {
		t.Run(tc.env+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.env, tc.value)
			if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), tc.env) {
				t.Fatalf("error = %v, want a validation error naming %s", err, tc.env)
			}
		})
	}
}
