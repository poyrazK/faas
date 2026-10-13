// adr: 1027
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "pgproxyd.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	full := `host_suffix = "db.gregale.dev"
tls_cert_path = "/c"
tls_key_path = "/k"
decoy_secret_path = "/d"
db_url_path = "/u"
`
	cfg, err := LoadConfig(write(full))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":5432" || cfg.ScheddTarget != "unix:///run/faas/schedd.sock" || cfg.VerifierCacheTTL != 30*time.Second || cfg.OpsAddr != DefaultOpsAddr {
		t.Fatalf("defaults = %+v", cfg)
	}
	if _, err := LoadConfig(write(`tls_cert_path = "/c"`)); err == nil || !strings.Contains(err.Error(), "host_suffix") {
		t.Fatalf("missing fields = %v", err)
	}
	if _, err := LoadConfig(write(full + "verifier_cache_ttl = \"1h\"\n")); err == nil {
		t.Fatal("an hour-long verifier cache was accepted")
	}
}

func TestReadSecretRefusesSharedFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "decoy")
	if err := os.WriteFile(p, []byte(strings.Repeat("s", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecret(p, 32); err == nil {
		t.Fatal("world-readable secret accepted")
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readSecret(p, 32); err != nil || len(got) != 40 {
		t.Fatalf("secret = %d bytes, %v", len(got), err)
	}
	if _, err := readSecret(p, 64); err == nil {
		t.Fatal("short secret accepted")
	}
}
