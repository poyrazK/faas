package fcvm

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// 200 concurrent refused boots against one fail-closed sidecar make one
// canonical read per interval, not one each (bug hunt O36).
func TestScanRefusalRefreshIsCoalesced(t *testing.T) {
	var c scanRefreshCoalescer
	var reads atomic.Int32
	release := make(chan struct{})
	refresh := func(context.Context) bool {
		reads.Add(1)
		<-release
		return false
	}
	now := time.Unix(1_800_000_000, 0)
	var wg sync.WaitGroup
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c.do(context.Background(), "scan/base", now, refresh) {
				t.Error("refusal refresh reported a refreshed copy")
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := reads.Load(); got != 1 {
		t.Fatalf("canonical reads = %d, want 1", got)
	}
	// Inside the interval the result is reused; another key has its own.
	c.do(context.Background(), "scan/base", now.Add(api.VmmdScanRefusalRefreshTTL-time.Second), refresh)
	c.do(context.Background(), "scan/other", now, refresh)
	if got := reads.Load(); got != 2 {
		t.Fatalf("canonical reads = %d, want 2 (one per key)", got)
	}
	// After it the sidecar is read again.
	c.do(context.Background(), "scan/base", now.Add(api.VmmdScanRefusalRefreshTTL), refresh)
	if got := reads.Load(); got != 3 {
		t.Fatalf("canonical reads = %d, want 3 after the interval", got)
	}
}
