// adr: 520
package state_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func requireDomainState(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestCustomDomainCertificateRetryMigrationIndexIsUsable(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	var valid, ready, validated bool
	requireDomainState(t, pool.QueryRow(ctx, `SELECT i.indisvalid,i.indisready,c.convalidated
	 FROM pg_index i CROSS JOIN pg_constraint c
	 WHERE i.indexrelid='custom_domains_cert_retry_due_idx'::regclass
	 AND c.conrelid='custom_domains'::regclass AND c.conname='custom_domains_cert_retry_at_check'`).Scan(&valid, &ready, &validated))
	if !valid || !ready || !validated {
		t.Fatalf("retry migration incomplete: valid=%v ready=%v constraint=%v", valid, ready, validated)
	}
}

func TestCustomDomainDoctorQueueExcludesDeletedSurfaces(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			reader := raw.(interface {
				ListAllCustomDomainsForDoctor(context.Context) ([]string, error)
				ListCustomDomainsForDoctorBatch(context.Context, int) ([]string, error)
			})
			appID := tlsHostApp(t, ctx, raw, "")
			app, err := store.AppByID(ctx, appID)
			requireDomainState(t, err)
			limits := api.MustLimitsFor(api.PlanPro)
			surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: app.AccountID, AppID: appID, Name: "retired"}, limits)
			requireDomainState(t, err)
			zone := uuid.NewString()[:8] + ".doctor.example.com"
			for n := range 3 {
				_, err = store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: fmt.Sprintf("dead-%d.%s", n, zone), ChallengeToken: "proof"}, limits)
				requireDomainState(t, err)
			}
			live, overlap := "live."+zone, "dead-0."+zone
			for _, host := range []string{live, overlap} {
				verifiedTLSWildcard(t, ctx, raw, host, appID)
			}
			requireDomainState(t, store.DeleteTenantSurface(ctx, surface.ID))
			for pass := range 2 {
				batch, err := reader.ListCustomDomainsForDoctorBatch(ctx, 2)
				requireDomainState(t, err)
				all, err := reader.ListAllCustomDomainsForDoctor(ctx)
				requireDomainState(t, err)
				for _, got := range [][]string{batch, all} {
					if len(got) != 2 || !slices.Contains(got, live) || !slices.Contains(got, overlap) {
						t.Fatalf("pass %d: deleted hostnames displaced live custom claims: %v", pass, got)
					}
				}
			}
		})
	}
}

func TestCustomDomainCertificateAttemptLeaseAndRetryDiscovery(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			reader := raw.(state.CustomDomainCertificateRetryReader)
			attempts := raw.(state.CustomDomainCertificateAttemptStore)
			appID := tlsHostApp(t, ctx, raw, "")
			zone := uuid.NewString()[:8] + ".retry.example.com"
			wildcard, exact, pending := "*."+zone, "exact."+zone, "*.pending."+zone
			verifiedTLSWildcard(t, ctx, raw, wildcard, appID)
			verifiedTLSWildcard(t, ctx, raw, exact, appID)
			_, err := store.CreateCustomDomain(ctx, pending, appID, "pending-proof")
			requireDomainState(t, err)
			claim, err := store.DomainByName(ctx, wildcard)
			requireDomainState(t, err)
			now := time.Now().UTC().Truncate(time.Microsecond)
			delay := api.CustomDomainCertRetryDelay
			jobs, err := reader.ListCustomDomainCertificateRetries(ctx, now, now.Add(30*24*time.Hour), 10)
			requireDomainState(t, err)
			if len(jobs) != 1 || !state.SameCustomDomainClaim(jobs[0], claim) {
				t.Fatalf("retry inventory: %+v", jobs)
			}
			var winners atomic.Int32
			var workers sync.WaitGroup
			for range 16 {
				workers.Go(func() {
					won, err := attempts.BeginCustomDomainCertificateAttempt(ctx, claim, now, delay)
					if err != nil {
						t.Error(err)
					}
					if won {
						winners.Add(1)
					}
				})
			}
			workers.Wait()
			if winners.Load() != 1 {
				t.Fatalf("attempt lease acquired %d times", winners.Load())
			}
			jobs, err = reader.ListCustomDomainCertificateRetries(ctx, now, now.Add(30*24*time.Hour), 10)
			requireDomainState(t, err)
			if len(jobs) != 0 {
				t.Fatalf("leased claim still due: %+v", jobs)
			}
			// Discovery must allow a failed attempt after its durable cooldown.
			requireDomainState(t, store.UpdateCustomDomainCertStatus(ctx, wildcard, state.CustomDomainCertFailed, time.Time{}, "CA unavailable", now))
			jobs, err = reader.ListCustomDomainCertificateRetries(ctx, now.Add(delay), now.Add(30*24*time.Hour), 10)
			requireDomainState(t, err)
			if len(jobs) != 1 {
				t.Fatalf("failed claim did not become retryable: %+v", jobs)
			}
			won, err := attempts.BeginCustomDomainCertificateAttempt(ctx, claim, now.Add(delay), delay)
			requireDomainState(t, err)
			if !won {
				t.Fatal("cooldown never released the attempt")
			}
			expiry := now.Add(90 * 24 * time.Hour)
			requireDomainState(t, store.UpdateCustomDomainCertStatus(ctx, wildcard, state.CustomDomainCertIssued, expiry, "", now))
			jobs, err = reader.ListCustomDomainCertificateRetries(ctx, now.Add(2*delay), now.Add(30*24*time.Hour), 10)
			requireDomainState(t, err)
			if len(jobs) != 0 {
				t.Fatalf("healthy certificate queued too soon: %+v", jobs)
			}
			jobs, err = reader.ListCustomDomainCertificateRetries(ctx, now.Add(61*24*time.Hour), now.Add(91*24*time.Hour), 10)
			requireDomainState(t, err)
			if len(jobs) != 1 {
				t.Fatalf("expiring wildcard was not queued: %+v", jobs)
			}
			// Replacement claims do not inherit the previous owner's cooldown,
			// and a queued attempt cannot consume the new claim's lease.
			requireDomainState(t, store.DeleteCustomDomain(ctx, wildcard))
			_, err = store.CreateCustomDomain(ctx, wildcard, appID, "new-proof")
			requireDomainState(t, err)
			requireDomainState(t, store.MarkDomainVerified(ctx, wildcard))
			won, err = attempts.BeginCustomDomainCertificateAttempt(ctx, claim, now, delay)
			requireDomainState(t, err)
			if won {
				t.Fatal("old claim acquired replacement attempt")
			}
			fresh, err := store.DomainByName(ctx, wildcard)
			requireDomainState(t, err)
			won, err = attempts.BeginCustomDomainCertificateAttempt(ctx, fresh, now, delay)
			requireDomainState(t, err)
			if !won {
				t.Fatal("new claim inherited old cooldown")
			}
		})
	}
}
