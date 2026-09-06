// Command vmmd — microVM supervisor: firecracker + jailer, the only root
// component (spec §4.4). vmmd owns everything that touches
// /usr/bin/firecracker and the jailer. It is the sole root-privileged daemon;
// per-VM work drops to the jailer immediately. Do not add a path that lets
// another component touch firecracker directly (spec §Component ownership).
//
// M1 wires the gRPC control surface (CreateFromSnapshot, CreateColdBoot,
// Pause+Snapshot, Destroy, Stats) per ADR-013..016. The control-plane TCP
// port is gated by the metrics_addr config field; the only required listen
// is the unix-domain socket at /run/faas/vmmd.sock.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/fcvm/cpustats"
	"github.com/onebox-faas/faas/pkg/fcvm/netstats"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/sched/flowcount"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/snapshothipd"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

// ellipticP256 returns the P-256 curve. Mirrors
// sched.ecdsaP256 without importing the unexported name from
// pkg/sched — we just need a stable pointer for the curve
// equality check inside loadNodeSigningKey.
func ellipticP256() elliptic.Curve { return elliptic.P256() }

// signalAdapter (M-2 / ADR-138 §Decision 1) wraps *fcvm.Manager
// so the gRPC surface can take int32 (the wire shape — every
// language binding sees int32) while the underlying Manager
// uses syscall.Signal (the kernel shape). All other Manager
// methods are inherited via embedding; only SignalAndKill
// needs the int32→syscall.Signal translation. The adapter is
// intentionally minimal — a single-method wrapper that keeps
// Manager's public API syscall-typed (matches the fcvm
// package's kernel-shaped idiom) without leaking the wire
// type into fcvm.
type signalAdapter struct {
	*fcvm.Manager
}

func (a signalAdapter) SignalAndKill(ctx context.Context, instance string, signal int32, graceSeconds int32) (bool, int32, error) {
	return a.Manager.SignalAndKill(ctx, instance, syscall.Signal(signal), time.Duration(graceSeconds)*time.Second)
}

// defaultNodeKeyPath is the canonical location for the slice-3
// node signing key (ADR-053). Mirrors DefaultSignKeyPath from
// pkg/cosign but lives under the vmmd-specific secrets dir so a
// future signer split (e.g. per-daemon keys) doesn't collide.
// Mode 0400 root:root on the install (PR-#237 stat-assert).
const defaultNodeKeyPath = "/etc/faas/secrets/vmmd/node.key"

// errNodeKeyInsecure is returned by loadNodeSigningKey when the
// node.key file's mode permits any group/other access. Inserting
// a node key whose file is readable by the faas-imaged or faas-
// schedd user is a §11 G2 violation: the canonical install is
// 0400 root:root (vmmd is the only root daemon, so the file is
// only readable because vmmd runs as root). Anything looser
// (group read, world read, any write/exec/setuid) is a PKI
// tamper signal and the daemon refuses to start.
var errNodeKeyInsecure = errors.New("vmmd: node.key mode permits group/other access")

// loadNodeSigningKey loads the per-node ECDSA P-256 signing key
// vmmd uses to stamp every CapacityReport with node_signature
// (ADR-053).
//
// Returns (nil, "", nil) when the file is missing — single-box
// dev default falls through to pre-slice-3 mode (unsigned
// reports). The wire field is additive per ADR-016, so legacy
// schedd silently accepts the empty signature.
//
// On a non-empty file: the file must be mode 0400 root:root
// (mode 0440 owner+group read is NOT accepted here — vmmd is
// the only root daemon, so the canonical install is owner-only
// to keep the post-restart file-mode stat-assert simple). The
// PEM type must be PRIVATE KEY (PKCS#8) and the curve must be
// P-256. The key_id is computed once as SHA-256(SPKI) hex so the
// hot path stays cheap and the schedd-side registry can bind
// signatures to the leaf's identity without re-running the PEM
// parse on every report.
//
// The mode check is performed via the open fd (f.Stat after
// os.Open), not a separate os.Stat call. A separate Stat +
// ReadFile pair is TOCTOU-racy: an attacker with write access
// to /etc/faas/secrets/vmmd/ could chmod 0400 node.key, then
// replace its body before ReadFile sees the new contents.
// Reading the inode via the open fd binds the mode check to
// the same inode the body comes from — a rename-aside attack
// surfaces as the open fd continuing to read the original
// (now-unlinked) inode, not as a swapped body.
//
// The matching public key is registered in schedd's
// compute_node_keys table by an out-of-band install step; the
// registry listens for `compute_node_changed` pg_notify and
// picks up the row within its next refresh tick (migration
// 00076).
// loadNodeSigningKeyDefault is the zero-arg shim used by every
// call site that doesn't carry a vmmd.toml (notably the
// loadNodeSigningKey unit tests and any test that exercises the
// env-var fallback path). Delegates to loadNodeSigningKey with
// an empty pathOverride so the env-or-default resolution
// applies.
func loadNodeSigningKeyDefault() (*ecdsa.PrivateKey, string, error) {
	return loadNodeSigningKey("")
}

func loadNodeSigningKey(pathOverride string) (*ecdsa.PrivateKey, string, error) {
	// Resolution order (most-specific first):
	//   1. pathOverride — empty string means "use the next layer down".
	//      Wired from cfg.NodeKeyPath in run() so a vmmd.toml with
	//      `node_key_path = "/path/to/key"` is honoured.
	//   2. FAAS_VMMD_NODE_KEY_PATH env var — containerised-deploys
	//      path (no toml in those images).
	//   3. defaultNodeKeyPath — the canonical install location
	//      /etc/faas/secrets/vmmd/node.key.
	//
	// Layer 1 wins over the env var so an operator who sets BOTH
	// gets the toml value; that matches the precedence every other
	// daemon follows (config.go + daemonunitspec wiring). Passing
	// empty from run() preserves the original env-or-default
	// behaviour for callers that don't carry a config (notably
	// tests that exercise this function directly).
	path := pathOverride
	if path == "" {
		path = envOr("FAAS_VMMD_NODE_KEY_PATH", defaultNodeKeyPath)
	}
	//nolint:forbidigo // vetted daemon-id path (/etc/faas/secrets/vmmd/node.key); the
	// openCustomerFile guard is for customer-supplied CLI paths. Here the
	// path is constructed from the FAAS_VMMD_NODE_KEY_PATH env var with a
	// fixed default, then stat'd on the same fd to bind the mode check to
	// the body read (F4 TOCTOU pin) — os.ReadFile would discard the fd and
	// re-introduce the swap window.
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Pre-slice-3 mode: no node key on disk. The
			// publisher emits unsigned reports; legacy
			// schedd accepts, slice-3 schedd (with a
			// configured registry) rejects the stream.
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("vmmd: open node key %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	// Mode 0400 strict, read via the open fd so the inode
	// can't be swapped between the mode check and the body
	// read. vmmd is root, so group/other bits are an
	// unambiguous tamper signal. Anything looser →
	// errNodeKeyInsecure (fail-loud, not fail-open).
	info, err := f.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("vmmd: fstat node key %q: %w", path, err)
	}
	// Mode 0400 strict, AND no setuid/setgid/sticky bits.
	// info.Mode().Perm() returns only the lower 9 bits
	// (rwxrwxrwx), so a file with mode 0o4600 (setuid + no
	// group/other read) would otherwise pass the perm check
	// while still being an unprivileged-escalation surface.
	// Rejecting anything beyond the strict 9 bits closes
	// that window — the canonical install is owner-read only
	// and any deviation (sticky, setuid, setgid, write, exec)
	// is a PKI tamper signal.
	if perm := info.Mode().Perm(); perm != 0o400 {
		return nil, "", fmt.Errorf("vmmd: node.key %q mode %#o: %w",
			path, perm, errNodeKeyInsecure)
	}
	if extra := info.Mode() &^ os.ModePerm; extra != 0 {
		return nil, "", fmt.Errorf("vmmd: node.key %q has setuid/setgid/sticky bits (%#o): %w",
			path, extra, errNodeKeyInsecure)
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, "", fmt.Errorf("vmmd: read node key %q: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, "", fmt.Errorf("vmmd: node key %q is not PEM-encoded", path)
	}
	// PKCS#8 form. The image builder (cmd/faas-pki) emits
	// "PRIVATE KEY" (PKCS#8), not "EC PRIVATE KEY" (SEC1),
	// so the matching PEM type is required.
	if block.Type != "PRIVATE KEY" {
		return nil, "", fmt.Errorf("vmmd: node key %q PEM type %q, want PRIVATE KEY",
			path, block.Type)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("vmmd: parse node key %q: %w", path, err)
	}
	priv, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, "", fmt.Errorf("vmmd: node key %q is not ECDSA (got %T)", path, key)
	}
	if priv.Curve != ellipticP256() {
		return nil, "", fmt.Errorf("vmmd: node key %q curve %s, want P-256",
			path, priv.Curve.Params().Name)
	}
	keyID, err := sched.KeyIDForPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, "", fmt.Errorf("vmmd: compute key_id for %q: %w", path, err)
	}
	return priv, keyID, nil
}

const metricsPath = "/metrics"

// ReportLivenessFailedCtxTimeout caps the vmmd→schedd
// drain for the liveness-failed RPC (issue #554 / ADR-078).
// 3 s matches the gRPC client default but is a separate,
// named constant so a future ops review can lift the cap if
// the schedd-side state-machine guard grows. The dial + RPC
// are both bounded by this; a wedged schedd surfaces as a
// log warning, the vmmd loop exits cleanly on its end, and
// the next probe will re-trigger if the guest is still
// wedged.
const ReportLivenessFailedCtxTimeout = 3 * time.Second

// ReportWorkloadOOMCtxTimeout (Cluster C / ADR-121) caps the
// vmmd→schedd drain for the workload-OOM RPC. The wire path
// is best-effort (the guest-init listener exits on its end
// after one emit; the workload is dead, the VM is about to be
// torn down) so 3 s matches the liveness constant — a wedged
// schedd surfaces as a Warn log, the vmmd loop exits cleanly
// on its end, and the customer's deployment was going to
// fail anyway (the stamp path on the guest side is the source
// of truth).
const ReportWorkloadOOMCtxTimeout = 3 * time.Second

func main() {
	if runMountBindHelper() {
		return
	}
	wire.Daemon("vmmd", run)
}

// runDeps is the dependency-injection seam for testing. Production code
// uses the defaults; tests can swap individual fields to drive `run` without
// needing KVM, root, or a real /etc/faas/vmmd.toml.
type runDeps struct {
	configPath string                                                                                                // defaults to /etc/faas/vmmd.toml
	detectFC   func(context.Context) (string, error)                                                                 // defaults to fcvm.DetectFirecrackerVersion
	listen     func(ctx context.Context, target string, tlsCfg *tls.Config, daemonUser string) (net.Listener, error) // defaults to wire.ListenAs (issue #95 / ADR-025)
	// openDB / openStore: only invoked when [compute_node].name is set;
	// the legacy default-local path skips the DB entirely (no upsert).
	openDB    func(context.Context, string) (*pgxpool.Pool, error)
	openStore func(*pgxpool.Pool) *state.PgStore
	// detectOverlayIP — best-effort, default shelles out to
	// `tailscale ip -4`. nil means "skip overlay detection"
	// (WireGuard-mode operators set [compute_node].overlay_ip
	// explicitly and don't need this hook).
	detectOverlayIP func(context.Context) (string, error)
	// hostKey plumbing — function-typed so tests can drive first-boot
	// (LoadHostKey returns ErrHostKeyNotFound → GenerateAndSaveHostKey)
	// and restart (LoadHostKey returns id) paths without touching disk.
	loadHostKey    func(path string) (*age.X25519Identity, error)
	loadHostKeys   func(dir string) ([]*age.X25519Identity, error)
	genAndSaveKey  func(path string) (*age.X25519Identity, error)
	writeRecipient func(path string, id *age.X25519Identity) error
	// popCounters: PR-E egress-deny poll seam. nil → netns.PopCounters
	// (metal) / netns.PopCounters non-metal stub (unit tests on dev box).
	// Tests inject a stub map-returning func to drive runEgressPoll
	// without shelling out to nft.
	popCounters popCountersFunc
	// popInstanceCounters: C1 per-netns egress-denied poll seam.
	// nil selects netns.PopCountersInNetns.
	popInstanceCounters popInstanceCountersFunc
	// egressPollInterval: PR-E override for runEgressPoll's cadence.
	// nil → EgressPollInterval (15s). Tests inject a 1ms cadence so the
	// loop ticks fast enough to be observable in a unit test.
	egressPollInterval *time.Duration
	// startEgressPoll: PR-E seam. nil → start the production goroutine
	// bound to ctx + ops + popCounters + log. Tests inject a no-op to
	// skip the loop entirely, or a callback to observe the seam args.
	startEgressPoll func(ctx context.Context, ops *wire.OpsMetrics, pop popCountersFunc, interval time.Duration, log *slog.Logger)
	// scheddTarget: ADR-025 axis 5 — vmmd's outbound gRPC target for
	// the capacity publisher. Empty disables the publisher entirely
	// (single-box dev default). Tests inject a fake target to drive
	// the seam without a real schedd.
	scheddTarget string
	// scheddClientTLS: ADR-052 — mTLS config the capacity publisher
	// uses to dial schedd. nil → no TLS (single-box unix socket);
	// loaded from cfg.ScheddClientTLS in main.go and passed through.
	// Tests inject a stub to assert the seam forwards the right
	// *tls.Config to wire.DialContext.
	scheddClientTLS *tls.Config
	// capacityInterval: ADR-025 axis 5 — override for the publisher's
	// tick cadence. nil → CapacityInterval (1 s). Tests inject
	// sub-second cadence so the loop has observable ticks in a unit
	// test.
	capacityInterval *time.Duration
	// residentFn: ADR-025 axis 5 — leakcheck seam. nil → leakcheck.ResidentBytes.
	// Tests inject a stub returning a fixed map.
	residentFn func() (map[string]int64, bool)
	// startCapacityPublish: ADR-025 axis 5 — seam for the publisher
	// goroutine. nil → start the production loop rooted at runCapacityPublish.
	// Tests inject a no-op to skip the loop or a callback to drive
	// the seam args.
	//
	// `counts` is a countReader (PR-1 review) rather than a concrete
	// *fcvm.Manager so the production wiring still passes `mgr` (which
	// satisfies the interface) and tests can inject a stub without
	// booting a real Manager.
	startCapacityPublish func(ctx context.Context, counts countReader, nodeID string, cfg ComputeNodeConfig, scheddTarget string, scheddClientTLS *tls.Config, tick time.Duration, resident func() (map[string]int64, bool), nodeKey *ecdsa.PrivateKey, nodeKeyID string, log *slog.Logger)
	// startEgressWatcher: ADR-055 — seam for the runtime egress
	// policy watcher. nil → start the production goroutine bound to
	// ctx + log + the watcher struct built inline. Tests inject a
	// no-op to skip the loop, or a callback to capture the watcher
	// reference for stub-driven reload assertions.
	//
	// The watcher is gated on cfg.ComputeNode.NodeName != "" in
	// runWithDeps (mirrors the capacity publisher gate), so single-box
	// default-local vmmd does NOT observe the egress_policy_changed
	// channel. The watcher is the only consumer of NotifyEgressPolicyChanged.
	startEgressWatcher func(ctx context.Context, log *slog.Logger)
	// egressWatcher: the constructed watcher. nil in production
	// (runWithDeps builds it); tests inject a stub to drive Reload
	// without subscribing to pg_notify. Bulk of the watcher's logic
	// is in egressWatcher.Reload — see cmd/vmmd/egress_watcher.go.
	egressWatcher *egressWatcher
	// capCheck: DEPLOY-1 / ADR-075 capdecl gate seam. nil → call
	// runtimecheck.MustCheckOnBoot(capsDecl, log, nil) which exits
	// on violation in production. Tests inject func() error { return nil }
	// to bypass the live /proc/self/status check (the runner lacks
	// the production capset, and MustCheckOnBoot's os.Exit on
	// violation would kill the test process). Tests that want to
	// exercise the violation branch inject a func returning a
	// non-nil error.
	capCheck func() error
}

func defaultDeps() runDeps {
	return runDeps{
		configPath:          envOr("FAAS_VMMD_CONFIG", "/etc/faas/vmmd.toml"),
		detectFC:            fcvm.DetectFirecrackerVersion,
		listen:              wire.ListenAs,
		openDB:              db.Open,
		openStore:           state.NewPgStore,
		detectOverlayIP:     nil, // Mega-PR-B Commit 3: detectOverlayIP is bound inline at the only call site (post-LoadConfig) so it can read cfg.ComputeNode.OverlayCIDR. Legacy first-line behavior preserved when the detector finds tailscale but no PreferCIDR match.
		loadHostKey:         secretbox.LoadHostKey,
		loadHostKeys:        secretbox.LoadHostKeys,
		genAndSaveKey:       secretbox.GenerateAndSaveHostKey,
		writeRecipient:      secretbox.WriteRecipientFile,
		popCounters:         netns.PopCounters,
		popInstanceCounters: netns.PopCountersInNetns,
		egressPollInterval:  durationPtr(EgressPollInterval),
		startEgressPoll:     nil, // defaultDeps() leaves nil so the runtime branch can detect "use production"
		scheddTarget:        envOr("FAAS_VMMD_SCHEDD_TARGET", "unix:///run/faas/schedd.sock"),
		capacityInterval:    durationPtr(CapacityInterval),
		// residentFn left nil; runWithDeps fills it with
		// leakcheck.ResidentBytes once the resolver runs.
		// startCapacityPublish left nil; the runtime branch
		// detects "use production" and calls runCapacityPublish.
	}
}

func durationPtr(d time.Duration) *time.Duration { return &d }

// envOr returns the value of env key, or fallback when unset/empty.
// Named envOr to avoid colliding with any same-named helper in
// cmd/<other-daemon> if these are ever linked into the same binary.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run(ctx context.Context, log *slog.Logger) error {
	return runWithDeps(ctx, log, defaultDeps())
}

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	// DEPLOY-1 / ADR-075 capdecl gate. Validates the live
	// /proc/self/status against the declared capability set in
	// cmd/vmmd/caps.go. A misconfigured
	// AmbientCapabilities / CapabilityBoundingSet pair fails
	// fast with a *runtimecheck.Violation rather than silently
	// restart-looping in production (the failure mode that
	// drove DEPLOY-1). On success the check is silent — every
	// boot doesn't need a "validated" log line. Tests inject
	// deps.capCheck to bypass the live capset (the test runner
	// doesn't carry the production capset, and MustCheckOnBoot
	// calls os.Exit on violation).
	capCheck := deps.capCheck
	if capCheck == nil {
		capCheck = func() error { return runtimecheck.MustCheckOnBoot(capsDecl, log, nil) }
	}
	if err := capCheck(); err != nil {
		return err
	}
	ops := wire.NewOpsMetrics("vmmd")
	traceShutdown, traceErr := trace.InitTracerWithRegistry(ctx, "vmmd", wire.Version, log, ops.Registry(), ops.MetricPrefix())
	if traceErr != nil {
		return fmt.Errorf("vmmd: init tracing: %w", traceErr)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := traceShutdown(shutdownCtx); err != nil {
			log.Warn("vmmd: trace shutdown failed", "err", err)
		}
	}()

	cfg, err := LoadConfig(deps.configPath)
	if err != nil {
		return err
	}
	// Gate-B box-role gate. vmmd is a compute-only daemon — it
	// refuses to start under RoleControlPlane. The role is set
	// from TOML or FAAS_VMMD_ROLE at deploy time; default is
	// RoleSingleBox so single-box dev boots unmoved.
	if err := role.Require("vmmd", cfg.Role, role.RoleSingleBox, role.RoleComputeOnly); err != nil {
		return err
	}
	// PR scale-out tier-1 residual (Gap #3 wiring): seed the
	// slot allocator with the operator-supplied bridge CIDR so
	// every per-VM /30 lease (hostIPForSlot) is carved from the
	// right /16 on this box. The setter is documented as
	// "exactly once at boot, before any Acquire" (see
	// pkg/fcvm/alloc.go SetHostIPBase). Wiring it HERE — after
	// LoadConfig + role gate, before registerComputeNode —
	// guarantees the order: any Wake path that calls Acquire
	// runs after the setter returns. The default branch (empty
	// HostBridgeCIDR) falls through to api.DefaultHostBridgeCIDR
	// (10.100.0.0/16) so single-host dev keeps its previous
	// allocation.
	var parsedBridge netip.Prefix
	bridgeCIDR := strings.TrimSpace(cfg.ComputeNode.HostBridgeCIDR)
	if bridgeCIDR == "" {
		parsedBridge = api.DefaultHostBridgeCIDR()
	} else {
		var perr error
		parsedBridge, perr = netip.ParsePrefix(bridgeCIDR)
		if perr != nil {
			// LoadConfig already rejected malformed CIDRs via
			// validateHostBridgeCIDR. This branch exists only as a
			// safety net for the api-default fallback path — the
			// default is a constant known-good.
			return fmt.Errorf("vmmd: bridge CIDR %q unparseable (load-time validator missed it): %w", bridgeCIDR, perr)
		}
	}
	fcvm.SetHostIPBase(parsedBridge.Masked().Addr())
	// Mirror the bridge base into the per-netns default-route. The
	// slot allocator reserves the .1 (see pkg/fcvm/alloc.go), so the
	// next-hop for every per-VM netns is the .1 of the same /16 we
	// just seeded the allocator with. This is a one-shot boot-time
	// write — the EgressPolicyChanged pg_notify reload path
	// (cmd/vmmd/egress_watcher.go) does NOT touch the bridge IP; it
	// only re-renders the nftables ruleset from compile-time
	// defaults. The setter is invoked exactly once per process.
	netns.SetDefaultHostBridgeIP(parsedBridge.Masked().Addr().Next())
	listenTarget := cfg.ResolveListenTarget()
	// targetURL is the DIAL target schedd/gatewayd use to reach
	// this vmmd. Distinct from listenTarget (the bind address):
	// a bind like tcp://0.0.0.0:50051 listens on all interfaces,
	// but it is NOT a routable dial target — schedd/gatewayd
	// would dial 0.0.0.0:50051 and resolve to the local host,
	// not the second box. ResolveTargetURL prefers an explicit
	// [compute_node].target_url (the operator's routable FQDN),
	// then falls back to tcp://+overlay_ip+:50051, then to the
	// unix socket for single-box. See
	// docs/runbooks/multi-host-rollout.md §3.5 for the operator
	// warning. We also log a Warn at startup if the resolved
	// targetURL is a tcp:// form AND it matches the bind form
	// (the most common re-introduction of the conflation is
	// "set listen_addr to the FQDN" without a separate
	// target_url — works on dev but routes to wrong host on
	// a multi-box fleet).
	targetURL := cfg.ResolveTargetURL()
	if listenTarget == targetURL && strings.HasPrefix(targetURL, "tcp://") {
		log.Warn("vmmd: target_url equals listen_addr — schedd/gatewayd will dial the same string you bind to; set [compute_node].target_url to a routable FQDN for multi-box routing to land on this host",
			"target_url", targetURL, "listen_addr", listenTarget)
	}
	log.Info("config", "listen_addr", listenTarget, "target_url", targetURL, "socket", cfg.SocketPath, "kernel_key", cfg.KernelKey,
		"kernel_path_legacy", cfg.KernelPath,
		"metrics_addr", cfg.MetricsAddr)

	// Slice-3 / ADR-053: the node signing key is loaded once at
	// startup and reused in two places — the per-node
	// compute_node_keys upsert (right after registerComputeNode)
	// and the capacity publisher wiring below. Declared at
	// function scope so both call sites see the same key bytes
	// without re-reading the file. nil means pre-slice-3 mode
	// (no node.key on disk); the publisher emits unsigned
	// reports and legacy schedd accepts them per ADR-016.
	var nodeKey *ecdsa.PrivateKey
	var nodeKeyID string

	// Fill in host-key defaults if a test passed a zero-value runDeps
	// without these. The other deps (configPath, detectFC, listen) are
	// not defaulted here — they're test seams where nil is meaningful
	// (e.g. TestRun_BadConfigPath passes configPath = a directory).
	if deps.loadHostKey == nil {
		deps.loadHostKey = secretbox.LoadHostKey
	}
	if deps.loadHostKeys == nil {
		deps.loadHostKeys = secretbox.LoadHostKeys
	}
	if deps.genAndSaveKey == nil {
		deps.genAndSaveKey = secretbox.GenerateAndSaveHostKey
	}
	if deps.writeRecipient == nil {
		deps.writeRecipient = secretbox.WriteRecipientFile
	}

	// Snapshots are pinned to the running Firecracker version (ADR-005);
	// detect it so restore only loads compatible snapshots and everything
	// else cold boots.
	fcVersion, err := deps.detectFC(ctx)
	if err != nil {
		log.Warn("could not detect firecracker version; treating all snapshots as stale", "err", err)
	}
	// Issue #96 / ADR-025 axis 2 (PR #116): derive the canonical
	// StorageBackend key for the kernel artifact from the detected
	// FC version. Operators may pin a specific key via vmmd.toml
	// (cfg.KernelKey); when unset we fall back to the version-keyed
	// form sched.KernelKey(fcVersion). The deprecated cfg.KernelPath
	// still flows into the log line so an operator can spot drift
	// between the two during the migration window.
	//
	// When fcVersion is empty (the FC-detect-failure warning path
	// pinned by TestRun_FCDetectFailureIsWarning), we leave cfg.KernelKey
	// empty and let the rest of startup proceed — every snapshot will
	// be marked stale and every wake will cold-boot, which is the
	// correct cold-boot-always-works behaviour (ADR-005).
	if cfg.KernelKey == "" && fcVersion != "" {
		cfg.KernelKey = sched.KernelKey(fcVersion)
	}

	// Host-key lifecycle (ADR-020 / spec §11 G2). Without this, the
	// Manager refuses to wake any app that PUT a secret (Manager.Wake
	// returns ErrNoHostKey). vmmd is the only writer to the on-disk
	// key — apid reads the public recipient to seal, builderd reads
	// it to seal build-time env, and the wake path inside vmmd unseals
	// with the private identity. The first-boot branch generates a
	// fresh X25519 identity; the restart branch loads the existing
	// one and re-emits the public recipient file (idempotent).
	hostID, keyPath, pubPath, err := loadOrGenerateHostIdentity(deps,
		envOr("FAAS_HOST_KEY_PATH", secretbox.DefaultHostKeyPath),
		envOr("FAAS_HOST_AGE_RECIPIENT_PATH", secretbox.DefaultHostAgeRecipientPath),
	)
	if err != nil {
		return err
	}

	// Issue #316 / ADR-057: load BOTH the current AND previous
	// host.age identities (during the 30-day rotation overlap
	// window) so the Manager can unseal envelopes sealed under
	// either key. The first-boot / restart single-identity path
	// above already wrote host.age.pub from the current identity,
	// so the apid-side sealing endpoint keeps working unchanged.
	hostIdentities, err := loadHostIdentities(deps, filepath.Dir(keyPath))
	if err != nil {
		return fmt.Errorf("vmmd: load host identities (%s): %w", filepath.Dir(keyPath), err)
	}

	// Issue #98 / ADR-028: vmmd self-registers in compute_nodes
	// before the gRPC listener binds. Fail-closed: if the upsert
	// fails (Postgres down, schema drift), vmmd exits rather than
	// serving traffic with no identity. The legacy default-local
	// path (NodeName empty) skips the DB entirely — no migration
	// is required on a fresh single-box dev install beyond what
	// already exists.
	var nodeID string
	var pool *pgxpool.Pool
	var store state.Store
	if cfg.ComputeNode.NodeName != "" {
		dbURL := cfg.DBURL
		if dbURL == "" {
			dbURL = envOr("FAAS_VMMD_DBURL", "")
		}
		if dbURL == "" {
			return errors.New("vmmd: [compute_node].name set but [db_url] (or FAAS_VMMD_DBURL) is empty")
		}
		// Issue #938 / PR-A Blocker 2: the original code was
		// `pool, err := deps.openDB(ctx, dbURL)` — Go 1.22's shadowing
		// rules treated `err` as already declared (from the outer
		// runWithDeps scope) and redeclared `pool` in this inner
		// scope, leaving the outer `pool` at line 554 nil. The node-
		// verifier wiring at line ~882 then constructed a
		// PGNodeLoader(nil) and tripped the "pgNodeLoader has nil
		// pool" guard at the first Refresh. Assign to the outer
		// `pool` (and `err`) by using `=` instead of `:=` so the
		// verifier sees the live pool.
		var err error
		pool, err = deps.openDB(ctx, dbURL)
		if err != nil {
			return fmt.Errorf("vmmd: open db for self-registration: %w", err)
		}
		defer pool.Close()
		store = deps.openStore(pool)
		cn, err := registerComputeNode(ctx, store, cfg.ComputeNode, targetURL,
			func(ctx context.Context) (string, error) {
				return defaultDetectOverlayIP(ctx, cfg.ComputeNode)
			}, log, deps.scheddTarget)
		if err != nil {
			return err
		}
		nodeID = cn.ID
		// Slice-3 / ADR-053: register the public half of the
		// node signing key against the just-inserted
		// compute_nodes.id. The signing key itself is loaded
		// further down at the capacity publisher wiring, so
		// call site reads it once into a local var first.
		// Doing the upsert here — inside the same scope as
		// registerComputeNode — keeps both writes sharing the
		// same pool, which the defer pool.Close() then cleans
		// up. Fail-closed: an upsert failure is a fatal startup
		// error so the daemon can't serve signed reports
		// against a registry that won't accept them.
		//
		// Pre-slice-3 / empty NodeName paths are handled by
		// registerComputeNodeKey's nil/empty guards — see the
		// comment on the function for why this is the right
		// posture (legacy schedd accepts unsigned reports per
		// ADR-016, so a missing key row is not a wire-level
		// regression).
		keyLoadedNode, keyLoadedID, keyErr := loadNodeSigningKey(cfg.NodeKeyPath)
		if keyErr != nil {
			return fmt.Errorf("vmmd: load node signing key: %w", keyErr)
		}
		nodeKey, nodeKeyID = keyLoadedNode, keyLoadedID
		if err := registerComputeNodeKey(ctx, store, nodeID, nodeKey, nodeKeyID, log); err != nil {
			return err
		}
	}

	cbm := fcvm.NewColdBootMetrics()
	// PR #470-FU-B (issue #470): the framework-ready receiver
	// needs the vmmd_guest_framework_warmup_seconds histogram
	// wired so the MarkInstanceFrameworkReady receipt can
	// observe per-runner warmup durations. nil-safe on the
	// Manager side, so a producer binary that doesn't wire
	// metrics still runs.
	frm := fcvm.NewFrameworkReadyMetrics()
	dsm := fcvm.NewDiskMetrics()
	// ADR-098 C11: wake-phase histogram (vmmd_wake_phase_duration_seconds).
	// Mirrors frm / cbm — dedicated per-vmmd registry, mounted
	// alongside on the cmd-side mux below.
	wpm := fcvm.NewWakePhaseMetrics()
	// #96 / ADR-025 axis 2: vmmd publishes the mem blob via the configured
	// StorageBackend after a successful Snapshot, and resolves it back
	// from the key on Restore. The env-driven fork (FAAS_STORAGE_BACKEND)
	// routes the same call sites through a remote OCI distribution-spec
	// backend when the operator sets one up.
	storageBackend, err := storage.BackendFromEnv()
	if err != nil {
		return fmt.Errorf("vmmd: %w", err)
	}
	if envOr("FAAS_STORAGE_BACKEND", "local") == "oci" {
		log.Info("vmmd: storage backend = oci", "registry", envOr("FAAS_OCI_REGISTRY", ""))
	} else {
		log.Info("vmmd: storage backend = local", "fc_root", envOr("FAAS_STORAGE_ROOT", "/srv/fc"))
	}
	// issue #517 / PR-C / ADR-064 — Ops constructed ABOVE the
	// Manager wiring so the wake-timeline fan-out (vmmd's canonical
	// emit site for wake.readiness_200) can capture them at
	// JailerVMM construction. Hoisted from the listener block
	// below; same single-registry pattern as every other daemon.
	wire.BootStamps(ctx, "vmmd", ops)
	wire.RegisterDefaultOps(ops)
	// ADR-054 acceptance: wire the LocalCacheBackend observer so
	// stale-fallback serves on the cold-boot Restore path emit
	// `vmmd_storage_cache_stale_fallback_total`. vmmd is the
	// primary emitter on the cold-boot path; imaged emits only on
	// the build/GC paths. Uses storage.AsCacheBackend so the
	// observer attaches even when the BackendFromEnv shape changes
	// (a future metrics wrapper, router-encloses-cache, etc.). Nil
	// result is expected on single-box local deploys — the cache is
	// opt-in there.
	if cacheBE := storage.AsCacheBackend(storageBackend); cacheBE != nil {
		cacheBE.SetObserver(storage.LogCacheObserver{
			Logger: log,
			Next: storage.FuncCacheObserver(func() {
				ops.StorageCacheStaleFallback().Inc()
			}),
		})
	}
	// Issue #1054: vmmd is the node-local worker host in the current
	// compute-only topology. Unlike schedd, vmmd is installed on every
	// compute box, already owns that box's StorageBackend/cache, and has
	// the self-registered node ID in hand. The local backend is intentionally
	// excluded: two hosts can both have /srv/fc while sharing no bytes, so
	// marking local-only reads as replicas would create false-ready rows.
	if nodeID != "" && strings.EqualFold(os.Getenv("FAAS_STORAGE_BACKEND"), "oci") {
		if storage.AsCacheBackend(storageBackend) == nil {
			return errors.New("vmmd: OCI snapshot fan-out requires the local read-through cache")
		}
		replicaStore, ok := store.(state.SnapshotReplicaStore)
		if !ok {
			return errors.New("vmmd: OCI snapshot fan-out requires a snapshot replica store")
		}
		fanoutRegion := ""
		if node, regionErr := store.ComputeNodeByID(ctx, nodeID); regionErr != nil {
			log.Warn("vmmd: snapshot fan-out region lookup failed", "node_id", nodeID, "err", regionErr)
		} else if node.Region != nil {
			fanoutRegion = strings.TrimSpace(*node.Region)
		}
		fanoutMetrics, metricErr := snapshothipd.NewPrometheusMetrics(ops.Registry(), fanoutRegion)
		if metricErr != nil {
			return fmt.Errorf("vmmd: register snapshot fan-out metrics: %w", metricErr)
		}
		fanout := snapshothipd.New(replicaStore, storageBackend, nodeID, log).
			WithMetrics(fanoutMetrics)
		if raw := os.Getenv("FAAS_SNAPSHOT_FANOUT_INTERVAL"); raw != "" {
			interval, parseErr := time.ParseDuration(raw)
			if parseErr != nil || interval <= 0 {
				return fmt.Errorf("FAAS_SNAPSHOT_FANOUT_INTERVAL=%q: must be a positive duration", raw)
			}
			fanout.WithInterval(interval)
		}
		go func() {
			if runErr := fanout.Run(ctx); runErr != nil && ctx.Err() == nil {
				log.Error("vmmd: snapshot fan-out stopped", "err", runErr)
			}
		}()
		log.Info("vmmd: snapshot fan-out enabled", "node_id", nodeID, "interval", fanout.Interval())
	}
	// Issue #667 / ADR-078: single storeStamper adapter satisfies
	// BOTH the FrameworkReadyStamper interface (PR #470-FU-B) and
	// the TailTerminalStamper interface (PR 3 of this issue) — the
	// two receipt paths share one SQL-persistence seam so the
	// framework_ready DGRAM and the type=0x04 tail_event DGRAM
	// route to the same adapter, the same store, the same error
	// policy. stamperFromStore returns *storeStamper precisely so
	// the With*Stamper chain shares one receiver rather than
	// allocating two equivalent adapters.
	tailStamper := stamperFromStore(store, log)
	archiveSink, stopArchive := startVMMDLogArchive(ctx, log, ops)
	if stopArchive != nil {
		defer stopArchive()
	}
	jailer := fcvm.NewJailerVMM(fcvm.JailChrootBase, 30*time.Second).
		WithStorage(storageBackend).
		// Issue #309 / tier-2 DX: install the per-VMM
		// slow-subscriber callback that every ring
		// registerRing creates will fire on a full
		// subscriber channel. The closure adapts to
		// the vmmd-wide wire.OpsMetrics so the
		// apid_logs_dropped_total{reason="slow_subscriber"}
		// counter the §12 dashboard queries surfaces
		// a real rate under load. The wire registry is
		// per-daemon, so this closure runs in vmmd's
		// /metrics scrape — the schedd's filter_* path
		// lives on the schedd registry (different
		// daemon), so the closed-set guard in
		// IncLogDropped is what keeps the label space
		// consistent across both scrape targets.
		WithSlowSubscriberCallback(func() {
			ops.IncLogDropped("slow_subscriber")
		})
	if archiveSink != nil {
		jailer.WithLogEvictionCallback(archiveSink.Enqueue)
	}
	// Activity tracker (PR-B, issue #462): per-instance in-flight
	// ForwardHTTP request counter. It is shared by the gRPC server's
	// stats surface and the liveness loop so load-correlated probe misses
	// can receive the bounded infrastructure grace (issue #1267).
	activityTracker := activity.NewWithDefaults()
	mgr := fcvm.NewManager(
		wire.ExecRunner{},
		jailer,
		fcvm.Paths{Kernel: cfg.KernelKey},
		fcVersion,
		log,
		cbm,
	).WithFrameworkReady(frm).
		WithDiskMetrics(dsm).
		SetWakePhaseMetrics(wpm).
		// Issue #470 / PR #470-FU-B: attach the SQL persistence
		// seam so the framework_ready DGRAM receipt path can
		// stamp the `instances.framework_ready_at` column. A
		// small adapter wraps the pgstore SetInstanceFrameworkReadyAt
		// to the local FrameworkReadyStamper interface (we
		// don't want the Manager to depend on the full
		// pkg/state surface).
		WithFrameworkReadyStamper(tailStamper).
		// Issue #667 / ADR-078: same adapter also implements
		// the TailTerminalStamper interface so the type=0x04
		// tail_event DGRAM receipt path mirrors the
		// in-memory TailCount decrement to the SQL column.
		WithTailTerminalStamper(tailStamper)
	// Process lifecycle + liveness monitoring are daemon-scoped.
	// Wake RPC contexts are canceled when the request returns and
	// must not own either background activity.
	mgr.WithLifecycleContext(ctx)
	// Recover only unused cache names from a previous daemon, including when
	// an operator has disabled the cache. Active instance names are excluded.
	preparedCleanupCtx, preparedCleanupCancel := context.WithTimeout(ctx, 5*time.Second)
	preparedCleanupErr := fcvm.ReapPreparedNetworks(preparedCleanupCtx, wire.ExecRunner{})
	preparedCleanupCancel()
	if preparedCleanupErr != nil {
		return fmt.Errorf("vmmd: recover prepared networks: %w", preparedCleanupErr)
	}
	if err := mgr.EnablePreparedNetworks(ctx, cfg.PreparedNetworks); err != nil {
		return err
	}
	defer func() {
		if err := mgr.ClosePreparedNetworks(); err != nil {
			log.Error("vmmd: prepared network cleanup", "err", err)
		}
	}()
	jailer.WithProcessExitSink(mgr.ProcessExited)
	// Issue #554 / ADR-078 / PR review fix: wire the per-instance
	// liveness probe registry + starter so the Manager's bringUp /
	// Park hooks actually launch + cancel the probe loops. The
	// defaultCfg carries the per-plan Hobby/Pro/Scale defaults
	// (5 s period, 3 consecutive, 60 s cooldown) merged into
	// per-deployment overrides at Wake time. The starter closure
	// builds the cmd-side loop body via startLivenessLoopHelper
	// (cmd/vmmd/liveness_recv.go). sink is wired below after
	// scheddTarget is known.
	mgr.WithLivenessProbes(
		fcvm.NewLivenessRegistry(),
		fcvm.LivenessProbeConfig{
			Path:                "/healthz",
			PeriodSeconds:       api.DefaultLivenessPeriodSeconds,
			ConsecutiveFailures: api.DefaultLivenessConsecutiveFailures,
			CooldownSeconds:     api.DefaultLivenessCooldownSeconds,
			IdleResetOnDestroy:  true,
		},
	).WithLivenessProbeStarter(func(ctx context.Context, instance string, slot int, deploymentID string, cfg fcvm.LivenessProbeConfig) context.CancelFunc {
		return startLivenessLoopHelper(ctx, mgr, log, instance, slot, deploymentID, cfg,
			jailer.VsockUDSSocketPath(instance), activityTracker)
	})
	mgr.SetHostIdentities(hostIdentities)
	// issue #299: wire the artifact backend the Manager uses to
	// read Grype scan sidecars at boot time. Mirrors the VMM's
	// own WithStorage wiring at line 223 above; the VMM uses
	// storage to materialize snapshot blobs while the Manager
	// uses it to fetch the per-runtime scan sidecar. Both share
	// the same backend (the production PrefixRouter rooted at
	// /srv/fc).
	mgr.WithStorage(storageBackend)
	// issue #517 / PR-C / ADR-064 — wire the wake-timeline fan-out
	// (pkg/events.Platform) on the VMM. vmmd is the canonical emit
	// site for wake.readiness_200 (the first 2xx probe) and a
	// corroborating observation for wake.boot_started (mirror at
	// the gRPC server boundary). nil events opts out (legacy
	// default-local path). Schedd is the canonical writer for
	// wake.boot_started — vmmd's mirror is a sanity check that the
	// boot RPC actually entered the FC bring-up path.
	vmm := mgr.VMM()
	if vmm != nil && store != nil {
		vmm.WithEvents(events.NewPlatform("vmmd", store, log, ops, nil))
	}

	// ADR-053: parent-base loopback registry. Constructed AFTER
	// WithStorage so Manager.MountParentExt4's storage.Get has a
	// backend to read from. The cap + max-age + sweep cadence come
	// from cfg (defaults via pkg/vmmdmount). Without this wiring
	// every production MountParentExt4ReadOnly RPC short-circuits
	// to vmmdmount.ErrNotFound (Manager.parentMounts nil guard) —
	// see review blocker #1.
	parentReg := vmmdmount.NewRegistry(cfg.ParentMountCap)
	mgr.SetParentMountRegistry(parentReg)
	log.Info("vmmd: parent-mount registry wired",
		"cap", cfg.ParentMountCap,
		"max_age", cfg.ParentMountMaxAge.String(),
		"sweep_interval", cfg.ParentSweepInterval.String())

	// Reap microVMs orphaned by a previous vmmd. Firecracker children
	// do not die with vmmd (they are jailed into faas-tenant.slice, a
	// sibling cgroup), but the live-instance map that makes them
	// reachable is in-memory — so after a restart they burn tenant RAM
	// while nothing can route to them. Measured on a production node
	// on 2026-09-04: 23 such VMs, oldest 3.7 days, 5.3 GB.
	//
	// Gated on durable state, never on "vmmd restarted". On that same
	// node 2 of the 25 running VMs were instances schedd still
	// considered live (one RUNNING and serving, one mid-SNAPSHOTTING);
	// an ungated sweep would have killed a customer's VM. A nil store
	// (default-local / tests) means there is no durable view to gate
	// on, so the sweep is skipped entirely rather than run blind.
	if store != nil {
		rep, err := fcvm.ReapOrphanedJails(ctx, fcvm.ReapOptions{
			JailRoot: jailer.JailRoot(),
			Runner:   wire.ExecRunner{},
			Log:      log,
			IsLive: func(ctx context.Context, instanceID string) (bool, error) {
				ins, err := store.InstanceByID(ctx, instanceID)
				if err != nil {
					// A row that is genuinely gone is not live;
					// anything else is unknown and must not
					// authorise a kill.
					if errors.Is(err, state.ErrNotFound) {
						return false, nil
					}
					return false, err
				}
				// state.IsLive is the documented single source of
				// truth for the live set, so a future state added
				// there is honoured here without a second edit.
				return state.IsLive(ins.State), nil
			},
		})
		if err != nil {
			log.Warn("vmmd: orphan reap failed", "err", err)
		} else if rep.Scanned > 0 {
			log.Info("vmmd: orphan reap complete",
				"scanned", rep.Scanned, "reaped", rep.Reaped,
				"skipped_live", rep.SkippedLive,
				"skipped_young", rep.SkippedYoung,
				"skipped_unknown", rep.SkippedUnknown)
		}
	}

	// Orphan sweep — schedule via a context-bound goroutine that
	// exits cleanly on ctx.Done. Mirrors the schedd watchdog tick
	// pattern (1s tick + KillStuck), adapted to the configurable
	// sweep interval. A tick that fires during shutdown exits
	// harmlessly because Registry.SweepOrphans is safe on a
	// partially-drained map.
	sweepCtx, sweepCancel := context.WithCancel(ctx)
	defer sweepCancel()
	go runParentMountSweep(sweepCtx, parentReg, cfg.ParentSweepInterval, log)
	// Shutdown sweep — registered as a defer BEFORE the gRPC
	// GracefulStop so a late RPC still gets serviced and the
	// registry is empty when vmmd exits. Defers run LIFO, so
	// this fires AFTER gsrv.GracefulStop + httpSrv.Shutdown —
	// correct order (no late caller waiting on a mountpoint
	// while we sweep it).
	defer func() {
		if n := parentReg.SweepAll(ctx, log); n > 0 {
			log.Info("vmmd: shutdown parent-mount sweep", "n", n)
		}
	}()

	// Ops metrics are constructed above (hoisted for the vmmd's
	// wake-timeline fan-out — same single-registry pattern as
	// every other daemon). The listener block below uses the same
	// `ops` variable for the metrics HTTP handler.
	// issue #299: wire the OpsMetrics the Manager's scan check
	// feeds per-severity finding counts into (vmmd_trivy_image_vulns_total{image, severity}).
	// The counter is pre-instantiated at boot on every daemon's
	// single-registry OpsMetrics (memory note wire/OpsMetrics),
	// so this call is the vmmd-side producer wiring only — no new
	// registration, no new listener.
	mgr.SetImageScanMetrics(ops)

	// Wave 0 PR-C / ADR-047: vmmd becomes a gRPC client for the
	// first time. The AdvisoryClient dials /run/faas/apid.sock to
	// forward guest-init fanotify batches. Empty FAAS_APID_ADVISORY_SOCK
	// disables (matches apid's explicit-empty pattern); nil client
	// short-circuits Manager.ForwardStatelessAdvisory to a no-op.
	//
	// Mega-PR B: pass `ops` so the AdvisoryClient can increment
	// stateless_advisory_batches_emitted_total{result} on every
	// Forward outcome. The accessor is nil-receiver safe, so a
	// nil ops is also a clean no-op (kept for symmetry / unit
	// tests that don't wire metrics).
	advisoryTarget := envOr("FAAS_APID_ADVISORY_SOCK", "unix:///run/faas/apid.sock")
	var advisoryCli *vmmdgrpc.AdvisoryClient
	if advisoryTarget != "" {
		advisoryCli = vmmdgrpc.NewAdvisoryClient(advisoryTarget, log, ops)
		mgr.SetAdvisoryClient(advisoryCli)
		log.Info("vmmd: stateless advisory client wired", "target", advisoryTarget)
	}

	// PR #470-FU-B (issue #470): the host-side DGRAM recv loop
	// for the framework-ready signal. Soft-fatal on bind failure
	// in BOTH directions:
	//
	//   - non-linux (Mac dev box): the stub returns an error; we
	//     log at Warn and continue so the dev workflow isn't gated.
	//   - linux WITHOUT the AF_VSOCK kernel module loaded (CI unit
	//     test container, build hosts without /dev/vsock): bind
	//     returns EADDRNOTAVAIL. The unit-test seam must keep
	//     running — the warm-tier path is dormant but the rest of
	//     vmmd (gRPC, host key, capacity publisher) still needs to
	//     come up so cmd/vmmd tests can exercise it.
	//
	// The production-only vsock path is opt-in: an operator running
	// the full vmmd on a host whose kernel supports vsock would
	// see the receiver come up. If bind fails on a real production
	// host, the warm-tier migration is silently dropped — but the
	// gRPC server still serves readiness, and the watchdog tick
	// (memory `schedd-watchdog-tick`) is unaffected.
	recv, err := StartFrameworkReadyReceiver(ctx, log, mgr)
	if err != nil {
		log.Warn("vmmd: framework_ready receiver unavailable", "err", err, "goos", runtime.GOOS)
		recv = nil
	}
	if recv != nil {
		// PR-C §3,§4 (issue #463 / ADR-069 / ADR-071):
		// wire the sidecar events emitter onto the
		// receiver. Production uses
		// SidecarEventsThroughPlatform which bundles the
		// canonical pkg/events.Platform (events.NewPlatform
		// constructed up front by the cmd main loop), the
		// OpsMetrics incrementer, and the audit-store
		// shibboleth for init_failed (failure_class:
		// user_error, AC #1). nil = no-op emitter so a
		// receiver that came up but the wiring didn't (e.g.
		// default-local run without a state.Store) keeps
		// working without audit drift.
		platform := events.NewPlatform("vmmd", store, log, ops, nil)
		recv.WithSidecarEmitter(&SidecarEventsThroughPlatform{
			Platform: platform,
			Metrics:  ops,
			Store:    store,
			// PR-B AC #1 (issue #463 / ADR-069): wire the
			// state.Store as the deploymentFailer so a
			// non-zero init sidecar exit flips the
			// deployments row to status='failed' with
			// error_code = api.CodeInitSidecarFailed at the
			// dispatch site (no pg_notify bridge). The store
			// satisfies the narrowed deploymentFailer
			// interface via SetDeploymentFailed
			// (pkg/state/store.go:1197, ADR-021).
			Failer: store,
			Log:    log,
		})
		defer recv.Close()
	}
	log.Info("vmmd ready", "fc_version", fcVersion, "max_slots", fcvm.MaxSlots,
		"uid_lo", fcvm.JailUIDBase, "uid_hi", fcvm.JailUIDMax,
		"host_key_path", keyPath, "recipient_path", pubPath,
		"recipient", hostID.Recipient().String())
	// ADR-056: handshake-layer NodeVerifier. Single-box vmmd
	// (cfg.ComputeNode.NodeName == "") does NOT construct a
	// verifier — stdlib trust alone runs. Multi-box vmmd
	// constructs a PG-backed verifier, refreshes once at
	// startup, and pumps compute_node_changed notifications into
	// a drain goroutine for the lifetime of the daemon.
	var nodeVerifier *wire.PGNodeVerifier
	if nodeID != "" {
		nodeVerifier = wire.NewPGNodeVerifier(wire.NewPGNodeLoader(pool), log)
		// Drive a synchronous startup Refresh so the first
		// handshake after listen sees a populated snapshot.
		if _, rerr := nodeVerifier.Refresh(ctx); rerr != nil {
			return fmt.Errorf("vmmd: node verifier startup refresh: %w", rerr)
		}
		go func() {
			ch, lerr := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyComputeNodeChanged}, log)
			if lerr != nil {
				log.Error("vmmd: node verifier LISTEN failed", "err", lerr)
				return
			}
			if rerr := nodeVerifier.Run(ctx, ch); rerr != nil && !errors.Is(rerr, context.Canceled) {
				log.Error("vmmd: node verifier exited", "err", rerr)
			}
		}()
	}

	// The compute-node registry describes compute/service peers, but it does
	// not contain the control-plane schedd leaf. vmmd has both directions on
	// this boundary: its server accepts schedd, while its capacity publisher
	// dials schedd and must validate schedd's server certificate. Keep those
	// identities explicit instead of applying the compute registry to the
	// control-plane certificate.
	var serverVerifier wire.NodeVerifier = nodeVerifier
	var scheddVerifier wire.NodeVerifier
	if nodeVerifier != nil {
		controlPlaneVerifier := wire.NewInmemNodeVerifier()
		controlPlaneVerifier.Set([]string{"schedd.faas"})
		// The compute_nodes registry contains vmmd identities, but local
		// service clients use role-specific leaves and are intentionally not
		// rows in that registry. Keep the server-side CN gate strict while
		// allowing the three services that legitimately call vmmd directly.
		serviceVerifier := wire.NewInmemNodeVerifier()
		serviceVerifier.Set([]string{
			"builderd.faas",
			"gatewayd.faas",
			"imaged.faas",
			"schedd.faas",
		})
		serverVerifier = wire.NewAnyNodeVerifier(nodeVerifier, controlPlaneVerifier, serviceVerifier)
		scheddVerifier = controlPlaneVerifier
	}

	serverTLS, err := cfg.LoadServerTLSWithVerifier(serverVerifier)
	if err != nil {
		return fmt.Errorf("vmmd: load server TLS: %w", err)
	}
	// Issue #900 follow-up: surface the leaf-CN-vs-registered-name
	// gap at startup so an operator running `gregale pki init &&
	// systemctl start faas-vmmd` sees the mismatch before traffic
	// starts to fail. The Warn is advisory-only — vmmd still starts
	// and serves traffic; the verifier (which runs AFTER stdlib
	// trust) only fails on the handshake with schedd / gatewayd.
	// Triggered only when the operator's [compute_node].name was
	// set (so auto-append will fire) AND serverTLS is non-nil
	// (the verifier path is installed). Other paths are silent.
	warnIfPkiCNMismatch(cfg.ComputeNode, serverTLS, log)
	scheddClientTLS, err := cfg.LoadScheddClientTLSWithVerifier(scheddVerifier)
	if err != nil {
		return fmt.Errorf("vmmd: load schedd client TLS: %w", err)
	}
	deps.scheddClientTLS = scheddClientTLS

	// ADR-052 §5 / PR-E: route the server + schedd-client loads
	// through the WithReload factories so a SIGHUP-driven reload
	// (`gregale pki rotate` → `kill -HUP $(pidof faas-vmmd)`)
	// swaps material on the next inbound / outbound handshake
	// without restart. serverRotator / scheddClientRotator hold
	// the live *tls.Config; Listen's tls.Config + the per-handshake
	// stdlib callback consult the rotator's Reload closure at
	// handshake time.
	serverRotator := wire.NewTLSRotator(serverTLS)
	scheddClientRotator := wire.NewTLSRotator(scheddClientTLS)
	// Replace the boot-time configs with reload-wrapped variants
	// so Listen + ServerCreds + the schedd dial go through the
	// rotator's Reload path. Listen and the publisher dial cache
	// *tls.Config pointers — the rotator's Get() is observed on
	// subsequent operations after Set.
	serverTLS, err = cfg.LoadServerTLSWithPrefixAndVerifierAndReload(serverVerifier, serverRotator.Reload(serverTLS))
	if err != nil {
		return fmt.Errorf("vmmd: load server TLS (reload): %w", err)
	}
	serverRotator.Set(serverTLS)
	scheddClientTLS, err = cfg.LoadScheddClientTLSWithPrefixAndVerifierAndReload(scheddVerifier, scheddClientRotator.Reload(scheddClientTLS))
	if err != nil {
		return fmt.Errorf("vmmd: load schedd client TLS (reload): %w", err)
	}
	scheddClientRotator.Set(scheddClientTLS)
	deps.scheddClientTLS = scheddClientTLS
	lis, err := deps.listen(ctx, listenTarget, serverTLS, cfg.OwnerUser)
	if err != nil {
		return fmt.Errorf("vmmd: listen %s: %w", listenTarget, err)
	}
	// Issue #571 PR-A2: construct the /readyz probe (kvm +
	// firecracker + gRPC). The gRPC bound signal flips to true
	// here — deps.listen() succeeded, so the unix socket is
	// bound and schedd can dial. BuildReadinessProbe also does
	// a one-shot /dev/kvm open + firecracker LookPath; both
	// succeed here or the daemon exits via the
	// BuildReadinessProbe call's failure path (the probe is
	// ready regardless).
	vmmdProbe, grpcBound := BuildReadinessProbe()
	vmmdProbe.SetReadyObserver(func(ready bool, reason string) {
		ops.MarkReady("vmmd", ready, reason)
	})
	// NOTE: grpcBound.MarkBound() is intentionally NOT called
	// here — see cmd/vmmd/readiness.go BuildReadinessProbe for
	// why. The flip must fire inside the serve goroutine, just
	// before gsrv.Serve, so a panic during the ~90 lines of
	// setup below cannot leave /readyz reporting ready while
	// no gRPC server is actually running (PR #1091 review
	// Finding 5).
	// CPU cache: a per-instance rate + accumulator over cgroup
	// usage_usec, fed by runCPUSampleLoop below and consumed by
	// vmmdgrpc.Server.Stats. issue #279 / PR-B. nil-safe so
	// tests that don't care about CPU can pass a fresh
	// cpustats.NewWithDefaults() and skip the sample loop
	// entirely via runCPUSampleInterval=0.
	cpuCache := cpustats.NewWithDefaults()
	// Netstats cache: per-instance byte-counter over root-side
	// vethHost.rx_bytes, fed by runNetworkEgressPoll below and
	// consumed by vmmdgrpc.Server.Stats as the net_tx_bytes
	// wire field. ADR-046 (step 7). nil-safe so tests can pass
	// nil to vmmdgrpc.NewWithCPUAndNet and skip the sample
	// loop entirely.
	netCache := netstats.NewWithDefaults()
	gsrv := grpc.NewServer(append(
		wire.ServerCredsOrEmpty(serverTLS),
		wire.TraceServerOptions()...,
	)...)
	impl := vmmdgrpc.NewWithCPUAndNetAndActivity(signalAdapter{mgr}, ops, fcVersion, log, cpuCache, netCache, activityTracker).
		WithFlowCounter(flowcount.NewReader(wire.ExecRunner{})).
		WithNodeID(nodeID)
	// issue #517 / PR-C / ADR-064 — wire the wake-timeline fan-out
	// on the gRPC server. vmmd is the corroborating-observation
	// source for wake.boot_started (mirror at the gRPC server
	// boundary) and the canonical emit site for wake.readiness_200
	// (the first 2xx probe). nil events opts out (legacy default-
	// local path).
	if store != nil {
		impl.WithEvents(events.NewPlatform("vmmd", store, log, ops, nil))
		impl.WithMigrationStore(store)
	}
	impl.Register(gsrv)
	defer func() {
		if err := impl.Close(context.WithoutCancel(ctx)); err != nil {
			log.Warn("vmmd: stream bridge cleanup during shutdown failed", "err", err)
		}
	}()

	// Optional /metrics endpoint.
	var httpSrv *http.Server
	if cfg.MetricsAddr != "" {
		mux := newMetricsMux(ops, cbm, frm, wpm, dsm)
		// Issue #571 PR-A2: /healthz + /readyz on the metrics mux
		// (operator-side, loopback-only) for the LB scrape and
		// on-box monitoring. Source of truth is the same
		// BuildReadinessProbe wired at the deps.listen site
		// above — single source between /readyz body and the
		// daemon_ready gauge (issue #586 / ADR-129).
		wire.ControlMuxLite(mux, vmmdProbe.ReadyFunc(), vmmdProbe.ReasonFunc())
		// ADR-122: apply the canonical metrics-listener shape —
		// RT/WT/IT/MHB from cfg.MetricsListener (cfg → constant
		// fallback). ReadHeaderTimeout=10s stays from before ADR-122.
		readTimeout, writeTimeout, idleTimeout, maxHeaderBytes := cfg.MetricsListener()
		httpSrv = &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second, // match schedd; guards the metrics endpoint against Slowloris
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    int(maxHeaderBytes),
		}
		go func() {
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("metrics http", "err", err)
			}
		}()
		log.Info("metrics listening", "addr", cfg.MetricsAddr)
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("grpc listening", "addr", listenTarget, "service", vmmdpb.Vmmd_ServiceDesc.ServiceName)
		// Flip the gRPC bound signal immediately before
		// gsrv.Serve so /readyz reflects "the gRPC server is
		// actually running" — not merely "the unix socket is
		// bound" (PR #1091 review Finding 5).
		grpcBound.MarkBound()
		serveErr <- gsrv.Serve(lis)
	}()

	// PR-E egress-deny counter poll adapter. Reads `nft list counters`
	// every EgressPollInterval (15 s by default) and emits the per-CIDR
	// delta as <daemon>_egress_deny_total{cidr,family}. Tests inject
	// startEgressPoll to skip the loop or capture the seam args; nil
	// means "start the production goroutine". The interval is
	// parameterised so a unit test can drive the loop at sub-second
	// cadence (see cmd/vmmd/poller_test.go::TestRunEgressPoll_DeltaOnSecondTick).
	interval := EgressPollInterval
	if deps.egressPollInterval != nil {
		interval = *deps.egressPollInterval
	}
	pop := deps.popCounters
	if pop == nil {
		pop = netns.PopCounters
	}
	if deps.startEgressPoll != nil {
		deps.startEgressPoll(ctx, ops, pop, interval, log)
	} else {
		go runEgressPoll(ctx, ops, pop, interval, log)
	}
	// C1: the global poll above preserves the existing per-CIDR series;
	// this companion loop reads each live namespace so those same counters
	// can be attributed to an app and rolled up by deny class.
	popInstance := deps.popInstanceCounters
	if popInstance == nil {
		popInstance = netns.PopCountersInNetns
	}
	go runEgressDeniedPoll(ctx, mgr, ops, popInstance, interval, log)

	// CPU sample loop (issue #279 / PR-B): drives the cpustats
	// cache at 250 ms cadence — half the schedd poller's
	// 200 ms so a fresh rate is always ready when schedd dials
	// Stats. 250 ms matches the spike-capture window the
	// cgroupstats metal test was written against
	// (pkg/sched/instancestats/poller_metal_test.go:153). On
	// non-Linux hosts cgroupstats.Sample returns ok=false; the
	// loop is a no-op there, leaving cpuCache cold.
	go runCPUSampleLoop(ctx, cpuCache, log)
	// Network egress poll loop (ADR-046, step 7): reads
	// /sys/class/net/<vethHost>/statistics/rx_bytes for every
	// live instance on a 250 ms tick and feeds netstats.Cache.
	// The schedd poller pulls the value via Stats at its own
	// 200 ms cadence; meterd's sampler appends to
	// usage_minutes.net_tx_bytes additively per minute.
	go runNetworkEgressPoll(ctx, mgr, netCache, ops, nil, nil, nil, 0, log)

	// Tier A5 (ADR-066) live-migration lease sweeper. Drops
	// tracker entries whose lease has expired so a dead vmmd's
	// orphaned leases don't leak memory across long-running
	// processes. Started unconditionally — the tracker is
	// empty in non-migration vmmds and listExpired returns
	// an empty slice fast.
	go impl.LeaseExpiryLoop(ctx)

	// ADR-025 axis 5: vmmd publishes live capacity (live_count,
	// leased_count, used_mb, ram_headroom_mb, vcpu_busy) to
	// schedd on a 1 s cadence. The publisher only runs on the
	// multi-node path (NodeName set, nodeID non-empty). The
	// single-box default-local vmmd skips the loop entirely,
	// preserving backward compatibility (ADR-005 cold-boot).
	//
	// residentFn is the leakcheck seam — leakcheck.ResidentBytes
	// on Linux, nil on non-Linux dev boxes (the chooser then
	// falls back to the store sum).
	if nodeID != "" && deps.scheddTarget != "" {
		interval := CapacityInterval
		if deps.capacityInterval != nil {
			interval = *deps.capacityInterval
		}
		resident := deps.residentFn
		if resident == nil {
			resident = leakcheckResidentBytes
		}
		// Slice-3 (ADR-053): the signing key + key_id were
		// loaded at registerComputeNodeKey above so the
		// compute_node_keys row could be written in the same
		// scope as the compute_nodes row. Reuse the values
		// here for the publisher wiring; do not reload — the
		// file is mode 0400 root:root and reading it twice
		// doubles the TOCTOU surface for no benefit (the F4
		// pin is the open-fd mode check, not the count of
		// loads). The nil/empty log lines emitted by
		// registerComputeNodeKey already explain the
		// pre-slice-3 posture; the publisher's hot path only
		// cares about the key bytes + the key_id string.
		if deps.startCapacityPublish != nil {
			deps.startCapacityPublish(ctx, mgr, nodeID, cfg.ComputeNode, deps.scheddTarget, deps.scheddClientTLS, interval, resident, nodeKey, nodeKeyID, log)
		} else {
			stats := telemetryReader(func(statsCtx context.Context) (*vmmdpb.StatsResponse, error) {
				return impl.Stats(statsCtx, &vmmdpb.StatsRequest{})
			})
			go runCapacityPublish(ctx, mgr, nodeID, cfg.ComputeNode, deps.scheddTarget, deps.scheddClientTLS, interval, resident, nodeKey, nodeKeyID, log, stats)
		}
		log.Info("vmmd: capacity publisher wired", "node_id", nodeID, "target", deps.scheddTarget, "interval", interval.String())
	}

	// Issue #554 / ADR-078 / PR-review fix F2: vmmd → schedd
	// drain for the liveness-probe failure path. The per-instance
	// poll goroutine (cmd/vmmd/liveness_recv.go::livenessProbeLoop)
	// invokes Manager.ReportLivenessFailed once the
	// consecutive-failure counter reaches the per-plan N. The
	// relay dials schedd over the same gRPC channel the capacity
	// publisher uses (deps.scheddTarget + deps.scheddClientTLS),
	// calls scheddpb.ReportLivenessFailed, and ignores the
	// returned ack — schedd's Engine.DestroyForLivenessFailure
	// is the source of truth for the state transition, and the
	// vmmd-side loop has already exited on its end.
	//
	// Why a fresh dial per call: the failure is rare (default
	// 3 consecutive misses, with the plan's liveness cooldown
	// keeping the per-app rate well under one-per-second), so
	// the connection-pool cost is negligible vs. the complexity
	// of maintaining a long-lived stream on a fire-and-forget
	// path. The dial is bounded by ReportLivenessFailedCtxTimeout
	// so a wedged schedd doesn't bleed back into the poll
	// goroutine.
	//
	// Skipping on the single-box default-local path
	// (deps.scheddTarget == ""): the liveness probe loop is
	// still wired and will increment its counter, but the relay
	// is a no-op. The single-box dev loop has no schedd to
	// drain into; the operator runs the test on a multi-node
	// fleet to exercise the full path. Mirrors the capacity
	// publisher's gating above.
	if deps.scheddTarget != "" {
		mgr.WithLivenessSink(func(ctx context.Context, instanceID, reason string) {
			dialCtx, cancel := context.WithTimeout(ctx, ReportLivenessFailedCtxTimeout)
			defer cancel()
			conn, err := wire.DialContext(dialCtx, deps.scheddTarget, deps.scheddClientTLS)
			if err != nil {
				log.Warn("vmmd: liveness-failed dial failed; engine will not be notified",
					"instance_id", instanceID, "reason", reason, "err", err)
				return
			}
			defer func() { _ = conn.Close() }()
			cli := scheddpb.NewScheddClient(conn)
			if _, err := cli.ReportLivenessFailed(dialCtx, &scheddpb.LivenessFailedReport{
				InstanceId: instanceID,
				Reason:     reason,
			}); err != nil {
				log.Warn("vmmd: ReportLivenessFailed RPC failed",
					"instance_id", instanceID, "reason", reason, "err", err)
				return
			}
			log.Info("vmmd: liveness-failure drained to schedd",
				"instance_id", instanceID, "reason", reason)
		})
		log.Info("vmmd: liveness-failed relay wired",
			"target", deps.scheddTarget,
			"timeout", ReportLivenessFailedCtxTimeout.String())
	}

	// Cluster C / ADR-121: vmmd → schedd drain for the
	// workload-OOM signal. The framework_ready receiver
	// (cmd/vmmd/framework_ready_recv.go) invokes
	// Manager.ReportWorkloadOOM when a guest-init
	// cgroup.events listener detects an oom_kill on the
	// per-VM cgroup v2 leaf and emits DGRAM type=0x05.
	// The relay dials schedd over the same gRPC channel as
	// the liveness relay (deps.scheddTarget +
	// deps.scheddClientTLS), calls
	// scheddpb.ReportWorkloadOOM, and ignores the returned
	// ack — schedd's
	// Engine.DestroyForWorkloadOOMFailure is the source of
	// truth for the stamp, and the guest-init listener has
	// already exited on its end.
	//
	// Why a fresh dial per call: same rationale as the
	// liveness relay above (failure is rare, fire-and-forget,
	// connection-pool cost negligible). The dial is bounded
	// by ReportWorkloadOOMCtxTimeout so a wedged schedd
	// doesn't bleed back into the framework_ready dispatch
	// loop.
	//
	// Skipping on the single-box default-local path
	// (deps.scheddTarget == ""): mirrors the liveness
	// relay gating. The framework_ready receiver still
	// parses + dispatch type=0x05 DGRAMs (the type
	// validation is host-local), but the sink is a no-op.
	if deps.scheddTarget != "" {
		mgr.WithWorkloadOOMSink(func(ctx context.Context, instanceID string, peakMB, planMB int) {
			// Review finding #6: spawn the relay in a
			// goroutine so the framework_ready recv loop
			// returns immediately. The previous shape ran
			// the dial + RPC synchronously inside the
			// dispatchWorkloadOOM call, which is invoked
			// from the framework_ready recv loop's
			// single-threaded switch. A wedged schedd (or
			// a slow TLS handshake) would block the entire
			// DGRAM loop for the
			// ReportWorkloadOOMCtxTimeout (3s) ceiling per
			// OOM — and a fleet-wide OOM storm (10
			// instances of the same app all hitting the
			// plan cap) would queue a backlog of VMs
			// waiting for the loop to drain. The
			// goroutine shape lets the recv loop keep
			// polling; each relay runs independently. The
			// receiver's stored ctx (the closure's `ctx`
			// here) is long-lived; the goroutine respects
			// it via the dialCtx cancel propagation.
			//
			// Note: the liveness relay above still uses
			// the synchronous shape — the liveness path
			// is one-at-a-time per instance (a probe
			// cycle is ~3s, so the relay throughput is
			// bounded by the probe schedule). The
			// workload-OOM path is bursty
			// (fleet-wide OOMs from a single bad
			// customer app), so the async shape is the
			// correct fit here. A future PR can lift
			// the liveness relay to the same shape if
			// the operator's dashboard shows an
			// liveness-driven backlog.
			go func() {
				dialCtx, cancel := context.WithTimeout(ctx, ReportWorkloadOOMCtxTimeout)
				defer cancel()
				conn, err := wire.DialContext(dialCtx, deps.scheddTarget, deps.scheddClientTLS)
				if err != nil {
					log.Warn("vmmd: workload-OOM dial failed; engine will not be notified",
						"instance_id", instanceID, "peak_mb", peakMB, "plan_mb", planMB, "err", err)
					return
				}
				defer func() { _ = conn.Close() }()
				cli := scheddpb.NewScheddClient(conn)
				if _, err := cli.ReportWorkloadOOM(dialCtx, &scheddpb.ReportWorkloadOOMRequest{
					InstanceId: instanceID,
					PeakMb:     uint32(peakMB),
					PlanMb:     uint32(planMB),
				}); err != nil {
					log.Warn("vmmd: ReportWorkloadOOM RPC failed",
						"instance_id", instanceID, "peak_mb", peakMB, "plan_mb", planMB, "err", err)
					return
				}
				log.Info("vmmd: workload-OOM drained to schedd",
					"instance_id", instanceID, "peak_mb", peakMB, "plan_mb", planMB)
			}()
		})
		log.Info("vmmd: workload-OOM relay wired",
			"target", deps.scheddTarget,
			"timeout", ReportWorkloadOOMCtxTimeout.String())
	}

	// ADR-055 / Tier 1 Phase 4: the per-host egress policy watcher.
	// Subscribes to `egress_policy_changed` pg_notify and re-renders
	// the host nftables ruleset on every notification. Gated on
	// nodeID (the multi-node path) so single-box default-local vmmd
	// does NOT observe the channel — the legacy single-box install
	// has no compute_nodes row, no platform-wide migration runner,
	// and the operator's working contract is `make bootstrap` which
	// already drops the policy file into place. The watcher is the
	// multi-box hot-reload path.
	//
	// Staging defaults: /tmp/vmmd-egress-staging (mode 0755, owned
	// by the daemon's process uid). /etc/nftables.conf is the
	// canonical live path; cross-fs renames fall through atomicReplace's
	// copy+rename fallback so the daemon does not silently fail on a
	// host where /tmp is tmpfs and /etc is on ext4.
	if nodeID != "" {
		w := newEgressWatcher(log, "/tmp/vmmd-egress-staging", "/etc/nftables.conf")
		deps.egressWatcher = w
		if deps.startEgressWatcher != nil {
			deps.startEgressWatcher(ctx, log)
		} else {
			go func() {
				if err := w.Run(ctx, pool); err != nil {
					log.Error("vmmd: egress watcher exited", "err", err)
				}
			}()
		}
		log.Info("vmmd: egress watcher wired", "node_id", nodeID, "staging", "/tmp/vmmd-egress-staging", "live", "/etc/nftables.conf")
	}

	// Issue #679 / PR-A: install the SIGHUP-driven egress
	// bundle reload. The signal goroutine is serialised against
	// Wake/Park/Destroy via Manager.SetEgressOperatorBundle's
	// internal locking, so concurrent reloads + live-patches
	// cannot corrupt the operator-bundle slice. A failed reload
	// keeps the prior bundle live (best-effort, never blocks
	// signal delivery).
	hupCh := make(chan os.Signal, 1)
	signal.Notify(hupCh, syscall.SIGHUP)
	defer signal.Stop(hupCh)
	go watchEgressBundleReload(ctx, mgr, cfg.EgressOperatorAllowlist, log, hupCh)
	// ADR-119 redesign: SIGHUP-driven static egress IP bundle
	// reload. Wired on the same hupCh as the operator-allowlist
	// bundle — every SIGHUP fans out to both. The watcher
	// (cmd/vmmd/egress_static_ip_bundle.go) reads the operator
	// TOML, installs the bridge aliases, pushes the rules into
	// the host renderer (via netns.SwapActiveHostPolicy), and
	// mirrors the (account_id, customer_ip) tuples into the
	// Postgres gate table the apid PUT path reads.
	//
	// `st` is the same state.Store the rest of the daemon
	// consumes (the apid uses it for the gate read).
	go watchStaticEgressIPBundleReload(ctx, mgr, store, cfg.StaticEgressIPBundlePath, log, hupCh)
	// ADR-052 §5 / PR-E: SIGHUP-driven TLS cert rotation on the
	// same hupCh the egress-bundle reload watches. Reuses the
	// channel — each signal is consumed by both watchers — and
	// uses the best-effort failure posture WatchTLSReload pins
	// (matches watchEgressBundleReload's contract: a malformed
	// cert file does NOT brick the daemon's mTLS leg).
	serverReload := func() (*tls.Config, error) {
		return cfg.LoadServerTLSWithPrefixAndVerifierAndReload(serverVerifier, nil)
	}
	scheddClientReload := func() (*tls.Config, error) {
		return cfg.LoadScheddClientTLSWithPrefixAndVerifierAndReload(scheddVerifier, nil)
	}
	// serverHupCh + scheddHupCh get every SIGHUP (signal.Notify
	// fans the signal out to every registered channel). The
	// single hupCh above is owned by watchEgressBundleReload and
	// is consumed there; the new channels keep the tls reloads
	// independent of that path.
	serverHupCh := make(chan os.Signal, 1)
	signal.Notify(serverHupCh, syscall.SIGHUP)
	defer signal.Stop(serverHupCh)
	scheddClientHupCh := make(chan os.Signal, 1)
	signal.Notify(scheddClientHupCh, syscall.SIGHUP)
	defer signal.Stop(scheddClientHupCh)
	go wire.WatchTLSReload(ctx, log, serverHupCh, serverRotator, serverReload)
	go wire.WatchTLSReload(ctx, log, scheddClientHupCh, scheddClientRotator, scheddClientReload)

	// Heartbeat retains the §6.2 leak signal (live + leased must be 0 when idle).
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
heartbeat:
	for {
		select {
		case <-ctx.Done():
			log.Info("draining", "live", mgr.LiveCount())
			break heartbeat
		case <-tick.C:
			log.Debug("heartbeat", "live", mgr.LiveCount(), "leased", mgr.LeasedCount())
		case err := <-serveErr:
			if err != nil {
				return err
			}
		}
	}

	// Graceful shutdown — 5s deadline; M2 schedd may be holding a Connect
	// we don't want to drop before its replacement lease is acquired.
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gsrv.GracefulStop()
	if httpSrv != nil {
		//nolint:contextcheck // shutdown context must outlive caller ctx (which is already Done); detached from caller per gRPC + net/http contract.
		_ = httpSrv.Shutdown(stopCtx)
	}
	_ = lis.Close()
	// Advisory gRPC client holds the dial to /run/faas/apid.sock
	// open for ~30s of keepalive if we don't close it explicitly.
	// Idempotent at the gRPC layer (pkg/vmmdgrpc uses sync.Once).
	if advisoryCli != nil {
		_ = advisoryCli.Close()
	}
	return nil
}

// loadOrGenerateHostIdentity implements the G2 host-key lifecycle:
//
//  1. Try LoadHostKey(path).
//  2. On ErrHostKeyNotFound (first boot) → GenerateAndSaveHostKey(path).
//  3. Always WriteRecipientFile(pubPath, id) so apid / builderd have
//     a fresh public recipient to seal against on every startup.
//
// Returns the identity plus the resolved paths so the caller can log
// them. Extracted so tests can drive the lifecycle without booting
// the full gRPC + listener stack.
func loadOrGenerateHostIdentity(deps runDeps, keyPath, pubPath string) (*age.X25519Identity, string, string, error) {
	id, err := deps.loadHostKey(keyPath)
	if errors.Is(err, secretbox.ErrHostKeyNotFound) {
		id, err = deps.genAndSaveKey(keyPath)
	}
	if err != nil {
		return nil, keyPath, pubPath, fmt.Errorf("vmmd: host key (%s): %w", keyPath, err)
	}
	if err := deps.writeRecipient(pubPath, id); err != nil {
		return nil, keyPath, pubPath, fmt.Errorf("vmmd: write recipient (%s): %w", pubPath, err)
	}
	return id, keyPath, pubPath, nil
}

// loadHostIdentities loads the multi-identity unseal set for the
// Manager (issue #316 / ADR-057). During the 30-day rotation
// overlap window the operator renames host.age → host.age.previous
// and drops a new host.age; this helper returns the slice
// [current, previous] so the Manager can pass it to
// secretbox.OpenMulti and unseal envelopes sealed under either
// key. Outside the overlap window the slice has length 1 (just
// the current identity) — same shape as SetHostIdentity(id)
// modulo a slice allocation.
//
// The dir is the parent of the canonical host.age file
// (e.g. /etc/faas/secrets for the production install). vmmd
// derives the dir from the FAAS_HOST_KEY_PATH the boot-time
// loadOrGenerateHostIdentity call already validated, so this
// helper does not need a separate env-var lookup.
func loadHostIdentities(deps runDeps, dir string) ([]*age.X25519Identity, error) {
	ids, err := deps.loadHostKeys(dir)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("vmmd: loadHostKeys(%q) returned empty slice", dir)
	}
	return ids, nil
}
