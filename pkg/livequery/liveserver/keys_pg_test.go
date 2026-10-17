package liveserver

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// keyTable is the control plane's live keys for one database.
type keyTable struct {
	mu   sync.Mutex
	keys []livekeys.SealedKey
}

func (k *keyTable) set(keys ...livekeys.SealedKey) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys = keys
}

func (k *keyTable) ring(_ context.Context, database string) ([]livekeys.SealedKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if database != "db_1" {
		return nil, nil
	}
	return append([]livekeys.SealedKey(nil), k.keys...), nil
}

// opens reports, on the hub's loop, whether its ring opens kid.
func (f *fixture) opens(kid string) bool {
	var ok bool
	f.sync(func() { ok = f.hub.keys.Opens(kid, f.clock.Now()) })
	return ok
}

func (f *fixture) waitKeys(what string, cond func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// adr: 1384 — livequeryd opens seals with the keys apid stored for its
// database, and removing a key (rotation without overlap, disabling live
// queries) closes the subscriptions its seals opened.
func TestHubWatchesItsKeys(t *testing.T) {
	f := newFixture(t, schema, Config{})
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	first, firstSecret := sealedKey(t, identity.Recipient(), livekeys.SealNamespace)
	second, secondSecret := sealedKey(t, identity.Recipient(), livekeys.SealNamespace)
	table := &keyTable{}
	table.set(first)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		WatchKeys(ctx, f.hub, table.ring, []*age.X25519Identity{identity}, 10*time.Millisecond, slog.New(slog.NewTextHandler(testLog{t}, nil)))
	}()
	t.Cleanup(func() { cancel(); <-done })
	f.waitKeys("the first key", func() bool { return f.opens(first.KID) })
	if f.opens(f.secret.KID) {
		t.Fatal("the hub kept a key the control plane does not hold")
	}

	c := f.dial()
	feed := `SELECT id, body FROM messages WHERE channel_id = $1`
	c.subscribe("a", f.seal(firstSecret, "db_1", feed, time.Minute, "1"))
	c.until("the first key's result", func() bool { return c.results["a"].Ready() })

	// Rotation with overlap: both keys open seals.
	second.RetiresAt = time.Time{}
	first.RetiresAt = f.clock.Now().Add(time.Hour)
	table.set(first, second)
	f.waitKeys("the second key", func() bool { return f.opens(second.KID) })
	c.subscribe("b", f.seal(secondSecret, "db_1", feed, time.Minute, "2"))
	c.until("the second key's result", func() bool { return c.results["b"].Ready() })
	if c.results["a"].Closed {
		t.Fatal("an overlapping rotation closed a subscription")
	}

	// Removing the first key revokes what its seals opened, and only that.
	table.set(second)
	c.until("the revocation", func() bool { return c.results["a"].Closed })
	if e := c.lastError("a"); e.Code != liveproto.CodeSealRejected || !e.Closed {
		t.Fatalf("revocation error %+v", e)
	}
	xid := f.write(`INSERT INTO messages VALUES (1, 2, 'after revocation')`)
	c.until("the surviving subscription's change", func() bool { return c.results["b"].Sees(xid) })
	if got := values(c.results["b"]); got != "1:after revocation" {
		t.Fatalf("surviving result %q", got)
	}

	// Disabling live queries removes every key.
	table.set()
	c.until("the last revocation", func() bool { return c.results["b"].Closed })
	f.waitSubscriptions(0, "after every key was removed")
}
