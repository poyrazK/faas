// vmmrouter.go — schedd's per-compute-node VMM dial cache (issue #97 /
// ADR-025 axis 3, slice 2/3).
//
// schedd is single-leader CP (ADR-025): one process owns placement. With
// N compute_nodes, the leader needs to dial vmmd on each node's target
// URL — unix:///run/faas/vmmd.sock on the legacy default-local node,
// tcp://… on the next cluster node, and so on. The dial is a gRPC
// connection (per-target resource) that we want to amortise across the
// many wakes of a busy box.
//
// VMMRouter is the dial-once-per-target cache. Concurrent dials for the
// same target are serialised by the cache mutex; concurrent dials for
// different targets race freely (different target = different connection
// = different file descriptor). The router implements the same four
// RPCs as VMM but each method takes a nodeID first arg, so the Engine
// can route without per-node shim methods.
//
// The Engine's vmm field changes from VMM (single-box, single client)
// to RoutedVMM (multi-node). RoutedVMM satisfies the same surface as
// VMM (every method exists), but the Engine's callsites must pass the
// chosen node's ID on every call. That makes the routing intent
// explicit at the call site and keeps the router's interface narrow.

//go:generate not used — the router is hand-wired.

package sched

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RoutedVMM is the multi-node vmmd surface schedd's engine consumes.
// Each method takes nodeID as the first argument so the router can
// forward to the right per-target vmmd connection.
//
// The 10-method surface mirrors VMM verbatim (CreateColdBoot,
// CreateFromSnapshot, PauseAndSnapshot, Destroy, Ping, Stats,
// plus the Tier A5 four-phase migration set: PrepareLiveMigration,
// AdoptMigratedInstance, AcknowledgeMigration,
// CancelLiveMigration). The router's implementation looks up the
// per-node client by ID, dials on first use, and forwards. If the
// ID has no row in the cache and the router can't dial (e.g. an
// operator typo in target_url), the call returns a *api.Problem
// with Code=Capacity — the same code the ledger uses for "no
// headroom" so the gateway's 503 mapping is consistent.
type RoutedVMM interface {
	CreateColdBoot(ctx context.Context, nodeID, instance string, app AppSpec) (*WakeOutcome, error)
	CreateFromSnapshot(ctx context.Context, nodeID, instance string, app AppSpec, snap SnapshotRef) (*WakeOutcome, error)
	// PauseAndSnapshot (issue #121 / ADR-025 axis 2 slice 4) takes
	// vmstateStorageKey as a third string alongside vmstatePath and
	// storageKey. Default-local schedd sends the empty value so vmmd's
	// host-path branch is taken bit-for-bit; remote-node schedd sends
	// state.SnapVMStateKey(deploymentID).
	PauseAndSnapshot(ctx context.Context, nodeID, instance, vmstatePath, storageKey, vmstateStorageKey string) (SnapshotBytes, error)
	// WarmSnapshot (issue #470 / PR #470-FU-A) is the warm-tier
	// twin of PauseAndSnapshot. Always storage-backend-only
	// (warm captures have no legacy host-path fallback). The
	// router resolves the per-node vmmd by nodeID and forwards
	// to VMMClient.WarmSnapshot. Returns Capacity on an unknown
	// nodeID to keep the failure semantics consistent with the
	// rest of the routed surface.
	WarmSnapshot(ctx context.Context, nodeID, instance, storageKey, vmstateStorageKey string) (SnapshotBytes, error)
	// FrameworkReady (issue #470 / PR #470-FU-B) is the vmmd-side
	// receipt of the guest-init "framework ready" vsock DGRAM
	// signal (port 1027). Schedd itself doesn't call this — the
	// cmd/vmmd DGRAM host recv loop does — but the routed
	// interface keeps it on the same surface as the other vmmd
	// RPCs so a future test or a multi-node vmmd mesh can wire
	// it through the router's per-node dial table. The default
	// router implementation forwards to cli.FrameworkReady.
	FrameworkReady(ctx context.Context, nodeID, instance string, warmupMs int64) error
	Destroy(ctx context.Context, nodeID, instance string) error
	// StopInstanceOnNode (M-2 / ADR-138 §Decision 1) is the
	// graceful signal-grace-SIGKILL sequence, routed by nodeID.
	// Named with OnNode suffix so test fakes that satisfy both
	// VMM (the per-target client) and RoutedVMM (the multi-node
	// router) can keep the VMM-shape StopInstance without
	// inserting a nodeID arg that VMM doesn't have. The
	// per-node VMM client forwards to vmmdpb.StopInstance;
	// signal+graceSeconds wire shape is identical.
	StopInstanceOnNode(ctx context.Context, nodeID, instance string, signal int32, graceSeconds int32) (*StopInstanceOutcome, error)
	// Ping is the wire-level liveness probe (issue #97 / ADR-025
	// axis 3, PR #114). schedd's heartbeat loop calls this every
	// HeartbeatInterval on every active compute_node; a non-error
	// round-trip proves both gRPC socket reachability and that
	// vmmd is responsive enough to schedule the handler. Like
	// the other five methods, the router resolves the per-node
	// client by nodeID first (dial-once-per-target), then
	// forwards. Returns *api.Problem Capacity on an unknown
	// nodeID (no target_url to dial).
	Ping(ctx context.Context, nodeID string) (*PingOutcome, error)
	// PrepareLiveMigration (Tier A5 / ADR-066) is Phase 1 of the
	// four-phase cross-node live-instance handoff. Dials the
	// DYING vmmd (the nodeID argument is the dying node). Returns
	// the snapshot storage keys + the per-migration lease_token
	// the dying vmmd minted. The instance is left paused on the
	// dying node; Phase 4 (CancelLiveMigration) resumes it on
	// a rollback.
	PrepareLiveMigration(ctx context.Context, dyingNodeID, instanceID, snapshotStorageKey string) (LiveMigrationPrepare, error)
	// AdoptMigratedInstance (Tier A5 / ADR-066) is Phase 2. Dials
	// the NEW owner vmmd (nodeID is the new owner). Restores the
	// snapshot the dying vmmd wrote at Phase 1 and returns the
	// new instance's network identifiers.
	AdoptMigratedInstance(ctx context.Context, newOwnerNodeID, instanceID string, app AppSpec, memKey, vmstateKey, leaseToken string) (LiveMigrationAdopt, error)
	// AcknowledgeMigration (Tier A5 / ADR-066) is Phase 3.5.
	// Dials the DYING vmmd and tells it "Phase 3 committed;
	// destroy the paused VM". Idempotent.
	AcknowledgeMigration(ctx context.Context, dyingNodeID, instanceID, leaseToken string) error
	// CancelLiveMigration (Tier A5 / ADR-066) is Phase 4.
	// Dials the DYING vmmd and tells it "abort — resume the
	// paused VM". Idempotent on an already-resumed VM.
	CancelLiveMigration(ctx context.Context, dyingNodeID, instanceID, leaseToken string) error
	// Stats (issue #170 / PR-A, observability slice) forwards to the
	// per-node VMM and decodes the typed wrapper. Same nodeID
	// resolution path as the lifecycle RPCs; partial per-node failure
	// is the poller's problem to handle (log + skip, never abort).
	Stats(ctx context.Context, nodeID string) (*StatsSnapshot, error)

	// UpdateEgressAllowlist (ADR-031 + ADR-033, tier-2 PR-B) pushes
	// a fresh per-app egress allowlist into vmmd's live-instance
	// map without tearing the netns down. The egress_drift
	// subscriber invokes this on every pg_notify app_changed
	// payload with kind="updated"; the router resolves the
	// per-node vmmd client by nodeID (the node the live instance
	// actually lives on) and forwards the call. Idempotent on
	// the vmmd side (set-equal allowlist is a no-op). Returns
	// *api.Problem Capacity on an unknown nodeID (no target_url
	// to dial), or the wrapped gRPC error / vmmd-typed problem
	// on patch failure.
	UpdateEgressAllowlist(ctx context.Context, nodeID, appID string, allowlist []netip.Prefix) error
	// UpdateStaticEgressIP (ADR-119) pushes a fresh per-app
	// static egress IP into vmmd's live-instance map. The
	// router resolves the per-node vmmd by nodeID and
	// forwards to VMMClient.UpdateStaticEgressIP. ip=""
	// clears the per-app pin (mirrors the DELETE wire
	// shape). Idempotent on the vmmd side (set-equal IP is
	// a no-op). Returns *api.Problem Capacity on an unknown
	// nodeID (no target_url to dial), or the wrapped gRPC
	// error / vmmd-typed problem on patch failure. Mirrors
	// UpdateEgressAllowlist's contract above.
	UpdateStaticEgressIP(ctx context.Context, nodeID, accountID, appID string, ip string) error
	// Logs (issue #254 / Move 4) opens a server-streaming handle
	// on the per-instance ring buffer on the vmmd that owns the
	// instance. The returned LogStream is the typed view of
	// vmmdpb.Vmmd_LogsClient — the caller drives it in a loop
	// until EOF or error. Returns *api.Problem Capacity on an
	// unknown nodeID; codes.NotFound from vmmd surfaces as
	// the gRPC status (the schedd-side handler lifts it to the
	// apid-facing 404).
	// sinceWrittenAt (issue #517 / PR-B acceptance #3) is the
	// host-side WrittenAt lower bound on the replay page; the
	// zero-time value is the "no bound" sentinel and is skipped
	// on the wire. Wire is additive per ADR-016.
	Logs(ctx context.Context, nodeID, instance string, sinceSeq int64, sinceWrittenAt time.Time) (LogStream, error)
}

// DialFunc is the factory VMMRouter uses to open a per-target VMM
// client. cmd/schedd wires the production sched.DialVMMContext;
// tests inject a recording stub so they don't need a real socket.
type DialFunc func(ctx context.Context, target string, tlsCfg *tls.Config) (VMM, error)

// LiveMigrationPrepare is the typed return for RoutedVMM::
// PrepareLiveMigration. Mirrors vmmdpb.PrepareLiveMigrationResponse
// field-for-field; the typed view lets the migration handoff
// orchestrator (pkg/sched/migration_handoff.go) carry the
// snapshot storage keys + the lease_token without a proto
// dependency in its signature.
type LiveMigrationPrepare struct {
	MemStorageKey     string
	VMStateStorageKey string
	LeaseToken        string
	FCVersion         string
}

// LiveMigrationAdopt is the typed return for RoutedVMM::
// AdoptMigratedInstance. Mirrors vmmdpb.AdoptMigratedInstanceResponse;
// the network identifiers (HostIP, Netns, GuestUID) are surfaced
// so the new owner vmmd's logs can correlate, even though schedd
// doesn't currently persist them on the migration path.
type LiveMigrationAdopt struct {
	HostIP   string
	Netns    string
	GuestUID int
}

// VMMRouter is the dial-once-per-target cache that satisfies
// RoutedVMM. The cache key is the nodeID string (every node has a
// stable UUID; the target_url it dials is the corresponding
// compute_nodes.target_url). The dial closure resolves the target
// URL on demand from the cached (nodeID, targetURL) map — the
// router never re-asks the Store mid-flight because the
// (nodeID, targetURL) tuple is fixed at startup via
// ActiveComputeNodes.
type VMMRouter struct {
	mu      sync.Mutex
	cache   map[string]VMM    // nodeID -> dialed client (targetURL stored separately for the dial path)
	targets map[string]string // nodeID -> target_url (filled at construction; lookup before dial)
	dial    DialFunc
	tls     *tls.Config
}

// NewVMMRouter builds a router pre-populated with the (nodeID →
// target_url) map from the active compute_nodes. The dial happens
// lazily on the first RPC for a given nodeID, so a slow / dead
// vmmd target never blocks startup.
//
// An empty activeNodes slice is allowed; the router is then a no-op
// (every method returns ErrCapacity). cmd/schedd's startup reads
// ActiveComputeNodes and passes the slice here; tests that don't
// care about the dial surface pass an empty slice and inject a
// fake via SetClient (used by vmmrouter_test.go).
func NewVMMRouter(activeNodes []ComputeNodeInfo, dial DialFunc, tlsCfg *tls.Config) *VMMRouter {
	r := &VMMRouter{
		cache:   map[string]VMM{},
		targets: map[string]string{},
		dial:    dial,
		tls:     tlsCfg,
	}
	for _, n := range activeNodes {
		r.targets[n.ID] = n.TargetURL
	}
	return r
}

// ComputeNodeInfo is the slim subset of state.ComputeNode the
// router needs at startup. Decoupling from state.ComputeNode keeps
// the router testable without importing pkg/state (which pulls in
// pgx, etc., via the Store interface). cmd/schedd projects the
// ActiveComputeNodes slice into this shape; tests pass literals.
type ComputeNodeInfo struct {
	ID        string
	TargetURL string
}

// SetClient installs a pre-dialed VMM for a given nodeID. Test-only
// seam; production code goes through the dial closure set at
// NewVMMRouter time. Calling SetClient on a node the router has
// already dialed replaces the cached client (tests that want to
// reset state between subtests use this).
func (r *VMMRouter) SetClient(nodeID string, c VMM) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[nodeID] = c
}

// Client returns the cached VMM for nodeID. Returns nil if the
// router has not yet dialled it. Tests use this to assert cache
// state without exposing internals.
func (r *VMMRouter) Client(nodeID string) VMM {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cache[nodeID]
}

// Refresh (Tier A3) drops the dialed client for nodeID and updates
// the in-memory target_url from the live compute_nodes row. Called
// by the router_watcher on every compute_node_changed pg_notify
// payload; the next resolveFor lazy-dials against the fresh URL.
//
// targetURL is the canonical compute_nodes.target_url at notify
// time. Pass an empty string when the row is no longer in the
// table — Refresh writes targets[nodeID]="" so resolveFor returns
// its normal ErrCapacity (no stale dial against an old URL).
//
// Concurrent resolves in flight when Refresh lands keep the client
// they already hold (Close is a best-effort tear-down; the caller
// will see the new URL on the next RPC and re-dial anyway). The
// cache eviction is unconditional on entry — even a Refresh that
// later errors out leaves an empty cache slot, so a transient PG
// blip cannot prolong the use of a stale dialed client.
// routerCloseGrace is how long an evicted vmmd client stays open before
// Refresh closes it, so an RPC already in flight can finish.
//
// Sized above the longest per-call budget the engine hands out. The
// longest is the snapshot capture, SnapshotBudgetFor at the largest
// plan RAM — a Scale park uploads ~1 GB to the OCI registry — plus
// headroom for the dial and response. Anything shorter reintroduces the
// bug for the calls most expensive to lose.
var routerCloseGrace = SnapshotBudgetFor(maxPlanRAMMB()) + 30*time.Second

// maxPlanRAMMB is the largest per-instance RAM any plan allows. Derived
// from the limits table rather than hardcoded so a future plan with more
// memory widens the grace automatically instead of silently shortening
// it relative to the budget.
func maxPlanRAMMB() int {
	max := 0
	for _, plan := range api.Plans {
		if limits, ok := api.LimitsFor(plan); ok && limits.RAMMB > max {
			max = limits.RAMMB
		}
	}
	return max
}

func (r *VMMRouter) Refresh(nodeID, targetURL string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cli, ok := r.cache[nodeID]; ok && cli != nil {
		delete(r.cache, nodeID)
		// Evict now, close LATER. Closing a grpc.ClientConn cancels
		// every RPC in flight on it with
		//   "rpc error: code = Canceled desc = grpc: the client
		//    connection is closing"
		// so an immediate Close here destroyed any long-running call
		// that happened to be mid-flight.
		//
		// The comment above used to claim in-flight resolves "keep the
		// client they already hold". They do hold the pointer — but the
		// connection under it is already gone, so the RPC dies anyway.
		//
		// That was not theoretical. PauseAndSnapshot runs for tens of
		// seconds (a Scale park uploads ~1 GB to ghcr.io), while
		// compute_node_changed fires on every heartbeat-driven
		// re-registration and on any operator UPDATE of the row. On
		// 2026-09-04 an undisturbed prime died at 36s of a 195s budget
		// with precisely that gRPC error, so no deployment could reach
		// `live`.
		//
		// Deferring the close lets in-flight RPCs drain while new ones
		// immediately get a freshly dialled client from the empty cache
		// slot. The delay is bounded and the connection is always
		// closed, so this does not leak.
		go func(c any) {
			timer := time.NewTimer(routerCloseGrace)
			defer timer.Stop()
			<-timer.C
			if closer, ok := c.(io.Closer); ok {
				_ = closer.Close()
			}
		}(cli)
	}
	// targets[nodeID] is written unconditionally — even when
	// targetURL == "" — so the unknown-node path (resolveFor's
	// `if !ok` branch) is reachable on the next dial for a
	// soft-deleted node, and a hard delete avoids a stale-dial
	// retry against an old URL.
	r.targets[nodeID] = targetURL
}

// resolveFor returns the dialed VMM for nodeID, dialing on first
// use. The dial is serialised under the cache mutex for the same
// node; concurrent dials for different nodes race freely. A lost
// race (another goroutine dialled while we were dialling) closes
// our client and returns the cached one — no leak.
func (r *VMMRouter) resolveFor(ctx context.Context, nodeID string) (VMM, error) {
	r.mu.Lock()
	if c, ok := r.cache[nodeID]; ok && c != nil {
		r.mu.Unlock()
		return c, nil
	}
	r.mu.Unlock()

	target, ok := r.targets[nodeID]
	// An empty target_url is the same as "row gone" for the
	// resolveFor path: resolveFor's contract is to dial vmmd
	// against a real URL. A blank URL would silently re-dial
	// against today's default (`unix://` of the empty path) and
	// either succeed (a real socket at the OS-default path) or
	// fail with a confusing connection-refused error. Tier A3's
	// Refresh writes "" precisely so resolveFor returns ErrCapacity
	// here — the gateway's 503 mapping is consistent.
	if !ok || target == "" {
		return nil, api.ErrCapacity(fmt.Sprintf(
			"vmm router: no target_url for node_id %q (compute_nodes row missing or target_url empty)", nodeID))
	}
	if r.dial == nil {
		return nil, errors.New("vmm router: nil dial closure (constructor not called)")
	}
	cli, err := r.dial(ctx, target, r.tls)
	if err != nil {
		return nil, fmt.Errorf("vmm router: dial %s: %w", target, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.cache[nodeID]; ok && existing != nil {
		// Lost race. Close the duplicate and return the winner.
		if closer, ok := cli.(io.Closer); ok {
			_ = closer.Close()
		}
		return existing, nil
	}
	r.cache[nodeID] = cli
	return cli, nil
}

// CreateColdBoot implements RoutedVMM.
func (r *VMMRouter) CreateColdBoot(ctx context.Context, nodeID, instance string, app AppSpec) (*WakeOutcome, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return cli.CreateColdBoot(ctx, instance, app)
}

// CreateFromSnapshot implements RoutedVMM.
func (r *VMMRouter) CreateFromSnapshot(ctx context.Context, nodeID, instance string, app AppSpec, snap SnapshotRef) (*WakeOutcome, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return cli.CreateFromSnapshot(ctx, instance, app, snap)
}

// PauseAndSnapshot implements RoutedVMM.
func (r *VMMRouter) PauseAndSnapshot(ctx context.Context, nodeID, instance, vmstatePath, storageKey, vmstateStorageKey string) (SnapshotBytes, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return SnapshotBytes{}, err
	}
	return cli.PauseAndSnapshot(ctx, instance, vmstatePath, storageKey, vmstateStorageKey)
}

// WarmSnapshot implements RoutedVMM (issue #470 / PR #470-FU-A).
// Resolves the per-node client by nodeID and forwards to
// VMMClient.WarmSnapshot. Returns Capacity on an unknown nodeID
// (same as PauseAndSnapshot).
func (r *VMMRouter) WarmSnapshot(ctx context.Context, nodeID, instance, storageKey, vmstateStorageKey string) (SnapshotBytes, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return SnapshotBytes{}, err
	}
	return cli.WarmSnapshot(ctx, instance, storageKey, vmstateStorageKey)
}

// Destroy implements RoutedVMM.
func (r *VMMRouter) Destroy(ctx context.Context, nodeID, instance string) error {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.Destroy(ctx, instance)
}

// StopInstanceOnNode (M-2 / ADR-138 §Decision 1) implements
// RoutedVMM. Same resolveFor path as Destroy — dial-once-per-target,
// the per-node VMMClient owns the gRPC socket and the vmmdpb proto
// envelope. The returned outcome carries (ExitCode, KillSignalSent)
// for the engine's audit-row stamping.
func (r *VMMRouter) StopInstanceOnNode(ctx context.Context, nodeID, instance string, signal int32, graceSeconds int32) (*StopInstanceOutcome, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return cli.StopInstance(ctx, instance, signal, graceSeconds)
}

// FrameworkReady implements RoutedVMM. Pure pass-through to the
// per-node client's FrameworkReady RPC; the router does not cache
// (the cmd/vmmd DGRAM host recv loop calls this on every signal, so
// the per-receipt dial overhead is amortized by the per-node
// connection pool in resolveFor).
func (r *VMMRouter) FrameworkReady(ctx context.Context, nodeID, instance string, warmupMs int64) error {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.FrameworkReady(ctx, instance, warmupMs)
}

// Ping implements RoutedVMM (issue #97 / ADR-025 axis 3, PR #114).
// Forwards to the per-node VMM client via the same resolveFor path
// the lifecycle RPCs use; dial-once-per-target semantics carry over
// (a successful CreateColdBoot earlier means Ping reuses the same
// connection, no extra dial). On unknown nodeID, returns the
// router's *api.Problem Capacity — the heartbeat loop treats that
// as "no client" and marks the row inactive.
func (r *VMMRouter) Ping(ctx context.Context, nodeID string) (*PingOutcome, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return cli.Ping(ctx)
}

// Stats implements RoutedVMM (issue #170 / PR-A, observability
// slice). Same resolveFor path as Ping / Destroy. Per-node dial
// failures surface here as *api.Problem Capacity, which the
// instancestats poller logs and skips over (partial snapshots
// preferred to aborting the whole sweep).
func (r *VMMRouter) Stats(ctx context.Context, nodeID string) (*StatsSnapshot, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return cli.Stats(ctx)
}

// PrepareLiveMigration (Tier A5 / ADR-066) routes Phase 1 to
// the DYING vmmd (the dyingNodeID argument). Same resolveFor
// path as the lifecycle RPCs — dial-once-per-target semantics
// carry over. On unknown nodeID, returns the router's
// *api.Problem Capacity — the migration_handoff orchestrator
// treats that as a transient peer failure and retries on the
// next compute_node_changed re-fire (the lease is bounded by
// MigrateLiveLeaseSeconds from pkg/api/limits.go).
func (r *VMMRouter) PrepareLiveMigration(ctx context.Context, dyingNodeID, instanceID, snapshotStorageKey string) (LiveMigrationPrepare, error) {
	cli, err := r.resolveFor(ctx, dyingNodeID)
	if err != nil {
		return LiveMigrationPrepare{}, err
	}
	return cli.PrepareLiveMigration(ctx, dyingNodeID, instanceID, snapshotStorageKey)
}

// AdoptMigratedInstance (Tier A5 / ADR-066) routes Phase 2 to
// the NEW owner vmmd. Same resolveFor pattern. A dial failure
// on the new owner surfaces as *api.Problem Capacity — the
// orchestrator cancels the handoff at Phase 4 in that case
// (the dying vmmd is told to resume the paused VM).
func (r *VMMRouter) AdoptMigratedInstance(ctx context.Context, newOwnerNodeID, instanceID string, app AppSpec, memKey, vmstateKey, leaseToken string) (LiveMigrationAdopt, error) {
	cli, err := r.resolveFor(ctx, newOwnerNodeID)
	if err != nil {
		return LiveMigrationAdopt{}, err
	}
	return cli.AdoptMigratedInstance(ctx, newOwnerNodeID, instanceID, app, memKey, vmstateKey, leaseToken)
}

// AcknowledgeMigration (Tier A5 / ADR-066) routes Phase 3.5
// to the DYING vmmd. Best-effort — a non-OK status here is
// logged but does not block the migration (Phase 3 has already
// committed). The dying vmmd will eventually destroy the
// paused VM on its own lease-expiry timeout; the ack is just
// a "you can free the netns now" hint.
func (r *VMMRouter) AcknowledgeMigration(ctx context.Context, dyingNodeID, instanceID, leaseToken string) error {
	cli, err := r.resolveFor(ctx, dyingNodeID)
	if err != nil {
		return err
	}
	return cli.AcknowledgeMigration(ctx, dyingNodeID, instanceID, leaseToken)
}

// CancelLiveMigration (Tier A5 / ADR-066) routes Phase 4 to
// the DYING vmmd. Idempotent on an already-resumed VM. A
// non-OK status here is logged but does not block the rollback
// (the row is already in 'parked' via Store.CancelInstanceMigration
// at Phase 4).
func (r *VMMRouter) CancelLiveMigration(ctx context.Context, dyingNodeID, instanceID, leaseToken string) error {
	cli, err := r.resolveFor(ctx, dyingNodeID)
	if err != nil {
		return err
	}
	return cli.CancelLiveMigration(ctx, dyingNodeID, instanceID, leaseToken)
}

// UpdateEgressAllowlist (ADR-031 + ADR-033, tier-2 PR-B) routes the
// patch to the vmmd that owns the live instance. The egress_drift
// subscriber hands us a single (appID, allowlist) pair; we resolve
// the per-node client by nodeID and forward. Per-node vmmd fans
// out to its own live-instance map. Errors from vmmd bubble up
// (gRPC status / typed problem); the subscriber logs + drops so a
// bad patch never blocks the loop — the next reconcile on the
// next event (or a watchdog-driven Park + ColdBoot) re-syncs.
func (r *VMMRouter) UpdateEgressAllowlist(ctx context.Context, nodeID, appID string, allowlist []netip.Prefix) error {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.UpdateEgressAllowlist(ctx, appID, allowlist)
}

// UpdateStaticEgressIP (ADR-119) routes the patch to the
// vmmd that owns the live instance. The egress_drift
// subscriber hands us a single (appID, ip) pair; we
// resolve the per-node client by nodeID and forward.
// ip="" clears the per-app pin. Errors from vmmd bubble
// up; the subscriber logs + drops so a bad patch never
// blocks the loop. Mirrors UpdateEgressAllowlist's
// contract above.
func (r *VMMRouter) UpdateStaticEgressIP(ctx context.Context, nodeID, accountID, appID string, ip string) error {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.UpdateStaticEgressIP(ctx, accountID, appID, ip)
}

// Logs (issue #254 / Move 4, issue #517 / PR-B acceptance #3 +
// #4) routes the per-instance log stream to the vmmd that owns
// the instance. Returns *api.Problem Capacity on an unknown
// nodeID; gRPC-side failures (vmmd dial issue, codes.NotFound
// when the instance is parked) bubble up as typed errors the
// schedd-side handler decides to lift or map.
//
// sinceWrittenAt (issue #517 / PR-B) is the host-side WrittenAt
// lower bound on the replay page; the zero value means no
// bound. Forwarded verbatim — the wire is additive per ADR-016.
func (r *VMMRouter) Logs(ctx context.Context, nodeID, instance string, sinceSeq int64, sinceWrittenAt time.Time) (LogStream, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return cli.Logs(ctx, instance, sinceSeq, sinceWrittenAt)
}

// Compile-time assertion: VMMRouter satisfies the engine-facing
// RoutedVMM interface. A regression that drops a method signature
// fails the build here, before it surfaces as a runtime nil-method
// panic in the wake loop.
var _ RoutedVMM = (*VMMRouter)(nil)
