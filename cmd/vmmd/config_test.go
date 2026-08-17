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

// TestLoadConfig_DefaultVCPUBudget pins the issue #938 / PR-A wiring
// between cmd/vmmd/config.go and pkg/api.VCPUSlots. With no TOML
// override, the default must match the migration 00123 backfill value
// so single-box dev never trips the CHECK constraint.
func TestLoadConfig_DefaultVCPUBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ComputeNode.VCPUBudget != api.VCPUSlots {
		t.Errorf("ComputeNode.VCPUBudget = %d, want %d (api.VCPUSlots)",
			cfg.ComputeNode.VCPUBudget, api.VCPUSlots)
	}
}

// TestLoadConfig_VCPUBudgetOverride: a per-host [compute_node].vcpu_budget
// flows through verbatim to ComputeNodeConfig.VCPUBudget.
func TestLoadConfig_VCPUBudgetOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	body := `
[compute_node]
vcpu_budget = 40
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ComputeNode.VCPUBudget != 40 {
		t.Errorf("ComputeNode.VCPUBudget = %d, want 40 (operator override)", cfg.ComputeNode.VCPUBudget)
	}
}

// TestLoadConfig_RejectsNonPositiveVCPUBudget: non-positive values
// must fail at LoadConfig so the migration 00123 CHECK
// (vcpu_budget > 0) cannot trip the self-registration upsert.
func TestLoadConfig_RejectsNonPositiveVCPUBudget(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"zero", `
[compute_node]
vcpu_budget = 0
`},
		{"negative", `
[compute_node]
vcpu_budget = -1
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "vmmd.toml")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatalf("expected non-positive vcpu_budget to be rejected")
			}
			if !strings.Contains(err.Error(), "vcpu_budget") {
				t.Errorf("error %q should name vcpu_budget", err.Error())
			}
		})
	}
}

// TestLoadConfig_FAASVCPUBudgetOverride pins the env-overlay seam
// (issue #938 / PR-A): FAAS_VCPU_BUDGET wins over the TOML value when
// both are set, mirroring the FAAS_NODE_NAME / FAAS_HOST_BRIDGE_CIDR
// pattern. Empty keeps the TOML value.
func TestLoadConfig_FAASVCPUBudgetOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	body := `
[compute_node]
vcpu_budget = 40
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_VCPU_BUDGET", "80")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ComputeNode.VCPUBudget != 80 {
		t.Errorf("ComputeNode.VCPUBudget = %d, want 80 (env override)", cfg.ComputeNode.VCPUBudget)
	}
}

// TestLoadConfig_FAASVCPUBudgetRejectsNonPositive pins the env-overlay
// validator (issue #938 / PR-A): non-positive FAAS_VCPU_BUDGET fails
// at LoadConfig rather than at the upsert.
func TestLoadConfig_FAASVCPUBudgetRejectsNonPositive(t *testing.T) {
	t.Setenv("FAAS_VCPU_BUDGET", "0")
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected non-positive FAAS_VCPU_BUDGET to be rejected")
	}
	if !strings.Contains(err.Error(), "FAAS_VCPU_BUDGET") {
		t.Errorf("error %q should name FAAS_VCPU_BUDGET", err.Error())
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

// TestTargetURLIsWildcardDial is the unit-level pin for the issue #900
// raw-string detector. Every shape the operator might paste into
// [compute_node].target_url is exhaustively covered: IPv4 wildcard, IPv6
// wildcard, routable IP, FQDN, unix://, dns://, and parse failures. The
// (wildcard, err) tuple is asserted directly so a future refactor that
// over-rejects (treats FQDN as wildcard) or under-rejects (accepts
// 0.0.0.0) surfaces here.
func TestTargetURLIsWildcardDial(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantWild   bool
		wantErrSub string // empty = no error expected
	}{
		{"ipv4-0.0.0.0 wildcard", "tcp://0.0.0.0:50051", true, ""},
		{"ipv6-:: wildcard", "tcp://[::]:50051", true, ""},
		{"ipv4-routable accepted", "tcp://100.64.0.1:50051", false, ""},
		{"ipv4-routable-192.168 accepted", "tcp://192.168.1.10:50051", false, ""},
		{"fqdn accepted", "tcp://vmmd-2.faas:50051", false, ""},
		{"fqdn-with-dots accepted", "tcp://vmmd.us-east-1.faas:50051", false, ""},
		{"unix-scheme accepted", "unix:///run/faas/vmmd.sock", false, ""},
		{"dns-scheme accepted", "dns:///vmmd-2.faas:50051", false, ""},
		{"bogus-scheme parse error", "bogus://nope", false, "parse"},
		{"empty-host-parse error", "tcp://", false, "parse"},
		{"missing-port-parse error", "tcp://0.0.0.0", false, "parse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wildcard, err := targetURLIsWildcardDial(tc.raw)
			if tc.wantErrSub != "" {
				if err == nil {
					t.Fatalf("targetURLIsWildcardDial(%q) = nil err, want containing %q", tc.raw, tc.wantErrSub)
				}
				if !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("targetURLIsWildcardDial(%q) err = %q, want containing %q", tc.raw, err, tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("targetURLIsWildcardDial(%q) unexpected error: %v", tc.raw, err)
			}
			if wildcard != tc.wantWild {
				t.Errorf("targetURLIsWildcardDial(%q) wildcard = %v, want %v", tc.raw, wildcard, tc.wantWild)
			}
		})
	}
}

// TestLoadConfig_RejectsWildcardTargetURL pins the load-time gate for
// issue #900. The accepted shapes (FQDN, routable IP, unix://, dns://,
// empty) load without error; the wildcard bind forms (0.0.0.0, ::) are
// rejected with the canonical "bind form" message that names the
// offending field for operator grep. The gate fires regardless of
// whether overlay_ip is also set — the explicit override is the
// load-bearing path and the fallback chain is not in scope here.
func TestLoadConfig_RejectsWildcardTargetURL(t *testing.T) {
	reject := []struct {
		name string
		toml string
	}{
		{
			name: "ipv4 wildcard",
			toml: `
[compute_node]
name = "vmmd-fsn-2"
target_url = "tcp://0.0.0.0:50051"
`,
		},
		{
			name: "ipv6 wildcard",
			toml: `
[compute_node]
name = "vmmd-fsn-2"
target_url = "tcp://[::]:50051"
`,
		},
		{
			name: "ipv4 wildcard even with overlay_ip set",
			toml: `
[compute_node]
name = "vmmd-fsn-2"
target_url = "tcp://0.0.0.0:50051"
overlay_ip = "100.64.0.1"
`,
		},
		{
			name: "ipv6 wildcard even with overlay_ip set",
			toml: `
[compute_node]
name = "vmmd-fsn-2"
target_url = "tcp://[::]:50051"
overlay_ip = "100.64.0.1"
`,
		},
	}
	for _, tc := range reject {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "vmmd.toml")
			if err := os.WriteFile(path, []byte(tc.toml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatalf("LoadConfig must reject wildcard target_url; got nil")
			}
			if !strings.Contains(err.Error(), "target_url") {
				t.Errorf("error %q must name target_url for operator grep", err.Error())
			}
			if !strings.Contains(err.Error(), "bind form") {
				t.Errorf("error %q must indicate bind form (4-line operator guidance)", err.Error())
			}
			// Don't pin the doc URL — the load-time path only
			// attaches it when the full reproduction path is
			// available; the LoadConfig error is the canonical
			// operator-facing shape.
		})
	}
}

// TestLoadConfig_AcceptsRoutableTargetURL pins the inverse: the
// issue #900 wildcard gate is narrowly scoped to bind forms. Operators
// on a multi-box fleet set [compute_node].target_url to a routable
// FQDN or IP; that path is accepted. Default-empty target_url
// (single-box dev) is also accepted (covered by
// TestLoadConfig_MissingFileReturnsDefaults).
func TestLoadConfig_AcceptsRoutableTargetURL(t *testing.T) {
	accept := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "fqdn",
			toml: `
[compute_node]
name = "vmmd-fsn-2"
target_url = "tcp://vmmd-2.faas:50051"
`,
			want: "tcp://vmmd-2.faas:50051",
		},
		{
			name: "routable ipv4",
			toml: `
[compute_node]
name = "vmmd-fsn-2"
target_url = "tcp://100.64.0.1:50051"
`,
			want: "tcp://100.64.0.1:50051",
		},
		{
			name: "unix-scheme",
			toml: `
[compute_node]
name = "vmmd-fsn-2"
target_url = "unix:///run/faas/vmmd.sock"
`,
			want: "unix:///run/faas/vmmd.sock",
		},
		{
			name: "dns-scheme",
			toml: `
[compute_node]
name = "vmmd-fsn-2"
target_url = "dns:///vmmd-2.faas:50051"
`,
			want: "dns:///vmmd-2.faas:50051",
		},
	}
	for _, tc := range accept {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "vmmd.toml")
			if err := os.WriteFile(path, []byte(tc.toml), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig must accept %q; got %v", tc.want, err)
			}
			if cfg.ComputeNode.TargetURL != tc.want {
				t.Errorf("TargetURL = %q, want %q", cfg.ComputeNode.TargetURL, tc.want)
			}
		})
	}
}
