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
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// RoutedVMM is the multi-node vmmd surface schedd's engine consumes.
// Each method takes nodeID as the first argument so the router can
// forward to the right per-target vmmd connection.
//
// The 4-method surface mirrors VMM verbatim (CreateColdBoot,
// CreateFromSnapshot, PauseAndSnapshot, Destroy). The router's
// implementation looks up the per-node client by ID, dials on first
// use, and forwards. If the ID has no row in the cache and the
// router can't dial (e.g. an operator typo in target_url), the call
// returns a *api.Problem with Code=Capacity — the same code the
// ledger uses for "no headroom" so the gateway's 503 mapping is
// consistent.
type RoutedVMM interface {
	CreateColdBoot(ctx context.Context, nodeID, instance string, app AppSpec) (*WakeOutcome, error)
	CreateFromSnapshot(ctx context.Context, nodeID, instance string, app AppSpec, snap SnapshotRef) (*WakeOutcome, error)
	// PauseAndSnapshot (issue #121 / ADR-025 axis 2 slice 4) takes
	// vmstateStorageKey as a third string alongside vmstatePath and
	// storageKey. Default-local schedd sends the empty value so vmmd's
	// host-path branch is taken bit-for-bit; remote-node schedd sends
	// state.SnapVMStateKey(deploymentID).
	PauseAndSnapshot(ctx context.Context, nodeID, instance, vmstatePath, storageKey, vmstateStorageKey string) (SnapshotBytes, error)
	Destroy(ctx context.Context, nodeID, instance string) error
	// Ping is the wire-level liveness probe (issue #97 / ADR-025
	// axis 3, PR #114). schedd's heartbeat loop calls this every
	// HeartbeatInterval on every active compute_node; a non-error
	// round-trip proves both gRPC socket reachability and that
	// vmmd is responsive enough to schedule the handler. Like
	// the other four methods, the router resolves the per-node
	// client by nodeID first (dial-once-per-target), then
	// forwards. Returns *api.Problem Capacity on an unknown
	// nodeID (no target_url to dial).
	Ping(ctx context.Context, nodeID string) (*PingOutcome, error)
}

// DialFunc is the factory VMMRouter uses to open a per-target VMM
// client. cmd/schedd wires the production sched.DialVMMContext;
// tests inject a recording stub so they don't need a real socket.
type DialFunc func(ctx context.Context, target string, tlsCfg *tls.Config) (VMM, error)

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
	if !ok {
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

// Destroy implements RoutedVMM.
func (r *VMMRouter) Destroy(ctx context.Context, nodeID, instance string) error {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.Destroy(ctx, instance)
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

// Compile-time assertion: VMMRouter satisfies the engine-facing
// RoutedVMM interface. A regression that drops a method signature
// fails the build here, before it surfaces as a runtime nil-method
// panic in the wake loop.
var _ RoutedVMM = (*VMMRouter)(nil)
