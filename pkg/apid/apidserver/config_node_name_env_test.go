// Mega-PR-A (issue #911 / ADR-110 PR-1): pin the FAAS_NODE_NAME
// env overlay onto apid's Config.NodeName. The overlay must win
// over TOML (the systemd drop-in is the deploy-time source of
// truth — no operator edits the TOML on every box add) AND keep
// the field empty when the env is unset (single-box dev
// back-compat). The test is broken into its own file to keep the
// existing config_test.go focused on TOML round-trip / TLS
// helpers; the env-overlay surface is its own concern.
package apidserver_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestLoadConfig_NodeNameEnvOverlay(t *testing.T) {
	tomlPath := filepath.Join(t.TempDir(), "apid.toml")
	if err := os.WriteFile(tomlPath, []byte(`node_name = "from-toml"`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_NODE_NAME", "from-env-fsn-1")
	cfg, err := apidserver.LoadConfig(tomlPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NodeName != "from-env-fsn-1" {
		t.Errorf("NodeName (env overlay) = %q, want from-env-fsn-1", cfg.NodeName)
	}

	t.Setenv("FAAS_NODE_NAME", "")
	cfg, err = apidserver.LoadConfig(tomlPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NodeName != "from-toml" {
		t.Errorf("NodeName (empty env) = %q, want from-toml (TOML untouched)", cfg.NodeName)
	}

	t.Setenv("FAAS_NODE_NAME", "")
	cfg, err = apidserver.LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NodeName != "" {
		t.Errorf("default NodeName = %q, want empty (single-box dev)", cfg.NodeName)
	}
}
