package state

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCustomDomainExpiredClaimReclaimAndChallengeCAS(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	firstAccount, _ := store.CreateAccount(ctx, "domain-first@example.test", api.PlanScale)
	secondAccount, _ := store.CreateAccount(ctx, "domain-second@example.test", api.PlanScale)
	firstApp, _ := store.CreateApp(ctx, App{AccountID: firstAccount.ID, Slug: "domain-first"})
	secondApp, _ := store.CreateApp(ctx, App{AccountID: secondAccount.ID, Slug: "domain-second"})

	const domain = "reclaim.example.test"
	first, err := store.CreateCustomDomainIfUnderQuota(ctx, domain, firstApp.ID, "old-token", 100, 500)
	if err != nil {
		t.Fatal(err)
	}
	if first.VerificationExpiresAt.IsZero() {
		t.Fatal("fresh pending claim has no expiry")
	}
	if _, err := store.CreateCustomDomainIfUnderQuota(ctx, domain, secondApp.ID, "new-token", 100, 500); !errors.Is(err, ErrConflict) {
		t.Fatalf("active cross-account claim error = %v, want ErrConflict", err)
	}

	store.mu.Lock()
	expired := store.domains[domain]
	expired.VerificationExpiresAt = time.Now().Add(-time.Minute)
	store.domains[domain] = expired
	store.mu.Unlock()

	reclaimed, err := store.CreateCustomDomainIfUnderQuota(ctx, domain, secondApp.ID, "new-token", 100, 500)
	if err != nil {
		t.Fatalf("reclaim expired claim: %v", err)
	}
	if reclaimed.AppID != secondApp.ID || reclaimed.ChallengeToken != "new-token" {
		t.Fatalf("reclaimed row = %+v, want second account's app and token", reclaimed)
	}
	if matched, err := store.MarkDomainVerifiedIfChallenge(ctx, domain, "old-token"); err != nil || matched {
		t.Fatalf("old challenge matched=%v err=%v, want stale proof ignored", matched, err)
	}
	if matched, err := store.MarkDomainVerifiedIfChallenge(ctx, domain, "new-token"); err != nil || !matched {
		t.Fatalf("new challenge matched=%v err=%v, want verification", matched, err)
	}

	store.mu.Lock()
	verified := store.domains[domain]
	verified.VerificationExpiresAt = time.Now().Add(-time.Hour)
	store.domains[domain] = verified
	store.mu.Unlock()
	if _, err := store.CreateCustomDomainIfUnderQuota(ctx, domain, firstApp.ID, "third-token", 100, 500); !errors.Is(err, ErrConflict) {
		t.Fatalf("verified domain replacement error = %v, want ErrConflict", err)
	}
}

func TestCustomDomainRetryDoesNotExtendOwnership(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, _ := store.CreateAccount(ctx, "domain-retry@example.test", api.PlanScale)
	app, _ := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "domain-retry"})
	domain, err := store.CreateCustomDomainIfUnderQuota(ctx, "retry.example.test", app.ID, "token", 100, 500)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RetryCustomDomainVerification(ctx, domain.Domain); err != nil {
		t.Fatal(err)
	}
	after, err := store.DomainByName(ctx, domain.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if !after.VerificationExpiresAt.Equal(domain.VerificationExpiresAt) {
		t.Fatalf("retry extended ownership from %v to %v", domain.VerificationExpiresAt, after.VerificationExpiresAt)
	}
}

func TestCustomDomainExpiredReclaimHasSingleWinner(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, _ := store.CreateAccount(ctx, "domain-race@example.test", api.PlanScale)
	seedApp, _ := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "domain-race-seed"})
	_, _ = store.CreateCustomDomainIfUnderQuota(ctx, "race.example.test", seedApp.ID, "seed", 100, 500)
	store.mu.Lock()
	expired := store.domains["race.example.test"]
	expired.VerificationExpiresAt = time.Now().Add(-time.Minute)
	store.domains[expired.Domain] = expired
	store.mu.Unlock()

	apps := make([]App, 2)
	for i, slug := range []string{"domain-race-one", "domain-race-two"} {
		apps[i], _ = store.CreateApp(ctx, App{AccountID: account.ID, Slug: slug})
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range apps {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := store.CreateCustomDomainIfUnderQuota(ctx, "race.example.test", apps[i].ID, apps[i].Slug, 100, 500)
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected race error: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("race results wins=%d conflicts=%d, want 1/1", wins, conflicts)
	}
}
