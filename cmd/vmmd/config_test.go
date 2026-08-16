// Tests for vmmd config loading: defaults, missing file, parse errors.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestLoadConfig_MissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if cfg.SocketPath != "/run/faas/vmmd.sock" {
		t.Errorf("SocketPath = %q, want default", cfg.SocketPath)
	}
	if cfg.KernelPath != "/srv/fc/base/vmlinux-6.1" {
		t.Errorf("KernelPath = %q, want default", cfg.KernelPath)
	}
	if cfg.OwnerUser != "faas-vmmd" {
		t.Errorf("OwnerUser = %q, want default", cfg.OwnerUser)
	}
	if cfg.MetricsAddr != "" {
		t.Errorf("MetricsAddr = %q, want empty (disabled)", cfg.MetricsAddr)
	}
	// Issue #95: server-mTLS paths default empty.
	if cfg.ListenAddr != "" || cfg.TLSCertPath != "" || cfg.TLSKeyPath != "" || cfg.TLSCAPath != "" {
		t.Errorf("TLS/listen defaults not all empty: %+v", cfg)
	}
	// PR scale-out readiness #4: the compute-node default
	// AdmissionCeilingMB must route through api.DefaultComputeNodeCeilingMB
	// so the vmmd default and the MemStore seed share a single source
	// of truth. This is the load-bearing assertion that proves
	// cmd/vmmd/config.go is wired to the helper (changing fixtures in
	// other tests wouldn't catch a literal regression here).
	if got := cfg.ComputeNode.AdmissionCeilingMB; got != api.DefaultComputeNodeCeilingMB() {
		t.Errorf("ComputeNode.AdmissionCeilingMB = %d, want %d (api.DefaultComputeNodeCeilingMB())",
			got, api.DefaultComputeNodeCeilingMB())
	}
}

func TestLoadConfig_OverridesFromTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	body := `
socket_path = "/run/faas/other.sock"
listen_addr = "tcp://0.0.0.0:50051"
tls_cert_path = "/etc/faas/tls/vmmd.crt"
tls_key_path = "/etc/faas/tls/vmmd.key"
tls_ca_path = "/etc/faas/tls/ca.pem"
metrics_addr = "127.0.0.1:9090"
owner_user = "vmmd-other"
kernel_path = "/srv/fc/alt/vmlinux"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.SocketPath != "/run/faas/other.sock" {
		t.Errorf("SocketPath = %q", cfg.SocketPath)
	}
	if cfg.ListenAddr != "tcp://0.0.0.0:50051" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" || cfg.TLSCAPath == "" {
		t.Errorf("TLS path overrides not all set: %+v", cfg)
	}
	if cfg.MetricsAddr != "127.0.0.1:9090" {
		t.Errorf("MetricsAddr = %q", cfg.MetricsAddr)
	}
	if cfg.OwnerUser != "vmmd-other" {
		t.Errorf("OwnerUser = %q", cfg.OwnerUser)
	}
	if cfg.KernelPath != "/srv/fc/alt/vmlinux" {
		t.Errorf("KernelPath = %q", cfg.KernelPath)
	}
	// node_key_path override (ADR-053). The default (empty string)
	// is the "use the env var or canonical default" sentinel;
	// setting it explicitly means "load from THIS path".
	// The Override body above doesn't include the key, so this
	// stays empty — see TestLoadConfig_NodeKeyPathOverride below
	// for the positive case.
	if cfg.NodeKeyPath != "" {
		t.Errorf("NodeKeyPath = %q, want empty (not in toml body)", cfg.NodeKeyPath)
	}
}

// TestLoadConfig_NodeKeyPathOverride pins the wiring between
// vmmd.toml and loadNodeSigningKey (ADR-053). Without this seam,
// operators using a non-canonical install path (air-gapped fleet,
// tmpdir in CI, read-only PKI mount) would have to set
// FAAS_VMMD_NODE_KEY_PATH per-process instead of declaratively in
// the toml. The canonical install leaves NodeKeyPath empty; only
// override-bearing fixtures set it.
func TestLoadConfig_NodeKeyPathOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	body := `node_key_path = "/etc/faas/secrets/vmmd/node.key"` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NodeKeyPath != "/etc/faas/secrets/vmmd/node.key" {
		t.Errorf("NodeKeyPath = %q, want %q",
			cfg.NodeKeyPath, "/etc/faas/secrets/vmmd/node.key")
	}
}

func TestLoadConfig_PartialTOMLKeepsDefaults(t *testing.T) {
	// Only override one field; the rest must stay at the defaults.
	path := filepath.Join(t.TempDir(), "partial.toml")
	body := `metrics_addr = "127.0.0.1:9090"` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.MetricsAddr != "127.0.0.1:9090" {
		t.Errorf("MetricsAddr = %q", cfg.MetricsAddr)
	}
	if cfg.SocketPath != "/run/faas/vmmd.sock" {
		t.Errorf("SocketPath = %q (default lost after partial unmarshal)", cfg.SocketPath)
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

// TestLoadConfig_RejectsOverlayCIDRInsideDenySet pins the M3
// load-time validator: an [compute_node].overlay_cidr that's a
// subset of any §11 deny entry (RFC1918 / CGNAT / link-local) MUST
// fail at LoadConfig, NOT at the first EgressPolicyChanged reload
// where the renderer would panic. The error must name BOTH the
// offending overlay CIDR and the swallowing deny entry so the
// operator can fix it without re-reading spec §11.
func TestLoadConfig_RejectsOverlayCIDRInsideDenySet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	toml := []byte(`
[compute_node]
name = "vmmd-fsn-2"
overlay_cidr = "10.0.0.0/16"  # subset of §11 10.0.0.0/8 deny
`)
	if err := os.WriteFile(path, toml, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig must reject overlay_cidr inside §11 deny set; got nil")
	}
	if !strings.Contains(err.Error(), "overlay_cidr") {
		t.Errorf("error %q must name the offending field for operator grep", err.Error())
	}
	if !strings.Contains(err.Error(), "10.0.0.0/16") {
		t.Errorf("error %q must name the offending overlay CIDR", err.Error())
	}
	if !strings.Contains(err.Error(), "10.0.0.0/8") {
		t.Errorf("error %q must name the swallowing deny entry", err.Error())
	}
}

// TestLoadConfig_AcceptsOverlayCIDROutsideDenySet pins the happy
// path: a public-range overlay (TEST-NET-3 203.0.113.0/24) loads
// without error. Default-empty overlay_cidr (single-host dev) is
// also accepted (covered by TestLoadConfig_OverridesFromTOML).
func TestLoadConfig_AcceptsOverlayCIDROutsideDenySet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	toml := []byte(`
[compute_node]
name = "vmmd-fsn-2"
overlay_cidr = "203.0.113.0/24"  # TEST-NET-3; outside §11 deny set
`)
	if err := os.WriteFile(path, toml, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig must accept public-range overlay CIDR; got %v", err)
	}
	if cfg.ComputeNode.OverlayCIDR != "203.0.113.0/24" {
		t.Errorf("overlay_cidr not loaded: got %q", cfg.ComputeNode.OverlayCIDR)
	}
}

// Issue #95: ResolveListenTarget prefers listen_addr, falls back to
// unix://+socket_path. The fallback must remain unchanged for
// single-box deployments.
func TestConfig_ResolveListenTarget(t *testing.T) {
	c := &Config{SocketPath: "/run/faas/vmmd.sock"}
	if got := c.ResolveListenTarget(); got != "unix:///run/faas/vmmd.sock" {
		t.Errorf("fallback = %q, want unix:///run/faas/vmmd.sock", got)
	}
	c.ListenAddr = "tcp://0.0.0.0:50051"
	if got := c.ResolveListenTarget(); got != "tcp://0.0.0.0:50051" {
		t.Errorf("explicit = %q, want tcp://0.0.0.0:50051", got)
	}
}

// Issue #95: LoadServerTLS rejects partial cluster — the wire helper
// names the missing fields. Empty config returns (nil, nil) and is the
// single-box path.
func TestConfig_LoadServerTLS(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadServerTLS()
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	c.TLSCertPath = "/some/cert"
	if _, err := c.LoadServerTLS(); err == nil {
		t.Errorf("partial (cert only): expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "tls_key_path") || !strings.Contains(err.Error(), "tls_ca_path") {
		t.Errorf("err = %q, want both tls_key_path and tls_ca_path named", err.Error())
	}
}

// TestValidateHostBridgeCIDR covers the Gap #3 wiring gate. The
// canonical use case (an operator setting a per-host RFC1918 /16 —
// the default is 10.100.0.0/16) MUST pass: the deny catalog is for
// tenant egress, not host infrastructure. The two narrow rejection
// paths remain: non-/16 prefix length and non-network-form input.
func TestValidateHostBridgeCIDR(t *testing.T) {
	cases := []struct {
		name      string
		cidr      string
		wantErr   bool
		wantConta string // substring expected in the error message
	}{
		{
			name:    "canonical RFC1918 default passes",
			cidr:    "10.100.0.0/16",
			wantErr: false,
		},
		{
			name:    "operator-chosen 10.42.0.0/16 passes",
			cidr:    "10.42.0.0/16",
			wantErr: false,
		},
		{
			name:    "172.16.0.0/16 passes",
			cidr:    "172.16.0.0/16",
			wantErr: false,
		},
		{
			name:    "192.168.0.0/16 passes",
			cidr:    "192.168.0.0/16",
			wantErr: false,
		},
		{
			name:    "100.64.0.0/16 (CGN) passes",
			cidr:    "100.64.0.0/16",
			wantErr: false,
		},
		{
			name:      "non-/16 rejected",
			cidr:      "10.100.0.0/24",
			wantErr:   true,
			wantConta: "prefix length must be /16",
		},
		{
			name:      "non-network-form rejected (host bits set)",
			cidr:      "10.42.0.5/16",
			wantErr:   true,
			wantConta: "not in network form",
		},
		{
			name:      "non-network-form rejected (host bits set, different position)",
			cidr:      "10.100.0.255/16",
			wantErr:   true,
			wantConta: "not in network form",
		},
		{
			name:      "unparseable CIDR rejected",
			cidr:      "not-a-cidr",
			wantErr:   true,
			wantConta: "parse",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateHostBridgeCIDR(tc.cidr)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("validateHostBridgeCIDR(%q) = nil, want error containing %q", tc.cidr, tc.wantConta)
				}
				if !strings.Contains(err.Error(), tc.wantConta) {
					t.Errorf("validateHostBridgeCIDR(%q) error %q does not contain %q", tc.cidr, err, tc.wantConta)
				}
				return
			}
			if err != nil {
				t.Errorf("validateHostBridgeCIDR(%q) = %v, want nil", tc.cidr, err)
			}
		})
	}
}
