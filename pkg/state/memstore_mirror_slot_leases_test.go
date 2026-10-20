package state_test

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestMemStoreMirrorSlotLeaseCapacityReleaseAndExpiry(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	(*m.MirrorRules())["rule-1"] = state.MirrorRule{ID: "rule-1"}

	first, acquired, err := m.TryAcquireMirrorSlotLease(context.Background(), "rule-1", 1, time.Minute)
	if err != nil || !acquired || first == "" {
		t.Fatalf("first acquire = (%q, %v, %v), want lease", first, acquired, err)
	}
	if leaseID, acquired, err := m.TryAcquireMirrorSlotLease(context.Background(), "rule-1", 1, time.Minute); err != nil || acquired || leaseID != "" {
		t.Fatalf("over-cap acquire = (%q, %v, %v), want empty/false/nil", leaseID, acquired, err)
	}
	if err := m.ReleaseMirrorSlotLease(context.Background(), "rule-1", first); err != nil {
		t.Fatalf("release: %v", err)
	}
	second, acquired, err := m.TryAcquireMirrorSlotLease(context.Background(), "rule-1", 1, time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire after release = (%q, %v, %v), want lease", second, acquired, err)
	}
	(*m.Mu()).Lock()
	lease := (*m.MirrorSlotLeases())[second]
	lease.ExpiresAt = time.Now().UTC().Add(-time.Second)
	(*m.MirrorSlotLeases())[second] = lease
	(*m.Mu()).Unlock()
	third, acquired, err := m.TryAcquireMirrorSlotLease(context.Background(), "rule-1", 1, time.Minute)
	if err != nil || !acquired || third == "" {
		t.Fatalf("acquire after expiry = (%q, %v, %v), want replacement lease", third, acquired, err)
	}
}
