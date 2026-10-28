package auth

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Each Argon2id hash allocates 64 MiB; about eight at once OOM-killed apid
// (bug hunt 2026-10-27 H20). No more than api.PasswordHashConcurrency run
// at once.
func TestPasswordHashesAreBounded(t *testing.T) {
	var running, peak atomic.Int32
	var wg sync.WaitGroup
	for range 4 * api.PasswordHashConcurrency {
		wg.Go(func() {
			hashSlots <- struct{}{}
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			running.Add(-1)
			<-hashSlots
		})
	}
	wg.Wait()
	if got := int(peak.Load()); got > api.PasswordHashConcurrency {
		t.Fatalf("concurrent hashes = %d, want at most %d", got, api.PasswordHashConcurrency)
	}
	if cap(hashSlots) != api.PasswordHashConcurrency {
		t.Fatalf("hash slots = %d, want %d", cap(hashSlots), api.PasswordHashConcurrency)
	}
	phc, err := Encode("bounded-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := Verify(phc, "bounded-horse-battery-staple"); !ok || err != nil {
		t.Fatalf("Verify through the bounded path = %v, %v", ok, err)
	}
}
