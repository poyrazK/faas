//go:build !no_pg

package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAppRequestRatePolicyRoundTrip(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "request-rate-policy-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "request-rate-policy-" + suffix,
		Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	rps, burst := 25, 120
	updated, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		RequestRateLimitRPS: &rps, SetRequestRateLimitRPS: true,
		RequestRateLimitBurst: &burst, SetRequestRateLimitBurst: true,
	})
	if err != nil {
		t.Fatalf("set request rate policy: %v", err)
	}
	assertPolicy := func(got state.App, wantRPS, wantBurst *int) {
		t.Helper()
		if !sameOptionalInt(got.RequestRateLimitRPS, wantRPS) || !sameOptionalInt(got.RequestRateLimitBurst, wantBurst) {
			t.Fatalf("request rate policy = (%v, %v), want (%v, %v)", got.RequestRateLimitRPS, got.RequestRateLimitBurst, wantRPS, wantBurst)
		}
	}
	assertPolicy(updated, &rps, &burst)

	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{MaxConcurrency: requestRatePolicyPGIntPtr(3)}); err != nil {
		t.Fatalf("update unrelated field: %v", err)
	}
	updated, err = store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("read after unrelated update: %v", err)
	}
	assertPolicy(updated, &rps, &burst)

	zero := 0
	updated, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		RequestRateLimitRPS: &zero, SetRequestRateLimitRPS: true,
		RequestRateLimitBurst: &zero, SetRequestRateLimitBurst: true,
	})
	if err != nil {
		t.Fatalf("reset request rate policy: %v", err)
	}
	assertPolicy(updated, nil, nil)
	negative := -1
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		RequestRateLimitRPS: &negative, SetRequestRateLimitRPS: true,
	}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("negative store override error = %v, want ErrInvalidArgument", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET request_rate_limit_rps = -1 WHERE id = $1`, app.ID); err == nil {
		t.Fatal("database accepted a negative request rate override")
	}
}

func requestRatePolicyPGIntPtr(value int) *int { return &value }

func sameOptionalInt(got, want *int) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}
