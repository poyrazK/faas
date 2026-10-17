package builderd

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// fakeFSUsage reports a 1000-byte filesystem whose free space grows by
// freedPerEntry for every seeded entry Sweep has removed.
func fakeFSUsage(baseAvail, freedPerEntry uint64, entries []string) func(string) (uint64, uint64, error) {
	return func(string) (uint64, uint64, error) {
		avail := baseAvail
		for _, e := range entries {
			if _, err := os.Stat(e); errors.Is(err, os.ErrNotExist) {
				avail += freedPerEntry
			}
		}
		return avail, 1000, nil
	}
}

// TestCacheSweep_FreeSpaceFloor pins H10-109: the 50 GiB size cap exceeds
// the 48 GB root disk the cache shares with the rest of the host, so the
// sweep must also evict oldest-first while that filesystem is below its
// free-space floor.
func TestCacheSweep_FreeSpaceFloor(t *testing.T) {
	cases := map[string]struct {
		fraction      float64
		baseAvail     uint64
		freedPerEntry uint64
		usageErr      error
		wantGone      int // oldest-first
	}{
		"below the floor evicts oldest until above": {fraction: 0.20, baseAvail: 100, freedPerEntry: 100, wantGone: 1},
		"above the floor keeps every entry":         {fraction: 0.20, baseAvail: 300, freedPerEntry: 100},
		"floor disabled":                            {fraction: 0, baseAvail: 0, freedPerEntry: 100},
		"unreachable floor empties the cache":       {fraction: 0.20, baseAvail: 0, freedPerEntry: 10, wantGone: 3},
		"filesystem check error keeps entries":      {fraction: 0.20, usageErr: errors.New("statfs failed")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := NewCache(t.TempDir()).WithMinFreeFraction(tc.fraction)
			now := time.Now()
			entries := []string{
				seedEntry(t, c, "floor1111111111111111111", FrameworkNode, 100, now.Add(-3*time.Hour)),
				seedEntry(t, c, "floor2222222222222222222", FrameworkNode, 100, now.Add(-2*time.Hour)),
				seedEntry(t, c, "floor3333333333333333333", FrameworkNode, 100, now.Add(-1*time.Hour)),
			}
			c.fsUsage = fakeFSUsage(tc.baseAvail, tc.freedPerEntry, entries)
			if tc.usageErr != nil {
				c.fsUsage = func(string) (uint64, uint64, error) { return 0, 0, tc.usageErr }
			}

			// No TTL and no size cap: only the floor may evict.
			n, err := c.Sweep(0, 100*365*24*time.Hour, now, quietLogger())
			if err != nil {
				t.Fatal(err)
			}
			if n != tc.wantGone {
				t.Errorf("swept = %d, want %d", n, tc.wantGone)
			}
			for i, e := range entries {
				_, statErr := os.Stat(e)
				if gone := errors.Is(statErr, os.ErrNotExist); gone != (i < tc.wantGone) {
					t.Errorf("entry %d gone = %v, want %v", i, gone, i < tc.wantGone)
				}
			}
		})
	}
}

// TestCacheGCSweepLoopSweepsAtStartup pins the other half of H10-109: the
// loop only swept on its first tick, and builderd restarts on every
// rollout, so on a fleet releasing more often than the interval the cache
// was never swept.
func TestCacheGCSweepLoopSweepsAtStartup(t *testing.T) {
	c := NewCache(t.TempDir())
	now := time.Now()
	stale := seedEntry(t, c, "stale000000000000000000", FrameworkNode, 100, now.Add(-31*24*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		CacheGCSweepLoop(ctx, c, time.Hour, 0, 30*24*time.Hour, quietLogger())
	}()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(stale); errors.Is(err, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("stale entry survived 5s of a sweep loop with a one-hour interval; the loop does not sweep at startup")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
