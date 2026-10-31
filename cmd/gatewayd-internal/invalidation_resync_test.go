// adr: 1344 — notifications are not durable (§5 rule 3): after a LISTEN
// reconnect or a full hub buffer the gateway reloads every cache they drive,
// because its route and app caches have no TTL.
package main

import (
	"testing"
	"time"
)

func TestFlushAfterMissedNotificationsDropsEveryNotificationCache(t *testing.T) {
	inv := &fakeInvalidator{}
	flushAfterMissedNotifications(inv, testLogger())
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if inv.flushCnt != 1 || inv.resetCnt != 1 || inv.publicAuthCnt != 1 || inv.responseCacheAll != 1 {
		t.Fatalf("flushes: routes=%d edge rules=%d public auth=%d response cache=%d, want one each",
			inv.flushCnt, inv.resetCnt, inv.publicAuthCnt, inv.responseCacheAll)
	}
}

// A burst that keeps overflowing the hub gets one flush at once and one when
// the interval ends, not one per dropped delivery.
func TestResyncFlusherLimitsFlushesToOnePerInterval(t *testing.T) {
	f := resyncFlusher{interval: 5 * time.Second}
	start := time.Unix(1_000, 0)
	if now, _ := f.request(start); !now {
		t.Fatal("the first resync did not flush at once")
	}
	f.flushed(start)
	now, wait := f.request(start.Add(2 * time.Second))
	if now || wait != 3*time.Second {
		t.Fatalf("a resync 2 s after a flush = now %v, wait %s; want a flush in 3 s", now, wait)
	}
	if now, _ := f.request(start.Add(5 * time.Second)); !now {
		t.Fatal("a resync one interval after a flush did not flush at once")
	}
}
