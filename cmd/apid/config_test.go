// Tests for apid config loading (issue #678 PR-0): defaults,
// missing file, parse errors, env overlay, NodeName round-trip,
// TLS partial-cluster rejection.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_MissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:8081" {
		t.Errorf("ListenAddr = %q, want default", cfg.ListenAddr)
	}
	// AdvisorySock + GithubdBridgeSock default to empty (env-only
	// enable, same as pre-PR-0). Non-empty defaults would auto-bind
	// the unix socket in CI where the per-daemon user doesn't exist.
	if cfg.AdvisorySock != "" {
		t.Errorf("AdvisorySock = %q, want empty (env-only enable)", cfg.AdvisorySock)
	}
	if cfg.GithubdBridgeSock != "" {
		t.Errorf("GithubdBridgeSock = %q, want empty (env-only enable)", cfg.GithubdBridgeSock)
	}
	if cfg.GithubdSocket != "/run/faas/githubd.sock" {
		t.Errorf("GithubdSocket = %q, want default", cfg.GithubdSocket)
	}
	if cfg.AppsDomain != "gregale.dev" {
		t.Errorf("AppsDomain = %q, want public default", cfg.AppsDomain)
	}
	if cfg.CLIAuthURLBase != defaultCLIAuthURLBase {
		t.Errorf("CLIAuthURLBase = %q, want public API default", cfg.CLIAuthURLBase)
	}
	if cfg.NodeName != "" {
		t.Errorf("NodeName = %q, want empty (single-box default)", cfg.NodeName)
	}
	if cfg.MetricsAddr != "" {
		t.Errorf("MetricsAddr = %q, want empty (disabled)", cfg.MetricsAddr)
	}
	// Issue #95: TLS path defaults all empty (single-box unix path).
	if cfg.AdvisoryTLSCertPath != "" || cfg.AdvisoryTLSKeyPath != "" || cfg.AdvisoryTLSCAPath != "" {
		t.Errorf("advisory TLS defaults not all empty: %+v", cfg)
	}
	if cfg.GithubdBridgeTLSCertPath != "" || cfg.GithubdBridgeTLSKeyPath != "" || cfg.GithubdBridgeTLSCAPath != "" {
		t.Errorf("githubd bridge TLS defaults not all empty: %+v", cfg)
	}
	if cfg.GithubdClientTLSCertPath != "" || cfg.GithubdClientTLSKeyPath != "" || cfg.GithubdClientTLSCAPath != "" {
		t.Errorf("githubd client TLS defaults not all empty: %+v", cfg)
	}
}

func TestLoadConfig_OverridesFromTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apid.toml")
	body := `
listen_addr = "0.0.0.0:8081"
metrics_addr = "127.0.0.1:9101"
advisory_sock = "/run/faas/apid-other.sock"
githubd_bridge_sock = "/run/faas/apid-githubd-other.sock"
githubd_socket = "/run/faas/githubd-other.sock"
apps_domain = "apps.example.com"
cli_auth_url_base = "https://api.example.com/"
db_url = "postgres:///faas?host=/run/postgresql&user=faas"
advisory_tls_cert_path = "/etc/faas/tls/apid/advisory.crt"
advisory_tls_key_path = "/etc/faas/tls/apid/advisory.key"
advisory_tls_ca_path = "/etc/faas/tls/ca.pem"
githubd_bridge_tls_cert_path = "/etc/faas/tls/apid/bridge.crt"
githubd_bridge_tls_key_path = "/etc/faas/tls/apid/bridge.key"
githubd_bridge_tls_ca_path = "/etc/faas/tls/ca.pem"
githubd_tls_cert_path = "/etc/faas/tls/apid/githubd-client.crt"
githubd_tls_key_path = "/etc/faas/tls/apid/githubd-client.key"
githubd_tls_ca_path = "/etc/faas/tls/ca.pem"
app_errors_target = "tcp://apid.faas:9093"
app_errors_tls_cert_path = "/etc/faas/tls/apid/advisory.crt"
app_errors_tls_key_path = "/etc/faas/tls/apid/advisory.key"
app_errors_tls_ca_path = "/etc/faas/tls/ca.pem"
node_name = "fsn-1-apid"
role = "control-plane"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ListenAddr != "0.0.0.0:8081" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.MetricsAddr != "127.0.0.1:9101" {
		t.Errorf("MetricsAddr = %q", cfg.MetricsAddr)
	}
	if cfg.AdvisorySock != "/run/faas/apid-other.sock" {
		t.Errorf("AdvisorySock = %q", cfg.AdvisorySock)
	}
	if cfg.GithubdBridgeSock != "/run/faas/apid-githubd-other.sock" {
		t.Errorf("GithubdBridgeSock = %q", cfg.GithubdBridgeSock)
	}
	if cfg.GithubdSocket != "/run/faas/githubd-other.sock" {
		t.Errorf("GithubdSocket = %q", cfg.GithubdSocket)
	}
	if cfg.AppsDomain != "apps.example.com" {
		t.Errorf("AppsDomain = %q", cfg.AppsDomain)
	}
	if cfg.CLIAuthURLBase != "https://api.example.com/" {
		t.Errorf("CLIAuthURLBase = %q", cfg.CLIAuthURLBase)
	}
	if cfg.DBURL != "postgres:///faas?host=/run/postgresql&user=faas" {
		t.Errorf("DBURL = %q", cfg.DBURL)
	}
	if cfg.AdvisoryTLSCertPath == "" || cfg.AdvisoryTLSKeyPath == "" || cfg.AdvisoryTLSCAPath == "" {
		t.Errorf("advisory TLS path overrides not all set: %+v", cfg)
	}
	if cfg.GithubdBridgeTLSCertPath == "" || cfg.GithubdBridgeTLSKeyPath == "" || cfg.GithubdBridgeTLSCAPath == "" {
		t.Errorf("githubd bridge TLS path overrides not all set: %+v", cfg)
	}
	if cfg.GithubdClientTLSCertPath == "" || cfg.GithubdClientTLSKeyPath == "" || cfg.GithubdClientTLSCAPath == "" {
		t.Errorf("githubd client TLS path overrides not all set: %+v", cfg)
	}
	if cfg.AppErrorsTarget != "tcp://apid.faas:9093" {
		t.Errorf("AppErrorsTarget = %q", cfg.AppErrorsTarget)
	}
	if cfg.AppErrorsTLSCertPath == "" || cfg.AppErrorsTLSKeyPath == "" || cfg.AppErrorsTLSCAPath == "" {
		t.Errorf("app errors TLS path overrides not all set: %+v", cfg)
	}
	if cfg.NodeName != "fsn-1-apid" {
		t.Errorf("NodeName = %q, want %q", cfg.NodeName, "fsn-1-apid")
	}
}

func TestLoadConfig_PartialTOMLKeepsDefaults(t *testing.T) {
	// Only override one field; the rest must stay at the defaults.
	path := filepath.Join(t.TempDir(), "partial.toml")
	body := `node_name = "fsn-1-apid"` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NodeName != "fsn-1-apid" {
		t.Errorf("NodeName = %q", cfg.NodeName)
	}
	if cfg.ListenAddr != "127.0.0.1:8081" {
		t.Errorf("ListenAddr = %q (default lost after partial unmarshal)", cfg.ListenAddr)
	}
	if cfg.AdvisorySock != "" {
		t.Errorf("AdvisorySock = %q, want empty (env-only enable)", cfg.AdvisorySock)
	}
}

func TestLoadConfig_BadTOMLErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(path, []byte("not valid toml === ==="), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error %q should mention parse failure", err.Error())
	}
}

func TestLoadConfig_ReadErrorOther(t *testing.T) {
	// A path that exists but is a directory produces a non-ENOENT read error.
	dir := t.TempDir()
	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error reading a directory")
	}
	if strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q should not be 'not found' — directory read is a real error", err.Error())
	}
}

func TestConfig_LoadAdvisoryTLS(t *testing.T) {
	// Empty cluster → (nil, nil) is the single-box back-compat path.
	c := &Config{}
	tls, err := c.LoadAdvisoryTLS()
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	// Partial cluster is rejected with the advisory_tls_* field
	// names so an operator can map the error straight to a TOML key.
	c.AdvisoryTLSCertPath = "/some/cert"
	if _, err := c.LoadAdvisoryTLS(); err == nil {
		t.Errorf("partial (cert only): expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "advisory_tls_key_path") || !strings.Contains(err.Error(), "advisory_tls_ca_path") {
		t.Errorf("err = %q, want both advisory_tls_key_path and advisory_tls_ca_path named", err.Error())
	}
}

func TestConfig_LoadGithubdBridgeTLS(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadGithubdBridgeTLS()
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	c.GithubdBridgeTLSCertPath = "/some/cert"
	if _, err := c.LoadGithubdBridgeTLS(); err == nil {
		t.Errorf("partial (cert only): expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "githubd_bridge_tls_key_path") || !strings.Contains(err.Error(), "githubd_bridge_tls_ca_path") {
		t.Errorf("err = %q, want both githubd_bridge_tls_key_path and githubd_bridge_tls_ca_path named", err.Error())
	}
}

func TestConfig_LoadGithubdTLS(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadGithubdTLS()
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	c.GithubdClientTLSCertPath = "/some/cert"
	if _, err := c.LoadGithubdTLS(); err == nil {
		t.Errorf("partial (cert only): expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "githubd_tls_key_path") || !strings.Contains(err.Error(), "githubd_tls_ca_path") {
		t.Errorf("err = %q, want both githubd_tls_key_path and githubd_tls_ca_path named", err.Error())
	}
}

func TestConfig_LoadAppErrorsTLS(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadAppErrorsTLS()
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	c.AppErrorsTLSCertPath = "/some/cert"
	if _, err := c.LoadAppErrorsTLS(); err == nil {
		t.Errorf("partial (cert only): expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "app_errors_tls_key_path") || !strings.Contains(err.Error(), "app_errors_tls_ca_path") {
		t.Errorf("err = %q, want both app_errors_tls_key_path and app_errors_tls_ca_path named", err.Error())
	}
}

func TestConfig_GetHelpersEnvOverlay(t *testing.T) {
	// Env wins over TOML for every field that has a legacy FAAS_*
	// env var. The containerised-deploys path uses env-only config
	// (no TOML file at all); the e2e harness stamps env overrides
	// to pick loopback ports without a TOML round-trip.
	c := &Config{
		ListenAddr:        "0.0.0.0:8081",
		MetricsAddr:       "127.0.0.1:9101",
		AdvisorySock:      "/run/faas/apid.sock",
		GithubdBridgeSock: "/run/faas/apid-githubd.sock",
		GithubdSocket:     "/run/faas/githubd.sock",
		AppsDomain:        "apps.toml.com",
		CLIAuthURLBase:    "https://api.toml.com/",
	}
	env := func(k string) string {
		switch k {
		case "FAAS_APID_LISTEN":
			return "127.0.0.1:8082"
		case "FAAS_APID_METRICS_ADDR":
			return "127.0.0.1:9102"
		case "FAAS_APID_ADVISORY_SOCK":
			return "/run/faas/apid-other.sock"
		case "FAAS_APID_GITHUBD_BRIDGE_SOCK":
			return "/run/faas/bridge-other.sock"
		case "FAAS_GITHUBD_SOCKET":
			return "/run/faas/gh-other.sock"
		case "FAAS_APPS_DOMAIN":
			return "apps.env.com"
		case "FAAS_CLI_AUTH_URL_BASE":
			return "api.env.com/"
		case "FAAS_APID_APP_ERRORS_TARGET":
			return "tcp://apid.env:9093"
		}
		return ""
	}
	if got := c.GetListenAddr(env); got != "127.0.0.1:8082" {
		t.Errorf("GetListenAddr (env) = %q, want env value", got)
	}
	if got := c.GetMetricsAddr(env); got != "127.0.0.1:9102" {
		t.Errorf("GetMetricsAddr (env) = %q, want env value", got)
	}
	if got := c.GetAdvisorySock(env); got != "/run/faas/apid-other.sock" {
		t.Errorf("GetAdvisorySock (env) = %q, want env value", got)
	}
	if got := c.GetGithubdBridgeSock(env); got != "/run/faas/bridge-other.sock" {
		t.Errorf("GetGithubdBridgeSock (env) = %q, want env value", got)
	}
	if got := c.GetGithubdSocket(env); got != "/run/faas/gh-other.sock" {
		t.Errorf("GetGithubdSocket (env) = %q, want env value", got)
	}
	if got := c.GetAppsDomain(env); got != "apps.env.com" {
		t.Errorf("GetAppsDomain (env) = %q, want env value", got)
	}
	if got := c.GetCLIAuthURLBase(env); got != "https://api.env.com" {
		t.Errorf("GetCLIAuthURLBase (env) = %q, want normalized env value", got)
	}
	c.AppErrorsTarget = "tcp://apid.toml:9093"
	if got := c.GetAppErrorsTarget(env); got != "tcp://apid.env:9093" {
		t.Errorf("GetAppErrorsTarget (env) = %q, want env value", got)
	}

	// Empty env → TOML value falls through.
	empty := func(string) string { return "" }
	if got := c.GetListenAddr(empty); got != "0.0.0.0:8081" {
		t.Errorf("GetListenAddr (empty env) = %q, want TOML value", got)
	}
	if got := c.GetMetricsAddr(empty); got != "127.0.0.1:9101" {
		t.Errorf("GetMetricsAddr (empty env) = %q, want TOML value", got)
	}
	if got := c.GetAppErrorsTarget(empty); got != "tcp://apid.toml:9093" {
		t.Errorf("GetAppErrorsTarget (empty env) = %q, want TOML value", got)
	}
	if got := c.GetCLIAuthURLBase(empty); got != "https://api.toml.com" {
		t.Errorf("GetCLIAuthURLBase (empty env) = %q, want normalized TOML value", got)
	}
	if got := (&Config{}).GetAppErrorsTarget(empty); got != "/run/faas/app_errors.sock" {
		t.Errorf("GetAppErrorsTarget (empty config) = %q, want legacy socket", got)
	}
}

func TestNormalizeCLIAuthURLBase(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "absolute", in: "https://api.example.com/", want: "https://api.example.com"},
		{name: "bare hostname", in: "api.example.com", want: "https://api.example.com"},
		{name: "http local", in: "http://127.0.0.1:8081///", want: "http://127.0.0.1:8081"},
		{name: "path prefix", in: "https://api.example.com/control/", want: "https://api.example.com/control"},
		{name: "query rejected", in: "https://api.example.com/?redirect=bad", want: defaultCLIAuthURLBase},
		{name: "scheme rejected", in: "ftp://api.example.com", want: defaultCLIAuthURLBase},
		{name: "empty", in: "", want: defaultCLIAuthURLBase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeCLIAuthURLBase(tt.in); got != tt.want {
				t.Errorf("normalizeCLIAuthURLBase(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestLoadConfig_RoleEnvOverlay pins the FAAS_APID_ROLE env-vs-TOML
// precedence behaviour (Gate-B). TOML value wins when set; empty
// TOML value falls through to env (RoleSingleBox fallback when env
// is also unset).
func TestLoadConfig_RoleEnvOverlay(t *testing.T) {
	// Empty TOML + empty env → RoleSingleBox.
	path := filepath.Join(t.TempDir(), "no-role.toml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if cfg.Role != "single-box" {
		t.Errorf("Role = %q, want RoleSingleBox", cfg.Role)
	}

	// TOML value wins over env (the post-decode c.Role is consulted
	// against FAAS_APID_ROLE only when the TOML field is empty —
	// see cmd/schedd/config.go pattern; here the role.FromConfig
	// call short-circuits because c.Role is already non-empty).
	path2 := filepath.Join(t.TempDir(), "with-role.toml")
	if err := os.WriteFile(path2, []byte(`role = "control-plane"`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(path2)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Role != "control-plane" {
		t.Errorf("Role = %q, want control-plane", cfg.Role)
	}
}
