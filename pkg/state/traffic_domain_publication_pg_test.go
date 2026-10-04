//go:build !no_pg

// adr: 531
package state

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func publicationPgOutboxCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM org_activity_outbox`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPgTrafficDomainPublicationShadowAndActivation(t *testing.T) {
	for _, form := range trafficDomainPublicationForms {
		t.Run(form, func(t *testing.T) {
			store, pool, account, _, app, environment := trafficEnvironmentPGFixture(t)
			peerAccount, peer := globalTrafficPeer(t, store, "publication-peer")
			testTrafficDomainPublicationShadowAndActivation(t, store, account, app, environment, peerAccount, peer, form,
				func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() int { return publicationPgOutboxCount(t, pool) })
		})
	}
}

func TestPgTrafficDomainPublicationRetainsForeignShadow(t *testing.T) {
	for _, mode := range []string{"exact-verified", "exact-unverified", "wildcard-verified", "wildcard-unverified"} {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			_, peer := globalTrafficPeer(t, store, "publication-peer")
			testTrafficDomainPublicationRetainsForeignShadow(t, store, account, app, peer, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) })
		})
	}
}

func TestPgTrafficDomainPublicationCancellation(t *testing.T) {
	for _, form := range trafficDomainPublicationForms {
		t.Run(form, func(t *testing.T) {
			store, pool, _, _, app, environment := trafficEnvironmentPGFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			d, id, err := createTrafficPublicationDomain(ctx, store, form, "canceled.example.test", app.ID, environment.ID)
			if !errors.Is(err, context.Canceled) || d.Domain != "" || id != 0 || publicationPgOutboxCount(t, pool) != 0 {
				t.Fatalf("canceled creation published intent: domain=%q outbox=%d err=%v", d.Domain, id, err)
			}
			if _, err := store.DomainByName(t.Context(), "canceled.example.test"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("canceled creation retained its claim: %v", err)
			}
		})
	}
}

func TestPgTrafficDomainPublicationLocksOverlappingAndGlobalRouteOwners(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	peerAccount, peer := globalTrafficPeer(t, store, "overlapping-owner")
	routeAccount, routeApp := globalTrafficPeer(t, store, "global-route-owner")
	if _, err := store.CreateCustomDomain(t.Context(), "*.example.test", peer.ID, "private-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEdgeRule(t.Context(), trafficHostRule(routeAccount, routeApp, "unrelated.example.org")); err != nil {
		t.Fatal(err)
	}
	tx, err := store.beginTrafficDomainPublication(t.Context(), "new.example.test", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	for _, owner := range []string{account.ID, peerAccount.ID, routeAccount.ID} {
		_, release, busy, err := store.tryAcquireTrafficPolicySession(t.Context(), uuidToPgtype(owner), false)
		if release != nil {
			release(t.Context())
		}
		if err != nil || !busy {
			t.Fatalf("publication omitted owner lock: busy=%v err=%v", busy, err)
		}
	}
	_, release, busy, err := store.tryAcquireTrafficPolicySessionKeys(t.Context(), []string{globalTrafficRoutesLock})
	if release != nil {
		release(t.Context())
	}
	if err != nil || !busy {
		t.Fatalf("publication omitted global reservation lock: busy=%v err=%v", busy, err)
	}
	if publicationPgOutboxCount(t, pool) != 0 {
		t.Fatal("read-only binding guard published activity")
	}
}

func TestPgTrafficDomainVerificationClaimReplacementDuringLockWait(t *testing.T) {
	for _, mode := range []string{"same-app-token", "foreign-app-challenge", "foreign-app-plain"} {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			_, peer := globalTrafficPeer(t, store, "reclaimed-verification")
			const domain = "verification-reclaimed.example.test"
			if _, err := store.CreateCustomDomain(t.Context(), domain, app.ID, "original-private-token"); err != nil {
				t.Fatal(err)
			}
			_, release, busy, err := store.tryAcquireTrafficPolicySession(t.Context(), uuidToPgtype(account.ID), false)
			if err != nil || busy {
				t.Fatalf("owner lock: busy=%v err=%v", busy, err)
			}
			defer release(t.Context())
			waiting, application := domainRemovalWaitingStore(t, pool)
			result := make(chan error, 1)
			go func() {
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				if mode == "foreign-app-plain" {
					result <- waiting.MarkDomainVerified(ctx, domain)
					return
				}
				matched, err := waiting.MarkDomainVerifiedIfChallenge(ctx, domain, "original-private-token")
				if matched {
					err = errors.New("stale challenge verified replacement")
				}
				result <- err
			}()
			awaitDomainRemovalSessionWait(t, pool, application)
			owner, token := peer.ID, "original-private-token"
			if mode == "same-app-token" {
				owner, token = app.ID, "replacement-private-token"
			}
			// Model the previous lock holder's legacy claim handoff. A current
			// guarded creator also needs this lock and cannot race the verifier.
			if _, err := pool.Exec(t.Context(), `UPDATE custom_domains SET app_id=$2,challenge_token=$3 WHERE domain=$1`, domain, owner, token); err != nil {
				t.Fatal(err)
			}
			replacement, err := store.DomainByName(t.Context(), domain)
			if err != nil {
				t.Fatal(err)
			}
			release(t.Context())
			err = <-result
			if mode == "foreign-app-plain" {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("plain verification rebased: %v", err)
				}
			} else if err != nil {
				t.Fatalf("stale challenge result: %v", err)
			}
			if after, err := store.DomainByName(t.Context(), domain); err != nil || !reflect.DeepEqual(replacement, after) {
				t.Fatalf("replacement changed: app=%q err=%v", after.AppID, err)
			}
			if matched, err := waiting.MarkDomainVerifiedIfChallenge(t.Context(), domain, token); err != nil || !matched {
				t.Fatalf("replacement could not verify after stale waiter: matched=%v err=%v", matched, err)
			}
		})
	}
}

func TestPgTrafficDomainCreationCancellationWhileWaitingPreservesQuotaAndActivity(t *testing.T) {
	store, pool, account, _, app, environment := trafficEnvironmentPGFixture(t)
	_, peer := globalTrafficPeer(t, store, "creation-blocker")
	const domain = "waiting-publication.example.test"
	if _, err := store.CreateCustomDomain(t.Context(), domain, peer.ID, "prior-private-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE custom_domains SET verification_expires_at=clock_timestamp()-interval '1 second' WHERE domain=$1`, domain); err != nil {
		t.Fatal(err)
	}
	before, err := store.DomainByName(t.Context(), domain)
	if err != nil {
		t.Fatal(err)
	}
	_, release, busy, err := store.tryAcquireTrafficPolicySession(t.Context(), uuidToPgtype(account.ID), false)
	if err != nil || busy {
		t.Fatalf("destination account lock: busy=%v err=%v", busy, err)
	}
	defer release(t.Context())
	waiting, application := domainRemovalWaitingStore(t, pool)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		claim, id, err := createTrafficPublicationDomain(ctx, waiting, "environment_activity", domain, app.ID, environment.ID)
		if claim.Domain != "" || id != 0 {
			err = errors.New("canceled creation returned published intent")
		}
		result <- err
	}()
	awaitDomainRemovalSessionWait(t, pool, application)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lock wait: %v", err)
	}
	if after, err := store.DomainByName(t.Context(), domain); err != nil || !reflect.DeepEqual(before, after) || publicationPgOutboxCount(t, pool) != 0 {
		t.Fatalf("canceled reclaim changed claim or activity: app=%q err=%v", after.AppID, err)
	}
	release(t.Context())
	// The same one-connection pool must be reusable, and the canceled claim
	// must not consume the destination's sole pending-domain quota slot.
	retry, stop := context.WithTimeout(t.Context(), 2*time.Second)
	defer stop()
	if claim, err := waiting.CreateCustomDomainIfUnderQuota(retry, domain, app.ID, "fresh-private-token", 1, 1); err != nil || claim.AppID != app.ID {
		t.Fatalf("cancellation leaked locks, connection or quota: app=%q err=%v", claim.AppID, err)
	}
}
