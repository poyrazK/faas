//go:build !no_pg

package state_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreMirrorSlotLeasesShareCapAcrossStoresAndReclaimExpiry(t *testing.T) {
	firstStore, pool, ctx := pgStoreWithPool(t)
	secondStore := state.NewPgStore(pool)
	accountID, appID := uuid.NewString(), uuid.NewString()
	sourceDeploymentID, mirrorDeploymentID, ruleID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `
INSERT INTO accounts (id, email, plan, created_at)
VALUES ($1::uuid, $2, 'pro', now())`, accountID, accountID+"@mirror-slot-test.invalid"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO apps (id, account_id, slug, ram_mb, max_concurrency, status, created_at)
VALUES ($1::uuid, $2::uuid, $3, 128, 5, 'active', now())`, appID, accountID, "mirror-slot-"+appID); err != nil {
		t.Fatalf("insert app: %v", err)
	}
	for _, deployment := range []struct {
		id    string
		scope string
	}{{sourceDeploymentID, "source"}, {mirrorDeploymentID, "mirror"}} {
		if _, err := pool.Exec(ctx, `
INSERT INTO deployments (id, app_id, scope, image_digest, status, created_at)
VALUES ($1::uuid, $2::uuid, $3, $4, 'live', now())`, deployment.id, appID,
			deployment.scope, "sha256:"+deployment.id); err != nil {
			t.Fatalf("insert deployment %s: %v", deployment.scope, err)
		}
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO mirror_rules (
    id, account_id, app_id, source_deployment_id, mirror_deployment_id,
    percent, enabled, include_body, redact_headers
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 100, true, false, '{}'::text[])`,
		ruleID, accountID, appID, sourceDeploymentID, mirrorDeploymentID); err != nil {
		t.Fatalf("insert mirror rule: %v", err)
	}

	const cap = 3
	const attempts = 24
	start := make(chan struct{})
	type result struct {
		leaseID  string
		acquired bool
		err      error
	}
	results := make(chan result, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		store := firstStore
		if i%2 != 0 {
			store = secondStore
		}
		wg.Add(1)
		go func(store *state.PgStore) {
			defer wg.Done()
			<-start
			leaseID, acquired, err := store.TryAcquireMirrorSlotLease(context.Background(), ruleID, cap, time.Minute)
			results <- result{leaseID: leaseID, acquired: acquired, err: err}
		}(store)
	}
	close(start)
	wg.Wait()
	close(results)

	var leases []string
	for got := range results {
		if got.err != nil {
			t.Fatalf("parallel acquire: %v", got.err)
		}
		if got.acquired {
			leases = append(leases, got.leaseID)
		}
	}
	if len(leases) != cap {
		t.Fatalf("admitted reservations = %d, want fleet-wide cap %d", len(leases), cap)
	}
	for _, leaseID := range leases {
		if err := secondStore.ReleaseMirrorSlotLease(ctx, ruleID, leaseID); err != nil {
			t.Fatalf("release %s: %v", leaseID, err)
		}
	}

	expired, acquired, err := firstStore.TryAcquireMirrorSlotLease(ctx, ruleID, cap, time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire lease to expire = (%q, %v, %v)", expired, acquired, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE mirror_slot_leases SET expires_at = clock_timestamp() - interval '1 second' WHERE lease_id = $1::uuid`, expired); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	replacement, acquired, err := secondStore.TryAcquireMirrorSlotLease(ctx, ruleID, 1, time.Minute)
	if err != nil || !acquired || replacement == "" {
		t.Fatalf("replacement after expiry = (%q, %v, %v), want acquired", replacement, acquired, err)
	}
	if err := firstStore.ReleaseMirrorSlotLease(ctx, ruleID, replacement); err != nil {
		t.Fatalf("release replacement: %v", err)
	}
}
