package state_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainManualCheckClaimCooldown(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			checks := raw.(state.CustomDomainManualCheckStore)
			app := tlsHostApp(t, ctx, raw, "")
			d, err := raw.CreateCustomDomain(ctx, "manual.example.com", app, "proof")
			requireDomainState(t, err)
			now := time.Now().UTC().Truncate(time.Microsecond)
			var winners atomic.Int32
			var group sync.WaitGroup
			for range 8 {
				group.Go(func() {
					allowed, err := checks.BeginCustomDomainManualCheck(ctx, d, now, api.DomainVerificationManualCooldown)
					if err != nil {
						t.Error(err)
					}
					if allowed {
						winners.Add(1)
					}
				})
			}
			group.Wait()
			if winners.Load() != 1 {
				t.Fatalf("concurrent probes admitted: %d", winners.Load())
			}
			allowed, err := checks.BeginCustomDomainManualCheck(ctx, d, now.Add(api.DomainVerificationManualCooldown), api.DomainVerificationManualCooldown)
			requireDomainState(t, err)
			if !allowed {
				t.Fatal("cooldown never released")
			}
			allowed, err = checks.BeginCustomDomainManualCheck(ctx, d, now.Add(8*24*time.Hour), api.DomainVerificationManualCooldown)
			requireDomainState(t, err)
			if allowed {
				t.Fatal("expired claim admitted a manual check")
			}
			requireDomainState(t, raw.DeleteCustomDomain(ctx, d.Domain))
			fresh, err := raw.CreateCustomDomain(ctx, d.Domain, app, "fresh-proof")
			requireDomainState(t, err)
			allowed, err = checks.BeginCustomDomainManualCheck(ctx, d, now.Add(time.Hour), api.DomainVerificationManualCooldown)
			requireDomainState(t, err)
			if allowed {
				t.Fatal("obsolete claim admitted a manual check")
			}
			allowed, err = checks.BeginCustomDomainManualCheck(ctx, fresh, now, api.DomainVerificationManualCooldown)
			requireDomainState(t, err)
			if !allowed {
				t.Fatal("replacement inherited the old cooldown")
			}
		})
	}
}

func TestCustomDomainCertificateRetryScheduleIsClaimBound(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			app := tlsHostApp(t, ctx, raw, "")
			verifiedTLSWildcard(t, ctx, raw, "*.retry-schedule.example.com", app)
			d, err := raw.(interface {
				DomainByName(context.Context, string) (state.CustomDomain, error)
			}).DomainByName(ctx, "*.retry-schedule.example.com")
			requireDomainState(t, err)
			now := time.Now().UTC().Truncate(time.Microsecond)
			allowed, err := raw.(state.CustomDomainCertificateAttemptStore).BeginCustomDomainCertificateAttempt(ctx, d, now, api.CustomDomainCertRetryDelay)
			requireDomainState(t, err)
			if !allowed {
				t.Fatal("certificate attempt was not admitted")
			}
			retry, err := raw.(state.CustomDomainManualCheckStore).CustomDomainCertificateRetryAt(ctx, d)
			requireDomainState(t, err)
			if !retry.Equal(now.Add(api.CustomDomainCertRetryDelay)) {
				t.Fatalf("retry schedule: %s", retry)
			}
		})
	}
}
