//go:build !no_pg

// adr: 375
package state

import (
	"context"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func pgTrafficTenantIntent(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var digest string
	err := pool.QueryRow(t.Context(), `SELECT md5(jsonb_build_object(
		'tenants',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM platform_tenants t),
		'consumers',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM api_consumers t),
		'surfaces',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM tenant_surfaces t),
		'hostnames',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM tenant_hostnames t),
		'keys',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM consumer_keys t),
		'tokens',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM platform_tenant_access_tokens t),
		'hostname_policy',(SELECT jsonb_agg(to_jsonb(t) ORDER BY tenant_id) FROM platform_tenant_hostname_policies t),
		'credential_policy',(SELECT jsonb_agg(to_jsonb(t) ORDER BY tenant_id) FROM platform_tenant_credential_policies t),
		'consumer_policy',(SELECT jsonb_agg(to_jsonb(t) ORDER BY tenant_id) FROM platform_tenant_consumer_provisioning_policies t),
		'reconciliation',(SELECT jsonb_agg(to_jsonb(t) ORDER BY receipt_id) FROM platform_tenant_reconciliation_receipts t),
		'offboarding',(SELECT jsonb_agg(to_jsonb(t) ORDER BY receipt_id) FROM platform_tenant_offboarding_receipts t),
		'webhook_outbox',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM app_webhook_event_outbox t),
		'webhooks',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM app_webhook_deliveries t)
	)::text)`).Scan(&digest)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestPgTrafficTenantCompleteTransitions(t *testing.T) {
	for _, mode := range trafficTenantRemovalModes {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			testTrafficTenantRemoval(t, store, account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficTenantIntent(t, pool) })
		})
	}
	t.Run("bulk-link", func(t *testing.T) {
		store, pool, account, app := trafficHostPGFixture(t)
		testTrafficTenantBulkLink(t, store, account, app, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficTenantIntent(t, pool) })
	})
}

func TestPgTrafficTenantHostnameRemovalReclaimDuringLockWait(t *testing.T) {
	for _, owned := range []bool{false, true} {
		t.Run(fmt.Sprint(owned), func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			surface, host := newTrafficTenantClaim(t, store, account, app, "reclaim.tenant.example.test")
			peerAccount, peer := trafficTenantTransitionPeer(t, store)
			peerSurface, err := store.CreateTenantSurfaceIfUnderQuota(t.Context(), CreateTenantSurfaceParams{AccountID: peerAccount.ID, AppID: peer.ID, Name: "reclaim-peer"}, api.MustLimitsFor(peerAccount.Plan))
			if err != nil {
				t.Fatal(err)
			}
			_, release, busy, err := store.tryAcquireTrafficPolicySession(t.Context(), uuidToPgtype(account.ID), false)
			if err != nil || busy {
				t.Fatalf("account lock: %v", err)
			}
			defer release(t.Context())
			waiting, application := domainRemovalWaitingStore(t, pool)
			result := make(chan error, 1)
			go func() {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if owned {
					result <- waiting.DeleteTenantHostnameForSurface(ctx, host.Hostname, surface.ID)
				} else {
					result <- waiting.DeleteTenantHostname(ctx, host.Hostname)
				}
			}()
			awaitDomainRemovalSessionWait(t, pool, application)
			// Legacy handoff while the canonical writer waits: all current
			// tenant mutations now share this lock and cannot race this way.
			if _, err := pool.Exec(t.Context(), `DELETE FROM tenant_hostnames WHERE id=$1::uuid`, host.ID); err != nil {
				t.Fatal(err)
			}
			replacement, err := scanTenantHostname(pool.QueryRow(t.Context(), `INSERT INTO tenant_hostnames(surface_id,hostname,challenge_token) VALUES($1::uuid,$2,$3) RETURNING `+tenantHostnameCols, peerSurface.ID, host.Hostname, host.ChallengeToken))
			if err != nil {
				t.Fatal(err)
			}
			release(t.Context())
			if err := <-result; !errors.Is(err, ErrNotFound) {
				t.Fatalf("stale authorization removed replacement: %v", err)
			}
			if current, err := store.GetTenantHostnameByName(t.Context(), host.Hostname); err != nil || !reflect.DeepEqual(current, replacement) {
				t.Fatal("reclaim changed foreign intent")
			}
			if err := waiting.DeleteTenantHostnameForSurface(t.Context(), host.Hostname, surface.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("wrong owner removed replacement: %v", err)
			}
			if err := waiting.DeleteTenantHostnameForSurface(t.Context(), host.Hostname, peerSurface.ID); err != nil {
				t.Fatalf("single-connection pool failed after refusal: %v", err)
			}
		})
	}
}

func TestPgTrafficTenantReconciliationValidatesFinalTopology(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	testTrafficTenantReconciliationFinalTopology(t, store, account, app, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func(id string) {
		if _, err := pool.Exec(t.Context(), `UPDATE tenant_surfaces SET platform_tenant_id=NULL WHERE id=$1::uuid`, id); err != nil {
			t.Fatal(err)
		}
	}, func() string { return pgTrafficTenantIntent(t, pool) })
}
