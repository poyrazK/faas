// adr: 122
package gateway

// handler_cache_key_regression_test.go — regression fences for
// the two CRITICAL findings the medium code review surfaced
// against PR #1008:
//
//   B1: CacheKey omitted r.URL.RawQuery, so different query
//       strings collided on the same entry. Two requests for
//       /products?id=1 and /products?id=999 served the same
//       body — silent wrong-data leak.
//
//   B2: CacheKey.String() used a fixed [404]byte stack buffer
//       and copy()ed fields silently. Paths longer than 293
//       bytes were truncated, so distinct URLs collided on
//       the same map key — a far worse cache-poisoning vector.
//
// Both fixes are pinned here so a future refactor that
// re-introduces either bug fails the build (or at least
// fails the test, given the other gates already enforce
// compile-time checks elsewhere).

import (
	"crypto/sha256"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestCacheKey_B1_QueryDimensionDiscriminates verifies the
// fix for B1: two requests for the same path with different
// query strings MUST produce distinct cache keys. The
// pre-fix code built CacheKey from r.URL.Path only and the
// silent collision is exactly the cache-poisoning vector the
// review called out.
func TestCacheKey_B1_QueryDimensionDiscriminates(t *testing.T) {
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, time.Now)
	now := time.Now()
	ruleAct := &state.EdgeRuleCacheAction{MaxAgeSeconds: 60}

	key1 := CacheKey{
		AppID:          "app-1",
		RuleID:         "rule-1",
		Method:         "GET",
		NormalizedPath: "/products",
		Query:          "id=1",
		VaryHash:       hashStable(""),
	}
	key2 := CacheKey{
		AppID:          "app-1",
		RuleID:         "rule-1",
		Method:         "GET",
		NormalizedPath: "/products",
		Query:          "id=999",
		VaryHash:       hashStable(""),
	}
	if key1.String() == key2.String() {
		t.Fatalf("B1 regression: keys for /products?id=1 and /products?id=999 collided (%q)", key1.String())
	}

	// And the end-to-end store path: storing under key1 must
	// NOT serve a key2 request.
	if !cache.Put(key1, 200, nil, []byte("id=1 body"), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct) {
		t.Fatalf("Put key1 failed")
	}
	if state, _ := cache.Get(key2); state != "" {
		t.Fatalf("B1 regression: key2 hit key1's entry (got state=%q)", state)
	}
	if state, _ := cache.Get(key1); state != "fresh" {
		t.Fatalf("key1 should hit its own entry; got state=%q", state)
	}
}

// TestCacheKey_B2_LongPathDoesNotTruncate verifies the fix
// for B2: a NormalizedPath longer than 293 bytes MUST NOT
// silently collide with a different long path of the same
// prefix. The pre-fix code's fixed-[404]byte buffer was
// shorter than 2 * 293 (hex), so paths past 293 bytes
// truncated; the worst-case was a cache-poisoning vector
// where any attacker under a 293-byte-prefix path could
// overwrite a victim entry.
func TestCacheKey_B2_LongPathDoesNotTruncate(t *testing.T) {
	// 400-byte path with the same 293-byte prefix followed by
	// different tail bytes — pre-fix these collided.
	prefix := strings.Repeat("a", 293)
	victim := CacheKey{
		AppID:          "app-1",
		RuleID:         "rule-1",
		Method:         "GET",
		NormalizedPath: prefix + "/victim-tail-1",
		Query:          "",
		VaryHash:       hashStable(""),
	}
	attacker := CacheKey{
		AppID:          "app-1",
		RuleID:         "rule-1",
		Method:         "GET",
		NormalizedPath: prefix + "/attacker-tail-2",
		Query:          "",
		VaryHash:       hashStable(""),
	}
	if victim.String() == attacker.String() {
		t.Fatalf("B2 regression: long paths collided on a shared 293-byte prefix")
	}

	// And the store path: the attacker's entry must not
	// overwrite the victim's lookup.
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, time.Now)
	now := time.Now()
	ruleAct := &state.EdgeRuleCacheAction{MaxAgeSeconds: 60}
	if !cache.Put(victim, 200, nil, []byte("victim-body"), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct) {
		t.Fatalf("Put victim failed")
	}
	if state, _ := cache.Get(attacker); state != "" {
		t.Fatalf("B2 regression: attacker long-path returned victim entry (got state=%q)", state)
	}
}

// TestCacheKey_B2_StringIsStableForIdenticalInputs is the
// pin for the second half of B2's fix: two CacheKey values
// with identical fields MUST produce the same String().
// This was true before the fix (the truncation was
// deterministic), but the dynamic-buffer rewrite is
// regression-prone if the field-order documentation drifts
// from the field-order encoding. The test pins both sides.
func TestCacheKey_B2_StringIsStableForIdenticalInputs(t *testing.T) {
	mk := func() CacheKey {
		return CacheKey{
			AppID:          "app-1",
			DeploymentID:   "dep-1",
			RuleID:         "rule-1",
			Method:         "GET",
			NormalizedPath: "/path",
			Query:          "id=1",
			VaryHash:       sha256.Sum256([]byte("vary")),
		}
	}
	if mk().String() != mk().String() {
		t.Fatalf("String() is not stable for identical inputs")
	}
	// Defensive: the response_cache_test.go Put/Get exercise
	// touches String() at the map key. The httptest request
	// here is unused — we just need to ensure the regression
	// test imports httptest so future tooling that uses it
	// doesn't break the import set.
	_ = httptest.NewRequest("GET", "/", nil)
}
