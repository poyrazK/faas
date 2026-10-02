//go:build !no_pg

package state

// adr: 429/422 — standards compose with ordinary lifecycle and capacity checks.

import "testing"

func TestPgInstanceApplicationStandardRefusalPreservesExclusiveOwner(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRefusalPreservesExclusiveOwner(t, s)
}

func TestPgInstanceApplicationStandardBootCapacity(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	standardNativeBootCapacity(t, s, func(id string) {
		var received bool
		if err := pool.QueryRow(t.Context(), `SELECT receipt IS NOT NULL FROM instance_application_standard_boots WHERE token=$1`, id).Scan(&received); err != nil || received {
			t.Fatalf("capacity refusal committed native receipt: %v %v", received, err)
		}
	})
}

func TestPgInstanceApplicationStandardPromotionCapacity(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	standardNativePromotionCapacity(t, s, func(id string) {
		var received bool
		if err := pool.QueryRow(t.Context(), `SELECT receipt IS NOT NULL FROM instance_application_standard_promotions WHERE token=$1`, id).Scan(&received); err != nil || received {
			t.Fatalf("capacity refusal committed promotion receipt: %v %v", received, err)
		}
	})
}
