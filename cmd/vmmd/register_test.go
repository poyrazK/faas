// Tests for registerComputeNode (issue #98 / ADR-028). The happy
// path covers upsert + re-upsert idempotency; the failure paths
// pin the validation contract (zero values are rejected) and the
// "operator opted out" path (empty NodeName = no DB needed).

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRegisterComputeNode_HappyPath(t *testing.T) {
	st := state.NewMemStore()
	cfg := ComputeNodeConfig{
		NodeName:           "box-east-1",
		VPCPUs:             160,
		MemMB:              56000,
		MaxConcurrency:     200,
		AdmissionCeilingMB: 47600,
	}
	got, err := registerComputeNode(context.Background(), st, cfg, "unix:///run/faas/vmmd.sock",
		func(context.Context) (string, error) { return "", nil }, testLogger())
	if err != nil {
		t.Fatalf("registerComputeNode: %v", err)
	}
	if got.Name != "box-east-1" {
		t.Errorf("name = %q", got.Name)
	}
	if got.ID == "" {
		t.Error("id empty")
	}
	if !got.Active {
		t.Error("not active after registration")
	}
	if got.TargetURL != "unix:///run/faas/vmmd.sock" {
		t.Errorf("target_url = %q", got.TargetURL)
	}
}

// TestRegisterComputeNode_Idempotent: a second call with the same
// name returns the same id (upsert, not insert). This is the
// "vmmd reboots and schedd still knows me" path.
func TestRegisterComputeNode_Idempotent(t *testing.T) {
	st := state.NewMemStore()
	cfg := ComputeNodeConfig{
		NodeName: "box-east-1",
		VPCPUs:   160, MemMB: 56000,
		MaxConcurrency: 200, AdmissionCeilingMB: 47600,
	}
	first, err := registerComputeNode(context.Background(), st, cfg, "unix:///x", nil, testLogger())
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := registerComputeNode(context.Background(), st, cfg, "unix:///x", nil, testLogger())
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("id changed across upsert: %q -> %q", first.ID, second.ID)
	}
	if second.Active != true {
		t.Error("upsert did not re-activate")
	}
}

// TestRegisterComputeNode_EmptyNameSkips: the legacy default-local
// path. No DB calls; no error. This is what tests / single-box dev
// rely on.
func TestRegisterComputeNode_EmptyNameSkips(t *testing.T) {
	st := state.NewMemStore()
	got, err := registerComputeNode(context.Background(), st,
		ComputeNodeConfig{}, "unix:///x", nil, testLogger())
	if err != nil {
		t.Fatalf("empty name: %v", err)
	}
	if got.Name != "" {
		t.Errorf("empty-name path returned a row: %+v", got)
	}
}

// TestRegisterComputeNode_RejectsZeroFields: any zero-valued resource
// number is a config bug; vmmd must fail fast at startup rather than
// register a node with bogus capacity.
func TestRegisterComputeNode_RejectsZeroFields(t *testing.T) {
	st := state.NewMemStore()
	cases := []ComputeNodeConfig{
		{NodeName: "x", VPCPUs: 0, MemMB: 1, MaxConcurrency: 1, AdmissionCeilingMB: 1},
		{NodeName: "x", VPCPUs: 1, MemMB: 0, MaxConcurrency: 1, AdmissionCeilingMB: 1},
		{NodeName: "x", VPCPUs: 1, MemMB: 1, MaxConcurrency: 0, AdmissionCeilingMB: 1},
		{NodeName: "x", VPCPUs: 1, MemMB: 1, MaxConcurrency: 1, AdmissionCeilingMB: 0},
	}
	for i, cfg := range cases {
		_, err := registerComputeNode(context.Background(), st, cfg, "unix:///x", nil, testLogger())
		if err == nil {
			t.Errorf("case %d: expected zero-field rejection", i)
		}
	}
}

// TestRegisterComputeNode_RejectsNegativeVCPUBudget: issue #938 / PR-A.
// The migration 00123 CHECK (vcpu_budget > 0) trips the upsert, so
// LoadConfig also rejects it via FAAS_VCPU_BUDGET. A negative value at
// the register boundary must surface as a startup error rather than
// reaching the upsert.
func TestRegisterComputeNode_RejectsNegativeVCPUBudget(t *testing.T) {
	st := state.NewMemStore()
	cfg := ComputeNodeConfig{
		NodeName:           "box-east-1",
		VPCPUs:             160,
		MemMB:              56000,
		MaxConcurrency:     200,
		AdmissionCeilingMB: 47600,
		VCPUBudget:         -1,
	}
	_, err := registerComputeNode(context.Background(), st, cfg, "unix:///x", nil, testLogger())
	if err == nil {
		t.Fatal("expected negative VCPUBudget to be rejected")
	}
	if !strings.Contains(err.Error(), "vcpu_budget") {
		t.Errorf("error %q does not name vcpu_budget", err.Error())
	}
}

// TestRegisterComputeNode_DefaultsVCPUBudgetFromAPI: issue #938 / PR-A.
// When the operator leaves VCPUBudget at the struct-default zero value,
// the upsert falls back to api.VCPUSlots so the migration 00123 CHECK
// never trips on single-box dev. Pins the fallback to a single source
// of truth (pkg/api.VCPUSlots).
func TestRegisterComputeNode_DefaultsVCPUBudgetFromAPI(t *testing.T) {
	st := state.NewMemStore()
	cfg := ComputeNodeConfig{
		NodeName:           "box-east-1",
		VPCPUs:             160,
		MemMB:              56000,
		MaxConcurrency:     200,
		AdmissionCeilingMB: 47600,
		// VCPUBudget omitted → fallback to api.VCPUSlots
	}
	got, err := registerComputeNode(context.Background(), st, cfg, "unix:///x", nil, testLogger())
	if err != nil {
		t.Fatalf("registerComputeNode: %v", err)
	}
	if got.VCPUBudget != api.VCPUSlots {
		t.Errorf("VCPUBudget = %d, want %d (api.VCPUSlots fallback)", got.VCPUBudget, api.VCPUSlots)
	}
}

// TestRegisterComputeNode_HonorsExplicitVCPUBudget: issue #938 / PR-A.
// Heterogeneous fleets override the per-host vCPU ceiling via
// [compute_node].vcpu_budget; the value flows through verbatim to the
// compute_nodes row.
func TestRegisterComputeNode_HonorsExplicitVCPUBudget(t *testing.T) {
	st := state.NewMemStore()
	cfg := ComputeNodeConfig{
		NodeName:           "box-east-1",
		VPCPUs:             160,
		MemMB:              56000,
		MaxConcurrency:     200,
		AdmissionCeilingMB: 47600,
		VCPUBudget:         40, // smaller box, narrower ceiling
	}
	got, err := registerComputeNode(context.Background(), st, cfg, "unix:///x", nil, testLogger())
	if err != nil {
		t.Fatalf("registerComputeNode: %v", err)
	}
	if got.VCPUBudget != 40 {
		t.Errorf("VCPUBudget = %d, want 40 (operator override)", got.VCPUBudget)
	}
}

// TestRegisterComputeNode_OverlayDetectionErrorContinues: a tailscale
// detection failure logs a warning and proceeds without the IP
// rather than failing vmmd startup. This matters for single-box dev
// where tailscale isn't installed and the daemon should still
// register via the unix target_url.
func TestRegisterComputeNode_OverlayDetectionErrorContinues(t *testing.T) {
	st := state.NewMemStore()
	detector := func(context.Context) (string, error) {
		return "", errors.New("tailscale down")
	}
	got, err := registerComputeNode(context.Background(), st,
		ComputeNodeConfig{
			NodeName: "box-east-1", VPCPUs: 1, MemMB: 1024,
			MaxConcurrency: 1, AdmissionCeilingMB: 512,
		}, "tcp://100.64.0.1:50051", detector, testLogger())
	if err != nil {
		t.Fatalf("overlay failure should not block registration: %v", err)
	}
	if got.Name != "box-east-1" {
		t.Errorf("name = %q", got.Name)
	}
}

// TestRegisterComputeNode_TargetURLPreservesOperatorValue pins the
// end-to-end ownership contract through registerComputeNode: an
// operator's POSTed target_url survives a vmmd restart even when
// vmmd's view of its dial target is wrong (the conflation that
// shipped with PR #445 / ADR-025 v1.1). The fix is the
// UpsertComputeNodeFromVmmd ownership split + the explicit
// targetURL parameter on registerComputeNode (no longer inferred
// from listen_addr).
//
// Sequence:
//  1. apid POSTs target_url=tcp://vmmd-2.faas:50051 via the
//     operator path (UpsertComputeNodeFromOperator).
//  2. vmmd restarts; registerComputeNode is called with
//     targetURL=tcp://0.0.0.0:50051 (the bind address —
//     deliberately wrong to surface the trap).
//  3. The stored row's target_url MUST still be the operator's
//     FQDN; vmmd-owned resource numbers are refreshed.
func TestRegisterComputeNode_TargetURLPreservesOperatorValue(t *testing.T) {
	st := state.NewMemStore()
	ctx := context.Background()

	// Step 1: operator POST (apid path).
	operator, err := st.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name:      "fsn-2",
		TargetURL: "tcp://vmmd-2.faas:50051",
		VPCPUs:    160, MemMB: 56000,
		MaxConcurrency: 200, AdmissionCeilingMB: 47600,
	})
	if err != nil {
		t.Fatalf("operator upsert: %v", err)
	}
	operatorID := operator.ID

	// Step 2: vmmd restart with a wrong target_url.
	got, err := registerComputeNode(ctx, st,
		ComputeNodeConfig{
			NodeName: "fsn-2", VPCPUs: 160, MemMB: 56000,
			MaxConcurrency: 200, AdmissionCeilingMB: 47600,
		}, "tcp://0.0.0.0:50051", nil, testLogger())
	if err != nil {
		t.Fatalf("vmmd register: %v", err)
	}

	// Step 3: target_url preserved.
	if got.TargetURL != "tcp://vmmd-2.faas:50051" {
		t.Errorf("vmmd register CLOBBERED operator target_url: got %q, want tcp://vmmd-2.faas:50051",
			got.TargetURL)
	}
	if got.ID != operatorID {
		t.Errorf("id changed across re-register: %q -> %q", operatorID, got.ID)
	}
}

// TestRegisterComputeNode_TargetURLFromConfig exercises the new
// ResolveTargetURL shape — when the operator leaves target_url
// empty in [compute_node], registerComputeNode is called with
// whatever the caller derives (here: tcp://100.64.0.1:50051 from
// overlay_ip). The stored row carries that dial target. This is
// the multi-box "auto-detect via Tailscale" path.
func TestRegisterComputeNode_TargetURLFromConfig(t *testing.T) {
	st := state.NewMemStore()
	got, err := registerComputeNode(context.Background(), st,
		ComputeNodeConfig{
			NodeName: "box-east-1", VPCPUs: 1, MemMB: 1024,
			MaxConcurrency: 1, AdmissionCeilingMB: 512,
			OverlayIP: "100.64.0.1",
		}, "tcp://100.64.0.1:50051", nil, testLogger())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if got.TargetURL != "tcp://100.64.0.1:50051" {
		t.Errorf("target_url = %q, want tcp://100.64.0.1:50051", got.TargetURL)
	}
}

// TestResolveTargetURL_ConfigResolution is the unit-level pin
// for the ResolveTargetURL precedence rules:
//  1. Explicit [compute_node].target_url wins (highest priority).
//  2. Otherwise tcp://+OverlayIP+:50051 when overlay_ip is set.
//  3. Otherwise unix://+SocketPath (single-box default-local).
func TestResolveTargetURL_ConfigResolution(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "explicit target_url wins",
			cfg: Config{
				SocketPath: "/run/faas/vmmd.sock",
				ComputeNode: ComputeNodeConfig{
					TargetURL: "tcp://vmmd-2.faas:50051",
					OverlayIP: "100.64.0.1",
				},
			},
			want: "tcp://vmmd-2.faas:50051",
		},
		{
			name: "overlay_ip fallback",
			cfg: Config{
				SocketPath: "/run/faas/vmmd.sock",
				ComputeNode: ComputeNodeConfig{
					OverlayIP: "100.64.0.1",
				},
			},
			want: "tcp://100.64.0.1:50051",
		},
		{
			name: "single-box unix fallback",
			cfg: Config{
				SocketPath:  "/run/faas/vmmd.sock",
				ComputeNode: ComputeNodeConfig{
					// no target_url, no overlay_ip
				},
			},
			want: "unix:///run/faas/vmmd.sock",
		},
		{
			name: "whitespace target_url treated as empty",
			cfg: Config{
				SocketPath: "/run/faas/vmmd.sock",
				ComputeNode: ComputeNodeConfig{
					TargetURL: "   ",
					OverlayIP: "100.64.0.2",
				},
			},
			want: "tcp://100.64.0.2:50051",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.ResolveTargetURL()
			if got != tc.want {
				t.Errorf("ResolveTargetURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// generateP256PrivateKey is the test helper for the slice-3
// registerComputeNodeKey coverage. Returns a fresh ECDSA P-256
// key + its canonical key_id (SHA-256(SPKI) hex) so the test can
// pre-compute the expected row without coupling to sched's
// internal hash.
func generateP256PrivateKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	keyID, err := sched.KeyIDForPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("sched.KeyIDForPublicKey: %v", err)
	}
	return priv, keyID
}

// TestRegisterComputeNodeKey_HappyPath pins the row-insertion
// contract: a fresh signing key against a registered node lands
// in compute_node_keys with the canonical key_id and a PEM body
// that round-trips through x509.ParsePKIXPublicKey to the same
// public key. The PKCS#8 + PEM round-trip is the load-bearing
// bit — sched's parsePublicKeyPEM only accepts a SubjectPublicKeyInfo
// PEM block, and the wire shape is the only piece the schedd side
// reads back.
func TestRegisterComputeNodeKey_HappyPath(t *testing.T) {
	st := state.NewMemStore()
	priv, keyID := generateP256PrivateKey(t)
	const nodeID = "00000000-0000-0000-0000-000000000001"

	if err := registerComputeNodeKey(context.Background(), st, nodeID, priv, keyID, testLogger()); err != nil {
		t.Fatalf("registerComputeNodeKey: %v", err)
	}

	gotPEM, ok := st.LookupNodeKey(context.Background(), nodeID, keyID)
	if !ok {
		t.Fatalf("row not present after Upsert")
	}

	block, _ := pem.Decode([]byte(gotPEM))
	block = mustPEMBlock(t, block, fmt.Sprintf("PEM not decodable: %q", gotPEM))
	if block.Type != "PUBLIC KEY" {
		t.Errorf("PEM type = %q, want PUBLIC KEY", block.Type)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}
	parsedECDSA, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("parsed type = %T, want *ecdsa.PublicKey", parsed)
	}
	if parsedECDSA.X.Cmp(priv.X) != 0 || parsedECDSA.Y.Cmp(priv.Y) != 0 {
		t.Error("parsed public key does not match the one we registered")
	}
}

// TestRegisterComputeNodeKey_NilKeySkips: pre-slice-3 mode. The
// function must not call UpsertNodeKey (which would error on an
// empty keyID) and must not fail vmmd startup. The legacy
// single-box vmmd has no node.key on disk; the publisher emits
// unsigned reports and the wire accepts them (ADR-016).
func TestRegisterComputeNodeKey_NilKeySkips(t *testing.T) {
	st := state.NewMemStore()
	const nodeID = "00000000-0000-0000-0000-000000000002"

	if err := registerComputeNodeKey(context.Background(), st, nodeID, nil, "", testLogger()); err != nil {
		t.Fatalf("nil-key path returned %v; want nil (pre-slice-3 mode)", err)
	}
	if _, ok := st.LookupNodeKey(context.Background(), nodeID, ""); ok {
		t.Error("nil-key path wrote a row")
	}
}

// TestRegisterComputeNodeKey_EmptyNodeIDSkips: the legacy
// default-local path. registerComputeNode was called with an
// empty NodeName, so cn.ID is "" and there's nothing to attach
// the key to. Match that path's silent-skip posture: the
// function returns nil and writes nothing. A regression that
// attempted an upsert with an empty nodeID would surface as an
// UpsertNodeKey validation error here.
func TestRegisterComputeNodeKey_EmptyNodeIDSkips(t *testing.T) {
	st := state.NewMemStore()
	priv, keyID := generateP256PrivateKey(t)

	if err := registerComputeNodeKey(context.Background(), st, "", priv, keyID, testLogger()); err != nil {
		t.Fatalf("empty-nodeID path returned %v; want nil (default-local only)", err)
	}
	if _, ok := st.LookupNodeKey(context.Background(), "", keyID); ok {
		t.Error("empty-nodeID path wrote a row")
	}
}

// TestRegisterComputeNodeKey_Idempotent: a second registration
// with the same (nodeID, keyID) must succeed without modifying
// the stored PEM. This is the "vmmd restarted, key unchanged"
// path — the row is already there from the first boot, and the
// second boot should be a no-op. ON CONFLICT DO NOTHING in
// PgStore + the composite-key collision check in MemStore
// together enforce this; the assertion proves the wiring
// surfaces it correctly to the caller.
func TestRegisterComputeNodeKey_Idempotent(t *testing.T) {
	st := state.NewMemStore()
	priv, keyID := generateP256PrivateKey(t)
	const nodeID = "00000000-0000-0000-0000-000000000003"

	if err := registerComputeNodeKey(context.Background(), st, nodeID, priv, keyID, testLogger()); err != nil {
		t.Fatalf("first: %v", err)
	}
	firstPEM, _ := st.LookupNodeKey(context.Background(), nodeID, keyID)

	if err := registerComputeNodeKey(context.Background(), st, nodeID, priv, keyID, testLogger()); err != nil {
		t.Fatalf("second: %v", err)
	}
	secondPEM, _ := st.LookupNodeKey(context.Background(), nodeID, keyID)
	if firstPEM != secondPEM {
		t.Errorf("PEM changed across upsert: %q -> %q", firstPEM, secondPEM)
	}
}

// TestPublicKeyPEM_NilKey: the marshaller's nil guard. Without
// it, x509.MarshalPKIXPublicKey(nil) returns an empty slice and
// a nil error — the PEM block would be a zero-length body that
// downstream parsePublicKeyPEM would reject with "PEM type"
// + "parse PKIX" errors instead of the clearer "nil key" we
// want at the call site. A regression that drops the guard
// would surface here.
func TestPublicKeyPEM_NilKey(t *testing.T) {
	if _, err := publicKeyPEM(nil); err == nil {
		t.Fatal("publicKeyPEM(nil) succeeded; want error")
	}
}

// mustPEMBlock is the SA5011 escape hatch for the node-key
// registration test: pem.Decode can legitimately return (nil, rest)
// for malformed input, but we want a real block for assertions.
// A helper that t.Fatal()s and returns the value lets staticcheck
// see the value is non-nil at the call site.
func mustPEMBlock(t *testing.T, b *pem.Block, msg string) *pem.Block {
	t.Helper()
	if b == nil {
		t.Fatal(msg)
	}
	return b
}

// ----------------------------------------------------------------------------
// Mega-PR-B Commit 3 — overlay IP detection scoring
//
// Pure helpers + a no-exec stub-detector path, so the tests don't
// need a tailscale binary on PATH. Each case pins one branch of
// detectOverlayIP / parseTailscaleIPLines / scoreByCIDR; the
// production defaultDetectOverlayIP is exercised through the
// cfg-bearing entry point at the bottom.
// ----------------------------------------------------------------------------

// TestDetectOverlayIP_PrefersCIDRMatch exercises the load-bearing
// branch: when tailscale prints multiple IPv4 lines, the candidate
// that lives in PreferCIDR wins. Anti-regression for the Tier-1
// BLOCKING tail-1 NIC ambiguity where a multi-NIC host's first
// `tailscale ip -4` line was a non-overlay secondary IP.
func TestDetectOverlayIP_PrefersCIDRMatch(t *testing.T) {
	stub := []byte("100.64.0.5\n192.168.1.10\n100.100.100.1\n")
	got, err := detectOverlayIP(context.Background(), OverlayDetector{
		TailscaleBinaryPath: "/nonexistent-but-not-looked-up",
		PreferCIDR:          netip.MustParsePrefix("100.64.0.0/10"),
		Run:                 func(_ context.Context) ([]byte, error) { return stub, nil },
	})
	if err != nil {
		t.Fatalf("detectOverlayIP: %v", err)
	}
	if got != "100.64.0.5" {
		t.Errorf("overlay IP = %q, want 100.64.0.5 (Tailscale CGNAT in PreferCIDR/10)", got)
	}
}

// TestDetectOverlayIP_FirstLineWhenNoCIDRMatch asserts the v1
// fall-through: when no candidate matches PreferCIDR, the first
// line wins (legacy first-line behavior preserved). An operator
// who set PreferCIDR too narrow still gets an answer.
func TestDetectOverlayIP_FirstLineWhenNoCIDRMatch(t *testing.T) {
	stub := []byte("192.168.1.10\n100.64.0.5\n")
	got, err := detectOverlayIP(context.Background(), OverlayDetector{
		TailscaleBinaryPath: "/not-used",
		PreferCIDR:          netip.MustParsePrefix("203.0.113.0/24"), // no candidate in this /24
		Run:                 func(_ context.Context) ([]byte, error) { return stub, nil },
	})
	if err != nil {
		t.Fatalf("detectOverlayIP: %v", err)
	}
	if got != "192.168.1.10" {
		t.Errorf("overlay IP = %q, want 192.168.1.10 (first-line fall-through)", got)
	}
}

// TestDetectOverlayIP_FirstLineWhenNoPreferCIDR is the explicit
// v1 contract: zero-value PreferCIDR (no manifest overlay.cidr)
// returns the first IP line. The Mega-PR-A config that omitted
// overlay_cidr must not break.
func TestDetectOverlayIP_FirstLineWhenNoPreferCIDR(t *testing.T) {
	stub := []byte("100.64.0.5\n192.168.1.10\n")
	got, err := detectOverlayIP(context.Background(), OverlayDetector{
		TailscaleBinaryPath: "/not-used",
		Run:                 func(_ context.Context) ([]byte, error) { return stub, nil },
	})
	if err != nil {
		t.Fatalf("detectOverlayIP: %v", err)
	}
	if got != "100.64.0.5" {
		t.Errorf("overlay IP = %q, want 100.64.0.5 (zero-PreferCIDR v1 behavior)", got)
	}
}

// TestDetectOverlayIP_ErrorsOnEmptyOutput pins the malformed-binary
// path: tailscale exists but produces no output. Must error (not
// silently return ""), so the caller logs and continues rather
// than accidentally skipping overlay detection.
func TestDetectOverlayIP_ErrorsOnEmptyOutput(t *testing.T) {
	_, err := detectOverlayIP(context.Background(), OverlayDetector{
		TailscaleBinaryPath: "/not-used",
		Run:                 func(_ context.Context) ([]byte, error) { return nil, nil },
	})
	if err == nil {
		t.Fatal("detectOverlayIP: want error on empty output, got nil")
	}
}

// TestParseTailscaleIPLines covers the pure parser. Filters IPv6,
// trims whitespace, rejects garbage lines, skips blanks. Pinned so
// a future edit that accidentally accepts `fe80::1` (or returns
// silently on garbage) fails here.
func TestParseTailscaleIPLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // stringified netip.Addr for equality
		err  bool
	}{
		{"ipv4-only", "100.64.0.5\n", []string{"100.64.0.5"}, false},
		{"ipv4-with-ipv6", "100.64.0.5\nfe80::1\n", []string{"100.64.0.5"}, false},
		{"trailing-whitespace", "100.64.0.5   \n", []string{"100.64.0.5"}, false},
		{"blank-lines", "100.64.0.5\n\n100.64.0.6\n", []string{"100.64.0.5", "100.64.0.6"}, false},
		{"garbage-line", "100.64.0.5\nnot-an-ip\n", nil, true},
		{"empty-after-trim", " \n \n", []string{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addrs, err := parseTailscaleIPLines([]byte(tc.in))
			if tc.err {
				if err == nil {
					t.Fatalf("parseTailscaleIPLines: want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTailscaleIPLines: %v", err)
			}
			if len(addrs) != len(tc.want) {
				t.Fatalf("parseTailscaleIPLines: got %d addrs, want %d (%v vs %v)",
					len(addrs), len(tc.want), addrs, tc.want)
			}
			for i, a := range addrs {
				if a.String() != tc.want[i] {
					t.Errorf("parseTailscaleIPLines[%d] = %s, want %s", i, a, tc.want[i])
				}
			}
		})
	}
}

// TestScoreByCIDR pins the scoring priority order:
//
//   - empty PreferCIDR → first element
//   - PreferCIDR with match → first match (stable order)
//   - PreferCIDR with no match → first element (fall-through)
//
// The stable-order-on-tie clause is load-bearing: when two
// candidates both live in PreferCIDR, the earlier one wins
// (the legacy first-line contract is preserved on ties).
func TestScoreByCIDR(t *testing.T) {
	mkAddrs := func(s ...string) []netip.Addr {
		out := make([]netip.Addr, 0, len(s))
		for _, v := range s {
			out = append(out, netip.MustParseAddr(v))
		}
		return out
	}
	t.Run("empty-prefer", func(t *testing.T) {
		got := scoreByCIDR(mkAddrs("100.64.0.5", "192.168.1.10"), netip.Prefix{})
		if got.String() != "100.64.0.5" {
			t.Errorf("got %s, want 100.64.0.5", got)
		}
	})
	t.Run("single-match", func(t *testing.T) {
		got := scoreByCIDR(mkAddrs("192.168.1.10", "100.64.0.5", "100.100.100.1"),
			netip.MustParsePrefix("100.64.0.0/10"))
		if got.String() != "100.64.0.5" {
			t.Errorf("got %s, want 100.64.0.5", got)
		}
	})
	t.Run("tie-stable-order", func(t *testing.T) {
		got := scoreByCIDR(mkAddrs("100.64.0.5", "100.64.0.6"),
			netip.MustParsePrefix("100.64.0.0/10"))
		if got.String() != "100.64.0.5" {
			t.Errorf("got %s, want 100.64.0.5 (first tie wins)", got)
		}
	})
	t.Run("no-match", func(t *testing.T) {
		got := scoreByCIDR(mkAddrs("192.168.1.10", "100.64.0.5"),
			netip.MustParsePrefix("203.0.113.0/24"))
		if got.String() != "192.168.1.10" {
			t.Errorf("got %s, want 192.168.1.10 (no-match fall-through)", got)
		}
	})
	t.Run("empty-list", func(t *testing.T) {
		got := scoreByCIDR(nil, netip.MustParsePrefix("100.64.0.0/10"))
		if got.IsValid() {
			t.Errorf("got %s, want zero Addr for empty list", got)
		}
	})
}
