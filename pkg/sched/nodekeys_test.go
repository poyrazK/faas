// nodekeys_test.go — NodeKeyRegistry behaviour tests.
//
// Pins the three contracts the schedd-side wiring (cmd/schedd/main.go)
// depends on:
//
//  1. Refresh + PublicKey round-trip: a loader-backed registry
//     returns registered public keys by key_id, and an unknown
//     key_id returns (nil, false).
//  2. ReplaceAll is atomic: a row whose PEM fails to parse is
//     skipped (the registry keeps the last-known-good map for the
//     parseable rows).
//  3. Run drains an already-opened 'compute_node_keys_changed'
//     channel and triggers Refresh on every notify; nil receiver is
//     a no-op drain that returns ctx.Err() on cancellation.
//     Post-00276 the node-key registry listens on the dedicated
//     keys channel (was 'compute_node_changed' before the split).
//
// White-box (package sched) so the test can construct a
// NodeKeyRegistry without re-exporting the loader interface.

package sched

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

// stubLoader is a NodeKeyLoader backed by an in-memory slice +
// a load counter. Tests assert the refresh pacing through the
// load counter (one Load per Refresh per Run notify).
type stubLoader struct {
	mu    sync.Mutex
	rows  []NodeKeyRow
	loads atomic.Int64
	// failOnNextLoad, if non-nil, makes the next Load call
	// return the wrapped error and clears the flag. Tests use
	// this to assert the loop survives a transient loader
	// failure (the registry keeps the last-known-good map).
	failOnNextLoad error
}

func (s *stubLoader) LoadNodeKeys(_ context.Context) ([]NodeKeyRow, error) {
	s.loads.Add(1)
	s.mu.Lock()
	err := s.failOnNextLoad
	s.failOnNextLoad = nil
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.rows, nil
}

// setFailOnNextLoad injects a loader failure for the next call.
// Mutex-guarded because the test goroutine sets it while the
// Run goroutine reads it.
func (s *stubLoader) setFailOnNextLoad(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failOnNextLoad = err
}

// silentLog is a NodeKeyLogger that swallows both Warn and Info.
// Used when the test wants to drive the registry without test
// log spam.
type silentLog struct{}

func (silentLog) Warn(string, ...any) {}
func (silentLog) Info(string, ...any) {}

// generateP256Row returns a parseable NodeKeyRow for a fresh
// ECDSA P-256 key. Test helper.
func generateP256Row(t *testing.T) (*ecdsa.PrivateKey, NodeKeyRow) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	keyID, err := KeyIDForPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("KeyIDForPublicKey: %v", err)
	}
	return priv, NodeKeyRow{KeyID: keyID, PublicKeyPEM: string(pemBytes)}
}

// TestNodeKeyRegistry_RefreshAndLookup pins the happy path:
// a loader-backed registry returns the registered public key
// by key_id, and an unknown key_id returns (nil, false).
func TestNodeKeyRegistry_RefreshAndLookup(t *testing.T) {
	_, row := generateP256Row(t)
	loader := &stubLoader{rows: []NodeKeyRow{row}}
	reg := NewNodeKeyRegistry(loader, silentLog{})

	// Pre-Refresh: the registry is empty.
	if _, ok := reg.PublicKey(row.KeyID); ok {
		t.Fatal("PublicKey before Refresh returned ok; want miss")
	}

	// Refresh populates the map.
	if n, err := reg.Refresh(context.Background()); err != nil || n != 1 {
		t.Fatalf("Refresh: n=%d, err=%v, want n=1, nil", n, err)
	}

	// Post-Refresh: registered key resolves.
	got, ok := reg.PublicKey(row.KeyID)
	if !ok {
		t.Fatal("PublicKey after Refresh returned false; want hit")
	}
	if got.Curve != elliptic.P256() {
		t.Errorf("curve = %s, want P-256", got.Curve.Params().Name)
	}

	// Unknown key returns (nil, false).
	if _, ok := reg.PublicKey("deadbeef"); ok {
		t.Error("PublicKey on unknown key_id returned ok; want miss")
	}
}

// TestNodeKeyRegistry_ReplaceAllSkipsUnparseableRows pins the
// failure-isolation contract: a row whose PEM fails to parse is
// skipped, and the parseable rows still land in the registry.
// A regression that turned a single bad row into a no-op
// ReplaceAll would silently drop legitimate keys, breaking
// signature verification for every healthy vmmd.
func TestNodeKeyRegistry_ReplaceAllSkipsUnparseableRows(t *testing.T) {
	_, good := generateP256Row(t)
	loader := &stubLoader{rows: []NodeKeyRow{
		good,
		{KeyID: "bad", PublicKeyPEM: "not a pem block"},
	}}
	reg := NewNodeKeyRegistry(loader, silentLog{})
	n, err := reg.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if n != 1 {
		t.Errorf("Refresh returned n=%d, want 1 (the bad row should be skipped)", n)
	}
	if _, ok := reg.PublicKey(good.KeyID); !ok {
		t.Errorf("good key not present after mixed-row Refresh")
	}
}

// TestNodeKeyRegistry_RefreshKeepsLastKnownGoodOnError pins
// the contract that a transient loader failure does NOT
// destructive-update the registry. A regression that replaced
// the map with an empty one on error would silently nuke every
// vmmd's signature verification.
func TestNodeKeyRegistry_RefreshKeepsLastKnownGoodOnError(t *testing.T) {
	_, row := generateP256Row(t)
	loader := &stubLoader{rows: []NodeKeyRow{row}}
	reg := NewNodeKeyRegistry(loader, silentLog{})

	// Initial Refresh populates the map.
	if _, err := reg.Refresh(context.Background()); err != nil {
		t.Fatalf("initial Refresh: %v", err)
	}
	if _, ok := reg.PublicKey(row.KeyID); !ok {
		t.Fatal("key not present after initial Refresh")
	}

	// Inject a loader failure; Refresh must keep the map.
	loader.setFailOnNextLoad(errors.New("postgres down"))
	if _, err := reg.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh on loader failure returned nil; want error")
	}
	if _, ok := reg.PublicKey(row.KeyID); !ok {
		t.Error("key disappeared after loader-failure Refresh; wanted last-known-good")
	}
}

// TestNodeKeyRegistry_RunTriggersRefresh pins the cmd/schedd
// wiring contract: every notify on the supplied channel triggers
// a Refresh; the loop survives a transient loader failure; the
// loop exits cleanly on channel close.
func TestNodeKeyRegistry_RunTriggersRefresh(t *testing.T) {
	_, row := generateP256Row(t)
	loader := &stubLoader{rows: []NodeKeyRow{row}}
	reg := NewNodeKeyRegistry(loader, silentLog{})

	// Initial Refresh.
	if _, err := reg.Refresh(context.Background()); err != nil {
		t.Fatalf("initial Refresh: %v", err)
	}
	initialLoads := loader.loads.Load()

	// Drive Run on a goroutine with a test-controlled channel.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed := make(chan db.Notification, 4)
	done := make(chan error, 1)
	go func() { done <- reg.Run(ctx, feed) }()

	// Send two notifies — one success, one loader failure.
	feed <- db.Notification{Channel: db.NotifyComputeNodeKeysChanged}
	loader.setFailOnNextLoad(errors.New("transient"))
	feed <- db.Notification{Channel: db.NotifyComputeNodeKeysChanged}

	// Let the loop drain.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if loader.loads.Load() >= initialLoads+2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	finalLoads := loader.loads.Load()
	if finalLoads < initialLoads+2 {
		t.Errorf("loads = %d, want ≥ %d (one per notify)", finalLoads, initialLoads+2)
	}

	// Loop must survive the transient failure: the registry still
	// holds the legitimate key.
	if _, ok := reg.PublicKey(row.KeyID); !ok {
		t.Error("key disappeared after loader-failure notify; wanted last-known-good")
	}

	// Close the channel — the loop exits cleanly.
	close(feed)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run on closed channel = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit within 2s of channel close")
	}
}

// TestNodeKeyRegistry_RunNilReceiver pins the nil-safety
// contract: a nil *NodeKeyRegistry.Run drains on ctx.Done and
// returns ctx.Err() on cancellation. The schedd wiring reads
// the engine's NodeKeyRegistry accessor which is nil-safe, so
// this is the gold-plate case — a regression that dropped
// the nil check would panic in tests that don't wire the
// registry.
func TestNodeKeyRegistry_RunNilReceiver(t *testing.T) {
	var reg *NodeKeyRegistry
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reg.Run(ctx, make(chan db.Notification)) }()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run on nil receiver = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nil-receiver Run did not exit within 2s of cancel")
	}
}
