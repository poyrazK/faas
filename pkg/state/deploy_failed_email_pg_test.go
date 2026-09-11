package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreClaimDeployFailedEmailUsesTimestampCooldown(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "deploy-failed-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "deploy-failed-" + uuid.NewString(),
		Type:      state.AppTypeFunction,
		RAMMB:     256,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	first := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	claimed, err := store.ClaimDeployFailedEmail(ctx, app.ID, first)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v; want true, nil", claimed, err)
	}
	claimed, err = store.ClaimDeployFailedEmail(ctx, app.ID, first.Add(30*time.Minute))
	if err != nil || claimed {
		t.Fatalf("claim inside cooldown = %v, %v; want false, nil", claimed, err)
	}
	claimed, err = store.ClaimDeployFailedEmail(ctx, app.ID, first.Add(61*time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim after cooldown = %v, %v; want true, nil", claimed, err)
	}
}
