package storage

import "testing"

// TestResolveCacheMaxBytes pins the sizing contract. The load-bearing case is
// the 16 GiB compute node the fleet actually runs: it must keep the 8 GiB it
// had under the old flat constant, because shrinking the cache to free page
// cache trades a ~0.85 s cold-cache restore for a ~3.8 s remote registry
// fetch — a worse tail, not a better one.
func TestResolveCacheMaxBytes(t *testing.T) {
	const gib = int64(1) << 30
	for _, tc := range []struct {
		name    string
		memHint int64
		want    int64
	}{
		{"16 GiB node keeps the previous 8 GiB", 16 * gib, 8 * gib},
		{"64 GiB reference node scales up instead of wasting RAM", 64 * gib, 32 * gib},
		{"32 GiB node", 32 * gib, 16 * gib},
		{"tiny host floors at one scale-plan layer plus snapshot", 2 * gib, 2 * gib},
		{"huge host caps so the eviction walk stays bounded", 512 * gib, 32 * gib},
		{"unknown MemTotal falls back to the flat default", 0, DefaultCacheMaxBytes},
		{"negative MemTotal falls back rather than guessing", -1, DefaultCacheMaxBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveCacheMaxBytes(tc.memHint); got != tc.want {
				t.Errorf("ResolveCacheMaxBytes(%d) = %d GiB, want %d GiB",
					tc.memHint, got/gib, tc.want/gib)
			}
		})
	}
}

// TestResolveCacheMaxBytes_NeverRegressesTheCurrentFleet is the guard that
// matters operationally: no supported node shape may end up with less cache
// than the flat 8 GiB default used to give it, because that converts warm
// cache hits into remote fetches.
func TestResolveCacheMaxBytes_NeverRegressesTheCurrentFleet(t *testing.T) {
	const gib = int64(1) << 30
	for _, mem := range []int64{16 * gib, 32 * gib, 64 * gib, 128 * gib} {
		if got := ResolveCacheMaxBytes(mem); got < DefaultCacheMaxBytes {
			t.Errorf("node with %d GiB got %d GiB, below the previous flat default of %d GiB",
				mem/gib, got/gib, DefaultCacheMaxBytes/gib)
		}
	}
}
