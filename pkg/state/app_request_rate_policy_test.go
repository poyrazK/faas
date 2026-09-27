package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreUpdateAppRequestRatePolicy(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "request-rate-policy@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "request-rate-policy"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	rps, burst := 25, 120
	updated, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{
		RequestRateLimitRPS: &rps, SetRequestRateLimitRPS: true,
		RequestRateLimitBurst: &burst, SetRequestRateLimitBurst: true,
	})
	if err != nil {
		t.Fatalf("set request rate policy: %v", err)
	}
	if updated.RequestRateLimitRPS == nil || *updated.RequestRateLimitRPS != rps ||
		updated.RequestRateLimitBurst == nil || *updated.RequestRateLimitBurst != burst {
		t.Fatalf("set policy = (%v, %v), want (%d, %d)", updated.RequestRateLimitRPS, updated.RequestRateLimitBurst, rps, burst)
	}

	updated, err = store.UpdateApp(ctx, app.ID, UpdateAppParams{MaxConcurrency: requestRatePolicyIntPtr(3)})
	if err != nil {
		t.Fatalf("update unrelated field: %v", err)
	}
	if updated.RequestRateLimitRPS == nil || *updated.RequestRateLimitRPS != rps ||
		updated.RequestRateLimitBurst == nil || *updated.RequestRateLimitBurst != burst {
		t.Fatalf("unrelated update changed request rate policy: (%v, %v)", updated.RequestRateLimitRPS, updated.RequestRateLimitBurst)
	}

	zeroRPS, zeroBurst := 0, 0
	updated, err = store.UpdateApp(ctx, app.ID, UpdateAppParams{
		RequestRateLimitRPS: &zeroRPS, SetRequestRateLimitRPS: true,
		RequestRateLimitBurst: &zeroBurst, SetRequestRateLimitBurst: true,
	})
	if err != nil {
		t.Fatalf("reset request rate policy: %v", err)
	}
	if updated.RequestRateLimitRPS != nil || updated.RequestRateLimitBurst != nil {
		t.Fatalf("reset policy = (%v, %v), want plan-default nil values", updated.RequestRateLimitRPS, updated.RequestRateLimitBurst)
	}

	negative := -1
	if _, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{
		RequestRateLimitRPS: &negative, SetRequestRateLimitRPS: true,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative override error = %v, want ErrInvalidArgument", err)
	}
}

func requestRatePolicyIntPtr(value int) *int { return &value }
