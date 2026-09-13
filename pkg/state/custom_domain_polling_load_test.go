package state

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestCustomDomainVerificationClaimFairWithTenThousandAbusiveRows(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	abusive, _ := m.CreateAccount(ctx, "abusive@example.test", api.PlanScale)
	good, _ := m.CreateAccount(ctx, "good@example.test", api.PlanFree)
	badApp, _ := m.CreateApp(ctx, App{AccountID: abusive.ID, Slug: "abusive"})
	goodApp, _ := m.CreateApp(ctx, App{AccountID: good.ID, Slug: "good"})
	now := time.Now()
	m.mu.Lock()
	for i := 0; i < 10000; i++ {
		d := fmt.Sprintf("bad-%05d.example.test", i)
		m.domains[d] = CustomDomain{Domain: d, AppID: badApp.ID, ChallengeToken: "x", VerificationNextCheckAt: now, VerificationExpiresAt: now.Add(time.Hour)}
	}
	m.domains["good.example.test"] = CustomDomain{Domain: "good.example.test", AppID: goodApp.ID, ChallengeToken: "ok", VerificationNextCheckAt: now, VerificationExpiresAt: now.Add(time.Hour)}
	m.mu.Unlock()
	started := time.Now()
	claimed, err := m.ClaimCustomDomainsForVerification(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("10k claim exceeded 1s: %v", time.Since(started))
	}
	found := false
	bad := 0
	for _, d := range claimed {
		if d.Domain == "good.example.test" {
			found = true
		}
		if d.AppID == badApp.ID {
			bad++
		}
	}
	if !found {
		t.Fatal("healthy tenant starved by abusive backlog")
	}
	if bad > 1 {
		t.Fatalf("abusive account claimed %d rows, want <=1", bad)
	}
}
