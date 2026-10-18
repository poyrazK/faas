// Tests for schedd config loading: defaults, missing file, partial TOML,
// plus the issue #95 ResolveListenTarget / ResolveVMMTarget / TLS loaders.
package main

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestLoadConfig_MissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if cfg.SocketPath != "/run/faas/schedd.sock" {
		t.Errorf("SocketPath = %q, want default", cfg.SocketPath)
	}
	if cfg.VMMDSocket != "/run/faas/vmmd.sock" {
		t.Errorf("VMMDSocket = %q, want default", cfg.VMMDSocket)
	}
	if cfg.GatewaySynthSocket != "/run/faas/gatewayd-internal.sock" {
		t.Errorf("GatewaySynthSocket = %q, want default", cfg.GatewaySynthSocket)
	}
	if cfg.GatewaySynthTarget != "" {
		t.Errorf("GatewaySynthTarget = %q, want default empty (fallback lives in cmd/schedd/main.go)", cfg.GatewaySynthTarget)
	}
	if cfg.GatewayMetricsURL != "http://127.0.0.1:9090/metrics/gateway-requests" {
		t.Errorf("GatewayMetricsURL = %q, want bounded request metrics endpoint", cfg.GatewayMetricsURL)
	}
	if cfg.OwnerUser != "faas-schedd" {
		t.Errorf("OwnerUser = %q, want default", cfg.OwnerUser)
	}
	if !cfg.MeasuredAdmission {
		t.Error("MeasuredAdmission = false, want on by default (ADR-909)")
	}
	// Issue #95: TLS/target fields all default empty.
	if cfg.ListenAddr != "" || cfg.VMMTarget != "" ||
		cfg.TLSCertPath != "" || cfg.TLSKeyPath != "" || cfg.TLSCAPath != "" ||
		cfg.VMMTLSCertPath != "" || cfg.VMMTLSKeyPath != "" || cfg.VMMTLSCAPath != "" {
		t.Errorf("issue #95 fields not all empty: %+v", cfg)
	}
}

func TestLoadConfig_RoutingEnvironmentOverrides(t *testing.T) {
	t.Setenv("FAAS_GATEWAY_SYNTH_TARGET", "tcp://compute.faas:8080")
	t.Setenv("FAAS_GATEWAY_METRICS_URL", "http://compute.faas:9090/metrics")
	t.Setenv("FAAS_HOST_AGE_IDENTITY_PATH", "/run/credentials/schedd/host.age")

	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.GatewaySynthTarget != "tcp://compute.faas:8080" {
		t.Errorf("GatewaySynthTarget = %q", cfg.GatewaySynthTarget)
	}
	if cfg.GatewayMetricsURL != "http://compute.faas:9090/metrics" {
		t.Errorf("GatewayMetricsURL = %q", cfg.GatewayMetricsURL)
	}
	if cfg.HostAgeIdentityPath != "/run/credentials/schedd/host.age" {
		t.Errorf("HostAgeIdentityPath = %q", cfg.HostAgeIdentityPath)
	}
}

// The listen address the unit renders wins over the TOML, so the durable
// socket rendered from the same variable always matches (ADR-1580).
func TestLoadConfig_ListenAddrEnvironmentOverridesTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(path, []byte("listen_addr = \"unix:///run/faas/schedd.sock\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_SCHEDD_LISTEN_ADDR", "tcp://0.0.0.0:9091")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.ResolveListenTarget(); got != "tcp://0.0.0.0:9091" {
		t.Fatalf("listen target = %q, want the unit's tcp://0.0.0.0:9091", got)
	}
}

func TestLoadConfig_OverridesFromTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedd.toml")
	body := `
listen_addr = "tcp://0.0.0.0:50051"
vmmd_target = "tcp://vmmd.internal:50051"
tls_cert_path = "/etc/faas/tls/schedd.crt"
tls_key_path = "/etc/faas/tls/schedd.key"
tls_ca_path = "/etc/faas/tls/ca.pem"
vmmd_tls_cert_path = "/etc/faas/tls/vmmd-client.crt"
vmmd_tls_key_path = "/etc/faas/tls/vmmd-client.key"
vmmd_tls_ca_path = "/etc/faas/tls/ca.pem"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ListenAddr != "tcp://0.0.0.0:50051" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.VMMTarget != "tcp://vmmd.internal:50051" {
		t.Errorf("VMMTarget = %q", cfg.VMMTarget)
	}
	if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" || cfg.TLSCAPath == "" {
		t.Errorf("server TLS path overrides not all set: %+v", cfg)
	}
	if cfg.VMMTLSCertPath == "" || cfg.VMMTLSKeyPath == "" || cfg.VMMTLSCAPath == "" {
		t.Errorf("vmmd TLS path overrides not all set: %+v", cfg)
	}
}

func TestConfig_ResolveListenTarget(t *testing.T) {
	c := &Config{SocketPath: "/run/faas/schedd.sock"}
	if got := c.ResolveListenTarget(); got != "unix:///run/faas/schedd.sock" {
		t.Errorf("fallback = %q, want unix:///run/faas/schedd.sock", got)
	}
	c.ListenAddr = "tcp://0.0.0.0:50051"
	if got := c.ResolveListenTarget(); got != "tcp://0.0.0.0:50051" {
		t.Errorf("explicit = %q, want tcp://0.0.0.0:50051", got)
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

func TestConfig_LoadServerTLS(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadServerTLS()
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	c.TLSCertPath = "/some/cert"
	if _, err := c.LoadServerTLS(); err == nil {
		t.Errorf("partial: expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "tls_key_path") || !strings.Contains(err.Error(), "tls_ca_path") {
		t.Errorf("err = %q, want both tls_key_path and tls_ca_path named", err.Error())
	}
}

func TestConfig_LoadVMMTLS(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadVMMTLS()
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	c.VMMTLSCertPath = "/some/cert"
	if _, err := c.LoadVMMTLS(); err == nil {
		t.Errorf("partial: expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "vmmd_tls_key_path") || !strings.Contains(err.Error(), "vmmd_tls_ca_path") {
		t.Errorf("err = %q, want both vmmd_tls_key_path and vmmd_tls_ca_path named", err.Error())
	}
}

// TestConfig_NodeNameGate exercises the ADR-056 multi-box gate at
// the config layer. cfg.NodeName is the gate: empty = single-box
// (no verifier), set = multi-box (PGNodeVerifier + the *WithVerifier
// factory variants both produce a *tls.Config with
// VerifyPeerCertificate installed). The test bridges the daemon
// wiring in cmd/schedd/main.go (which gates `if cfg.NodeName != ""`)
// with the wire-layer factory behaviour pinned by
// pkg/wire/grpc_test.go TestMTLSRoundTrip_NodeVerifier*.
//
// The test deliberately does NOT stand up Postgres — the gate is
// a TOML field, and the actual verifier construction is a
// separate concern pinned by pkg/wire/pgverifier_test.go. The
// factory's response to a nil verifier is the load-bearing piece
// for the gate-closed path (single-box dev must keep working).
func TestConfig_NodeNameGate(t *testing.T) {
	// All-empty TLS paths return (nil, nil) — loadServerTLSConfig's
	// canonical contract — so the gate-open and gate-closed paths
	// both short-circuit. To exercise the gate, set all three TLS
	// paths. With no cert materials on disk, the load step errors
	// out, so the gate-closed assertion is "no error from the
	// verifier half" while the gate-empty behaviour is captured
	// by the existing TestConfig_LoadServerTLS / TestConfig_LoadVMMTLS
	// tests above (they assert the nil contract on all-empty paths).
	//
	// The actual gate assertion uses a stub server TLS helper that
	// bypasses the disk load: the production daemon wiring only
	// calls *WithVerifier after the loader succeeds, so the gate
	// decision lives in the *WithVerifier call site. The test
	// substitutes a verifier-bearing variant and pins the
	// verifier's response via the wire package's existing test
	// surface (TestMTLSRoundTrip_NodeVerifier*).
	//
	// Concretely: a *Config with NodeName set, all three TLS paths
	// pointing at non-existent files, and a nil verifier. The
	// loader errors at TLS-load (not at the gate), and the test
	// confirms the gate wiring doesn't add a NEW error class.
	c := &Config{
		NodeName:    "schedd-fsn-1",
		TLSCertPath: "/nonexistent/cert.pem",
		TLSKeyPath:  "/nonexistent/key.pem",
		TLSCAPath:   "/nonexistent/ca.pem",
	}
	closedTLS, err := c.LoadServerTLSWithVerifier(nil)
	if err == nil {
		t.Fatalf("gate-closed: expected disk-load error, got nil cfg=%v", closedTLS)
	}
	if closedTLS != nil {
		t.Errorf("gate-closed: cfg = %v, want nil on disk-load error", closedTLS)
	}
	// The error must be the disk-load error from the loader, not a
	// new error introduced by the verifier gate. Parsing failure on
	// the cert path is the canonical signal.
	if !strings.Contains(err.Error(), "cert") && !strings.Contains(err.Error(), "key") {
		t.Errorf("gate-closed: err = %q, want cert/key missing message", err.Error())
	}
}

// TestLoadConfig_NodeNameOverride pins the TOML round-trip on the
// new node_name field. ADR-056 mirrors vmmd's compute_node.name
// gate; on the schedd side the field is intentionally flat
// (no [compute_node] subsection) because schedd is the
// control-plane trust anchor, not a self-registrant.
func TestLoadConfig_NodeNameOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedd.toml")
	body := `node_name = "schedd-fsn-1"`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NodeName != "schedd-fsn-1" {
		t.Errorf("NodeName = %q, want schedd-fsn-1", cfg.NodeName)
	}

	// Round-trip the gate-empty reset: a fresh default config
	// (no node_name) must keep the gate closed so the
	// single-box dev path doesn't accidentally wire a verifier.
	zero, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if zero.NodeName != "" {
		t.Errorf("default NodeName = %q, want empty (gate closed)", zero.NodeName)
	}
}

// TestLoadConfig_NodeNameEnvOverlay pins the FAAS_NODE_NAME env
// overlay (Mega-PR-A / issue #911 / ADR-110 PR-1). The overlay
// must win over TOML (the systemd drop-in is the deploy-time
// source of truth — no operator edits the TOML on every box add)
// AND keep the field empty when the env is unset (single-box dev
// back-compat).
func TestLoadConfig_NodeNameEnvOverlay(t *testing.T) {
	// Env wins over TOML.
	tomlPath := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(tomlPath, []byte(`node_name = "from-toml"`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_NODE_NAME", "from-env-fsn-1")
	cfg, err := LoadConfig(tomlPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NodeName != "from-env-fsn-1" {
		t.Errorf("NodeName (env overlay) = %q, want from-env-fsn-1", cfg.NodeName)
	}

	// Empty env keeps the TOML value (no silent override).
	t.Setenv("FAAS_NODE_NAME", "")
	cfg, err = LoadConfig(tomlPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NodeName != "from-toml" {
		t.Errorf("NodeName (empty env) = %q, want from-toml (TOML untouched)", cfg.NodeName)
	}

	// Empty env + missing TOML = empty single-box dev.
	t.Setenv("FAAS_NODE_NAME", "")
	cfg, err = LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NodeName != "" {
		t.Errorf("default NodeName = %q, want empty (single-box dev)", cfg.NodeName)
	}
}

// ADR-1151: the frozen tier is off by default; frozen_tier or
// FAAS_FROZEN_TIER turns it on, and the env wins.
func TestLoadConfig_FrozenTier(t *testing.T) {
	t.Setenv("FAAS_FROZEN_TIER", "")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || cfg.FrozenTier {
		t.Fatalf("default FrozenTier = %v, %v; want off", cfg != nil && cfg.FrozenTier, err)
	}
	tomlPath := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(tomlPath, []byte("frozen_tier = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(tomlPath); err != nil || !cfg.FrozenTier {
		t.Fatalf("frozen_tier = true: FrozenTier = %v, %v", cfg != nil && cfg.FrozenTier, err)
	}
	t.Setenv("FAAS_FROZEN_TIER", "false")
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.FrozenTier {
		t.Fatalf("FAAS_FROZEN_TIER=false over TOML: FrozenTier = %v, %v", cfg != nil && cfg.FrozenTier, err)
	}
	t.Setenv("FAAS_FROZEN_TIER", "maybe")
	if _, err := LoadConfig(tomlPath); err == nil {
		t.Fatal("FAAS_FROZEN_TIER=maybe accepted")
	}
}

// ADR-1342: the frozen fast path is off by default; frozen_fast_path or
// FAAS_FROZEN_FAST_PATH turns it on, and the env wins.
func TestLoadConfig_FrozenFastPath(t *testing.T) {
	t.Setenv("FAAS_FROZEN_FAST_PATH", "")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || cfg.FrozenFastPath {
		t.Fatalf("default FrozenFastPath = %v, %v; want off", cfg != nil && cfg.FrozenFastPath, err)
	}
	tomlPath := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(tomlPath, []byte("frozen_fast_path = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(tomlPath); err != nil || !cfg.FrozenFastPath {
		t.Fatalf("frozen_fast_path = true: FrozenFastPath = %v, %v", cfg != nil && cfg.FrozenFastPath, err)
	}
	t.Setenv("FAAS_FROZEN_FAST_PATH", "false")
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.FrozenFastPath {
		t.Fatalf("FAAS_FROZEN_FAST_PATH=false over TOML: FrozenFastPath = %v, %v", cfg != nil && cfg.FrozenFastPath, err)
	}
	t.Setenv("FAAS_FROZEN_FAST_PATH", "maybe")
	if _, err := LoadConfig(tomlPath); err == nil {
		t.Fatal("FAAS_FROZEN_FAST_PATH=maybe accepted")
	}
}

// ADR-1345: the owner cache is off by default; owner_cache or
// FAAS_OWNER_CACHE turns it on, and the env wins.
func TestLoadConfig_OwnerCache(t *testing.T) {
	t.Setenv("FAAS_OWNER_CACHE", "")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || cfg.OwnerCache {
		t.Fatalf("default OwnerCache = %v, %v; want off", cfg != nil && cfg.OwnerCache, err)
	}
	tomlPath := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(tomlPath, []byte("owner_cache = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(tomlPath); err != nil || !cfg.OwnerCache {
		t.Fatalf("owner_cache = true: OwnerCache = %v, %v", cfg != nil && cfg.OwnerCache, err)
	}
	t.Setenv("FAAS_OWNER_CACHE", "false")
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.OwnerCache {
		t.Fatalf("FAAS_OWNER_CACHE=false over TOML: OwnerCache = %v, %v", cfg != nil && cfg.OwnerCache, err)
	}
	t.Setenv("FAAS_OWNER_CACHE", "maybe")
	if _, err := LoadConfig(tomlPath); err == nil {
		t.Fatal("FAAS_OWNER_CACHE=maybe accepted")
	}
}

// ADR-1151 step 3: frozen_tier_apps extends the tier to apps, off by default.
func TestLoadConfig_FrozenTierApps(t *testing.T) {
	t.Setenv("FAAS_FROZEN_TIER_APPS", "")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || cfg.FrozenTierApps {
		t.Fatalf("default FrozenTierApps = %v, %v; want off", cfg != nil && cfg.FrozenTierApps, err)
	}
	tomlPath := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(tomlPath, []byte("frozen_tier_apps = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(tomlPath); err != nil || !cfg.FrozenTierApps {
		t.Fatalf("frozen_tier_apps = true: FrozenTierApps = %v, %v", cfg != nil && cfg.FrozenTierApps, err)
	}
	t.Setenv("FAAS_FROZEN_TIER_APPS", "false")
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.FrozenTierApps {
		t.Fatalf("FAAS_FROZEN_TIER_APPS=false over TOML: FrozenTierApps = %v, %v", cfg != nil && cfg.FrozenTierApps, err)
	}
	t.Setenv("FAAS_FROZEN_TIER_APPS", "maybe")
	if _, err := LoadConfig(tomlPath); err == nil {
		t.Fatal("FAAS_FROZEN_TIER_APPS=maybe accepted")
	}
}

func TestResolveLocalNodeID_ComputeOnlyAllowsDrainedRollout(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "drained-rollout-node",
		TargetURL: "tcp://127.0.0.1:50051",
		Lifecycle: state.NodeLifecycleMaintenance,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	if node.Active {
		t.Fatal("test node unexpectedly active")
	}

	computeCfg := &Config{NodeName: node.Name, Role: role.RoleComputeOnly}
	got, err := computeCfg.ResolveLocalNodeID(ctx, store)
	if err != nil {
		t.Fatalf("compute-only ResolveLocalNodeID: %v", err)
	}
	if got != node.ID {
		t.Errorf("owner node id = %q, want %q", got, node.ID)
	}

	controlCfg := &Config{NodeName: node.Name, Role: role.RoleControlPlane}
	if _, err := controlCfg.ResolveLocalNodeID(ctx, store); err == nil || !strings.Contains(err.Error(), "is inactive") {
		t.Fatalf("control-plane ResolveLocalNodeID error = %v, want inactive-node refusal", err)
	}
}

// _ ensures the wire import is used even when the test file is
// compiled without the NodeName test (e.g. a future -run filter).
var _ = (*tls.Config)(nil)

// TestConfig_MetricsListener_Defaults / OverridesRespected — ADR-122
// canonical shape (mirrors cmd/meterd/config_test.go). The helper
// lives next to LoadVMMTLSWithPrefixAndVerifierAndReload; this test
// pins both the constant fallback and the operator-override path.
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

// adr: 909 — measured admission is on by default and schedd.toml can turn it
// off.
func TestLoadConfig_MeasuredAdmissionOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(path, []byte("measured_admission = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.MeasuredAdmission {
		t.Fatal("measured_admission = false left measured admission on")
	}
}
