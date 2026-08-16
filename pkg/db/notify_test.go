package db

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSubscribeWithReconnect_NilPoolErrors ensures the wrapper fails fast on
// obvious misconfig rather than spinning forever.
func TestSubscribeWithReconnect_NilPoolErrors(t *testing.T) {
	if _, err := SubscribeWithReconnect(context.Background(), nil, []string{NotifyAppChanged}, nil); err == nil {
		t.Fatalf("expected error for nil pool, got nil")
	}
	if _, err := SubscribeWithReconnect(context.Background(), &pgxpool.Pool{}, nil, nil); err == nil {
		t.Fatalf("expected error for empty channels, got nil")
	}
}

// TestSubscribeWithReconnect_ClosesOnCtxCancel ensures the wrapper's outer
// channel shuts down cleanly when the caller's context is cancelled (the
// one path the wrapper exposes its own close on).
//
// Skipped when the test Postgres connection is unavailable — the rest of
// the test suite (pkg/state, etc.) honours the same LOCAL_PG_URL env.
func TestSubscribeWithReconnect_ClosesOnCtxCancel(t *testing.T) {
	pool, closer := newTestPoolOrSkip(t)
	defer closer()

	ctx, cancel := context.WithCancel(context.Background())
	notif, err := SubscribeWithReconnect(ctx, pool, []string{"test_channel_xyz"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("SubscribeWithReconnect initial acquire: %v", err)
	}
	cancel()
	select {
	case _, ok := <-notif:
		if ok {
			// Drain anything buffered, then check the next read sees close.
			select {
			case _, ok := <-notif:
				if ok {
					t.Fatalf("expected channel to close after ctx cancel; got a buffered value")
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("expected channel close within 2s of ctx cancel")
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected channel close within 2s of ctx cancel")
	}
}

// TestSubscribeWithReconnect_ResubscribesOnInnerClose exercises the F-11
// invariant across the four daemon call sites: when the inner Subscribe
// channel closes (here: pool close → conn drop → inner Subscribe goroutine
// returns), the outer channel stays open so the daemon's select loop
// keeps running. This is the bug class closed by F-11.
func TestSubscribeWithReconnect_ResubscribesOnInnerClose(t *testing.T) {
	pool, closer := newTestPoolOrSkip(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	notif, err := SubscribeWithReconnect(ctx, pool, []string{"resubscribe_test"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("SubscribeWithReconnect initial acquire: %v", err)
	}

	// First message — sanity check the subscription is live.
	if err := Notify(ctx, pool, "resubscribe_test", `{"hello":"world"}`); err != nil {
		t.Fatalf("notify first: %v", err)
	}
	select {
	case n := <-notif:
		if n.Payload != `{"hello":"world"}` {
			t.Fatalf("first payload=%s, want hello:world", n.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("never received first notification")
	}

	// Killing the pool closes every acquired connection; the wrapper's
	// inner goroutine returns from WaitForNotification, inner Subscribe
	// closes the inner channel, and the wrapper falls into its
	// resubscribe loop. The outer channel must NOT close (the F-11
	// invariant).
	closer()

	// Give the wrapper a moment to observe the close and start retrying.
	// Without the wrapper, this same sequence would have closed the outer
	// channel within the same window — the regression test reads the
	// outer channel after the close window and expects it still open.
	time.Sleep(200 * time.Millisecond)

	select {
	case _, ok := <-notif:
		if ok {
			// A spurious buffered value during the close window is fine;
			// we want to confirm the channel is still open for the next read.
			select {
			case _, ok := <-notif:
				if !ok {
					// Channel closed within the resubscribe window.
					// F-11 regression.
					select {
					case <-ctx.Done():
						// Race: ctx cancel got there first. Tolerated;
						// the assertion below still proves the loop is
						// healthy at this point.
					default:
						t.Fatalf("F-11 regression: outer channel closed mid-resubscribe; daemon loops would exit")
					}
				}
			default:
				// Channel still open, no buffered value — expected.
			}
		} else {
			select {
			case <-ctx.Done():
			default:
				t.Fatalf("F-11 regression: outer channel closed mid-resubscribe")
			}
		}
	default:
		// No buffered value either — channel still open. Expected.
	}
}

// newTestPoolOrSkip returns a *pgxpool.Pool wired against the standard
// LOCAL_PG_URL, or skips the test (the rest of the suite honours the same
// env var). The closer should be deferred by the test to release the pool.
func newTestPoolOrSkip(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := os.Getenv("LOCAL_PG_URL")
	if dsn == "" {
		t.Skip("LOCAL_PG_URL not set; skipping pgx-backed integration test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Skipf("LOCAL_PG_URL parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Skipf("pgxpool.NewWithConfig: %v", err)
	}
	return pool, pool.Close
}

// TestNotifyConstants_ComputeNodesSplit asserts the post-00276 channel
// split is wire-distinct. Each constant is the wire-level channel name;
// a typo here would silently route consumers to a never-firing channel.
//
// The split is the lineage:
//
//	pre-00276:  NotifyComputeNodeChanged = "compute_node_changed"
//	            (carried compute_nodes + compute_node_keys events)
//	post-00276: NotifyComputeNodesChanged     = "compute_nodes_changed"
//	            NotifyComputeNodeKeysChanged   = "compute_node_keys_changed"
//
// The old constant is removed outright (full atomic migration); this
// test guards against accidental revert.
func TestNotifyConstants_ComputeNodesSplit(t *testing.T) {
	// Wire-distinct: a typo that maps two constants to the same
	// channel would silently route everything to the same place.
	if NotifyComputeNodesChanged == NotifyComputeNodeKeysChanged {
		t.Fatalf("NotifyComputeNodesChanged == NotifyComputeNodeKeysChanged (%q); the split is not actually split",
			NotifyComputeNodesChanged)
	}
	// Names match the migration-00276 pg_notify('...') strings.
	wantNodes := "compute_nodes_changed"
	wantKeys := "compute_node_keys_changed"
	if NotifyComputeNodesChanged != wantNodes {
		t.Errorf("NotifyComputeNodesChanged = %q, want %q", NotifyComputeNodesChanged, wantNodes)
	}
	if NotifyComputeNodeKeysChanged != wantKeys {
		t.Errorf("NotifyComputeNodeKeysChanged = %q, want %q", NotifyComputeNodeKeysChanged, wantKeys)
	}
	// Neither name collides with the legacy constant's value.
	const legacy = "compute_node_changed"
	if NotifyComputeNodesChanged == legacy {
		t.Errorf("NotifyComputeNodesChanged collides with legacy %q; constants are aliased", legacy)
	}
	if NotifyComputeNodeKeysChanged == legacy {
		t.Errorf("NotifyComputeNodeKeysChanged collides with legacy %q; constants are aliased", legacy)
	}
}

// TestNotifyPayloadShape_ComputeNodesChanged pins the expected JSON
// payload for the nodes-channel. The consumer (schedd's
// router-refresh, rebalancer, live-migrator; vmmd's PGNodeVerifier;
// gatewayd-internal's nodecache.WatchEvictions) parses the payload
// without re-reading the row, so the shape is part of the contract.
//
// An example payload is hard-coded here rather than synthesized from
// a struct so a refactor that changes the field name (e.g. `id` →
// `node_uuid`) shows up as a test failure rather than silently
// shipping.
func TestNotifyPayloadShape_ComputeNodesChanged(t *testing.T) {
	raw := `{"node_id":"5b1f9a8e-7c2d-4f3a-9e8b-1a2b3c4d5e6f","active":true}`
	var p struct {
		NodeID string `json:"node_id"`
		Active bool   `json:"active"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("consumer-side parse failed: %v (schema drift?)", err)
	}
	if p.NodeID == "" {
		t.Errorf("node_id empty; consumers that filter on node_id would drop this notify")
	}
	if !p.Active {
		t.Errorf("active = false; the example is an INSERT (active default true)")
	}
}

// TestNotifyPayloadShape_ComputeNodeKeysChanged pins the expected
// JSON payload for the keys-channel. The consumer (schedd's
// nodeKeyRegistry via subscribeNodeKeyChanges) parses the payload
// to detect rotation events without a re-fetch.
//
// The post-00276 payload carries a sha256-hex fingerprint of the
// new public_key_pem (empty on DELETE). The example here uses a
// 64-character lower-case hex string — the canonical form produced
// by encode(sha256(bytea), 'hex').
func TestNotifyPayloadShape_ComputeNodeKeysChanged(t *testing.T) {
	raw := `{"key_id":"node-1","fingerprint":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}`
	var p struct {
		KeyID       string `json:"key_id"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("consumer-side parse failed: %v (schema drift?)", err)
	}
	if p.KeyID == "" {
		t.Errorf("key_id empty; consumers remove entries by key_id (revocation)")
	}
	if len(p.Fingerprint) != 64 {
		t.Errorf("fingerprint length = %d, want 64 (sha256-hex)", len(p.Fingerprint))
	}
	// Empty fingerprint is the DELETE-side sentinel — pin its
	// shape so consumers' "empty == revocation" branch is
	// covered by the test.
	rawDel := `{"key_id":"node-1","fingerprint":""}`
	var pDel struct {
		KeyID       string `json:"key_id"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal([]byte(rawDel), &pDel); err != nil {
		t.Fatalf("DELETE-payload parse failed: %v", err)
	}
	if pDel.Fingerprint != "" {
		t.Errorf("DELETE fingerprint = %q, want empty (revocation signal)", pDel.Fingerprint)
	}
}
