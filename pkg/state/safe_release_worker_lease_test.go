package state

import (
	"testing"
	"time"
)

func TestMemStoreSafeReleaseWorkerLease(t *testing.T) {
	store := NewMemStore()
	health, err := store.SafeReleaseWorkerLeaseHealth(t.Context())
	if err != nil || health.Exists || health.Ready() {
		t.Fatalf("missing lease health = %+v, err=%v", health, err)
	}
	ready, err := store.SafeReleaseWorkerLeaseReady(t.Context())
	if err != nil || ready {
		t.Fatalf("missing lease: ready=%v err=%v", ready, err)
	}
	if err := store.StampSafeReleaseWorkerLease(t.Context(), 0); err == nil {
		t.Fatal("zero TTL accepted")
	}
	if err := store.StampSafeReleaseWorkerLease(t.Context(), time.Minute); err != nil {
		t.Fatal(err)
	}
	ready, err = store.SafeReleaseWorkerLeaseReady(t.Context())
	if err != nil || !ready {
		t.Fatalf("fresh lease: ready=%v err=%v", ready, err)
	}
	health, err = store.SafeReleaseWorkerLeaseHealth(t.Context())
	if err != nil || !health.Ready() || health.SecondsUntilExpiry() <= 0 {
		t.Fatalf("fresh lease health = %+v, err=%v", health, err)
	}
	store.mu.Lock()
	store.safeReleaseWorkerLeaseUntil = time.Now().Add(-time.Second)
	store.mu.Unlock()
	ready, err = store.SafeReleaseWorkerLeaseReady(t.Context())
	if err != nil || ready {
		t.Fatalf("expired lease: ready=%v err=%v", ready, err)
	}
	health, err = store.SafeReleaseWorkerLeaseHealth(t.Context())
	if err != nil || health.Ready() || health.SecondsUntilExpiry() >= 0 {
		t.Fatalf("expired lease health = %+v, err=%v", health, err)
	}
}
