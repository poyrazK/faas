package sched

// nodekeys.go — in-memory registry of trusted capacity signing keys,
// populated from compute_node_keys and bound to their compute-node owners.
//
// Background. ADR-053 closes the CapacityReport trust gap:
// every report carries a 64-byte ECDSA-P-256 (r||s) signature
// over the canonical payload, and the schedd handler verifies
// it against a key registered in this table. The registry is
// keyed by key_id — the SHA-256 hex of the leaf's SubjectPublicKeyInfo —
// with compute_node_id retained as an authorization constraint.
//
// The registry is the load-bearing enforcement; without it, a
// misconfigured vmmd (or an attacker with the wire) could send
// "valid ECDSA" signatures under an arbitrary ephemeral key,
// defeating the whole slice. The key_id binding is what makes
// the signature path useful.
//
// Lifecycle. NewNodeKeyRegistry returns an empty registry;
// schedd's wiring calls Refresh on startup AND subscribes to
// the 'compute_node_changed' pg_notify channel so a vmmd
// registering a new key (rotation, fresh boot) lands within
// the listener's next refresh tick. The lookup is a single
// map read under RWMutex.RLock; the chooser goroutine is the
// only reader.
//
// The database loader admits only active nodes and either the current key or
// one unexpired overlap key. A rollout preserves the on-host key; an explicit
// rotation keeps the former key usable for 24 hours.

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/db"
)

// PublicKey is a diagnostic and compatibility accessor. Authentication paths
// must call PublicKeyForNode so a valid key cannot claim another node's ID.
func (r *NodeKeyRegistry) PublicKey(keyID string) (*ecdsa.PublicKey, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.keys[keyID]
	return entry.publicKey, ok
}

// PublicKeyForNode resolves a key only for the compute identity that owns it.
// The report's node_id is caller supplied, so this check is part of signature
// authentication rather than an optional authorization layer.
func (r *NodeKeyRegistry) PublicKeyForNode(nodeID, keyID string) (*ecdsa.PublicKey, bool) {
	if r == nil || nodeID == "" || keyID == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.keys[keyID]
	if !ok || entry.computeNodeID != nodeID {
		return nil, false
	}
	return entry.publicKey, true
}

type trustedNodeKey struct {
	computeNodeID string
	publicKey     *ecdsa.PublicKey
}

// NodeKeyRegistry is the in-memory key_id → owner/public-key map. It is
// constructed once at schedd startup and refreshed via the
// 'compute_node_changed' pg_notify listener.
//
// RWMutex guards the map. Read paths take RLock;
// write path (ReplaceAll) takes WLock. Refresh is full-snapshot
// (read every row, swap the map) — partial updates would
// require a per-row delta log and aren't worth the complexity
// at the typical fleet size (handful of rows).
type NodeKeyRegistry struct {
	mu   sync.RWMutex
	keys map[string]trustedNodeKey
	// loader is the production-side row loader; tests inject a
	// stub. Returns one (key_id, public_key_pem) tuple per row.
	// Returning an error aborts the refresh — schedd keeps the
	// last-known-good map and logs the failure.
	loader NodeKeyLoader
	// log is optional; pass nil in unit tests, the daemon's
	// slog.Logger at wiring time.
	log           NodeKeyLogger
	countObserver func(map[string]int)
}

// SetCountObserver installs the bounded per-node trusted-key metric sink. The
// callback runs after each atomic snapshot replacement and never under r.mu.
func (r *NodeKeyRegistry) SetCountObserver(observer func(map[string]int)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.countObserver = observer
	r.mu.Unlock()
}

// NodeKeyLoader is the production-side Postgres loader. schedd
// constructs an implementation that reads
//
//	select compute_node_id, key_id, public_key_pem
//
// for active nodes and usable lifecycle states. The interface lets tests
// inject an in-memory loader without spinning up a database.
type NodeKeyLoader interface {
	LoadNodeKeys(ctx context.Context) ([]NodeKeyRow, error)
}

// NodeKeyRow is one row from compute_node_keys. Loaded fresh on
// every Refresh; replaced atomically in the registry map.
type NodeKeyRow struct {
	ComputeNodeID string
	KeyID         string
	PublicKeyPEM  string
}

// NodeKeyLogger is the minimal slog.Logger interface so schedd's
// wiring can pass the daemon's structured logger and tests can
// pass nil (silent).
type NodeKeyLogger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}

// NewNodeKeyRegistry constructs an empty registry bound to
// loader + log. The wiring goroutine calls Refresh once at
// startup; the pg_notify listener calls it on every
// 'compute_node_changed' tick. Both paths share the
// full-snapshot ReplaceAll so partial-update bugs can't
// silently de-sync the map.
func NewNodeKeyRegistry(loader NodeKeyLoader, log NodeKeyLogger) *NodeKeyRegistry {
	return &NodeKeyRegistry{
		keys:   make(map[string]trustedNodeKey),
		loader: loader,
		log:    log,
	}
}

// ReplaceAll atomically swaps the registry's map for a fresh
// build from rows. Rows whose PEM fails to parse are skipped
// (with a Warn log) so a single malformed row doesn't
// sabotage the whole registry.
//
// nil receiver is tolerated (no-op). Returns the number of
// rows successfully parsed — useful for diagnostics and tests.
func (r *NodeKeyRegistry) ReplaceAll(rows []NodeKeyRow) int {
	if r == nil {
		return 0
	}
	fresh := make(map[string]trustedNodeKey, len(rows))
	conflicted := make(map[string]struct{})
	for _, row := range rows {
		if row.ComputeNodeID == "" {
			continue
		}
		pub, err := parsePublicKeyPEM(row.PublicKeyPEM)
		if err != nil {
			if r.log != nil {
				r.log.Warn("sched: skip unparseable node key",
					"key_id", row.KeyID, "err", err)
			}
			continue
		}
		if existing, ok := fresh[row.KeyID]; ok && existing.computeNodeID != row.ComputeNodeID {
			delete(fresh, row.KeyID)
			conflicted[row.KeyID] = struct{}{}
			if r.log != nil {
				r.log.Warn("sched: reject node key assigned to multiple compute nodes", "key_id", row.KeyID)
			}
			continue
		}
		if _, conflict := conflicted[row.KeyID]; conflict {
			continue
		}
		fresh[row.KeyID] = trustedNodeKey{computeNodeID: row.ComputeNodeID, publicKey: pub}
	}
	counts := make(map[string]int)
	for _, entry := range fresh {
		counts[entry.computeNodeID]++
	}
	r.mu.Lock()
	r.keys = fresh
	observer := r.countObserver
	r.mu.Unlock()
	if observer != nil {
		observer(counts)
	}
	return len(fresh)
}

// Size returns the count of registered keys for diagnostics.
func (r *NodeKeyRegistry) Size() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.keys)
}

// Refresh calls the loader and swaps the registry map. Errors
// keep the last-known-good map (no destructive partial updates)
// and are logged at Warn. Returns the count of keys after
// refresh; 0 + a non-nil error means the registry is now empty
// (loaders must not return rows + error).
func (r *NodeKeyRegistry) Refresh(ctx context.Context) (int, error) {
	if r == nil {
		return 0, errors.New("sched: nil registry")
	}
	if r.loader == nil {
		return 0, errors.New("sched: nil loader")
	}
	rows, err := r.loader.LoadNodeKeys(ctx)
	if err != nil {
		if r.log != nil {
			r.log.Warn("sched: node key loader failed; keeping last-known-good",
				"err", err)
		}
		return r.Size(), fmt.Errorf("load: %w", err)
	}
	n := r.ReplaceAll(rows)
	if r.log != nil {
		r.log.Info("sched: node key registry refreshed",
			"keys", n, "rows_loaded", len(rows))
	}
	return n, nil
}

// Run drains an already-opened 'compute_node_changed' channel
// until ctx is cancelled or the channel closes. Each notify
// triggers a Refresh; the production channel covers both
// compute_nodes AND compute_node_keys writes (migration 00076's
// trigger fires on either table), so a single subscription
// refreshes both lifecycles.
//
// Returns ctx.Err() on cancellation; closes on nil-channel
// return cleanly. The handler's nil-safe NodeKeyRegistry
// accessor means a missing subscription is operationally safe
// (the registry stays at its initial-Refresh snapshot); the
// subtle failure mode is "vmmd registers a new key but
// schedd's listener is wedged" — the canary is the
// schedd_capacity_signature_rejected_total counter (F7) which
// spikes if the live registry doesn't accept a freshly-issued
// signature.
//
// nil receiver is tolerated (no-op drain).
func (r *NodeKeyRegistry) Run(ctx context.Context, ch <-chan db.Notification) error {
	if r == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-ch:
			if !ok {
				return nil
			}
			// Refresh on every notify. Errors keep the
			// last-known-good map (Refresh's contract); the
			// loop survives a transient loader failure.
			if _, err := r.Refresh(ctx); err != nil {
				// Refresh already logs at Warn; the loop
				// continues so the next notify retries.
				_ = err
			}
		}
	}
}

// parsePublicKeyPEM parses a PEM-encoded SubjectPublicKeyInfo
// into an *ecdsa.PublicKey. Returns an error when the PEM is
// malformed, the algorithm is not ECDSA, or the curve is not
// P-256 (the only curve the platform supports; mirrors
// pkg/cosign's strictness).
func parsePublicKeyPEM(pemStr string) (*ecdsa.PublicKey, error) {
	if pemStr == "" {
		return nil, errors.New("empty PEM")
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("not PEM-encoded")
	}
	if block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("PEM type %q, want PUBLIC KEY", block.Type)
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKIX: %w", err)
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not ECDSA (got %T)", pub)
	}
	if ec.Curve != ecdsaP256() {
		return nil, fmt.Errorf("curve %s, want P-256", ec.Curve.Params().Name)
	}
	return ec, nil
}

// Compile-time guards.
var (
	_ nodeKeyLookup = (*NodeKeyRegistry)(nil)
)
