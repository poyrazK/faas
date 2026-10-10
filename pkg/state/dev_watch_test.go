package state_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type devWatchStore interface {
	state.Store
	state.DevWatchStore
}

func TestDevWatchStoreMem(t *testing.T) {
	runDevWatchStoreContract(t, state.NewMemStore(), context.Background())
}

func TestDevWatchStorePg(t *testing.T) {
	s, ctx := pgStore(t)
	runDevWatchStoreContract(t, s, ctx)
}

// adr: 970 — the watch command is set, replaced and cleared per app.
func runDevWatchStoreContract(t *testing.T, s devWatchStore, ctx context.Context) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "dev-watch-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "dev-watch-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.DevWatchCommand(ctx, app.ID); err != nil || got != "" {
		t.Fatalf("unset watch command = %q, %v", got, err)
	}
	for _, command := range []string{"npm run dev", "next dev"} {
		if err := s.SetDevWatchCommand(ctx, app.ID, command); err != nil {
			t.Fatal(err)
		}
		if got, err := s.DevWatchCommand(ctx, app.ID); err != nil || got != command {
			t.Fatalf("watch command = %q, %v; want %q", got, err, command)
		}
	}
	if err := s.SetDevWatchCommand(ctx, app.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DevWatchCommand(ctx, app.ID); err != nil || got != "" {
		t.Fatalf("cleared watch command = %q, %v", got, err)
	}
	if err := s.SetDevWatchCommand(ctx, app.ID, ""); err != nil {
		t.Fatalf("clearing an unset command: %v", err)
	}
}
