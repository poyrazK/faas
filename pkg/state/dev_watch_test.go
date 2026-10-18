package state_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type devWatchStore = contractTestStore

func TestDevWatchStoreMem(t *testing.T) {
	runDevWatchStoreContract(t, state.NewMemStore(), context.Background())
}

func TestDevWatchStorePg(t *testing.T) {
	s, ctx := pgStore(t)
	runDevWatchStoreContract(t, s, ctx)
}

// adr: 1170 — the watch command and hostname are set, replaced and cleared per app.
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
	if got, err := s.DevWatch(ctx, app.ID); err != nil || got != (state.DevWatchSetting{}) {
		t.Fatalf("unset watch mode = %+v, %v", got, err)
	}
	for _, setting := range []state.DevWatchSetting{
		{Command: "npm run dev"},
		{Command: "next dev", Host: app.Slug + ".gregale.dev"},
	} {
		if err := s.SetDevWatch(ctx, app.ID, setting); err != nil {
			t.Fatal(err)
		}
		if got, err := s.DevWatch(ctx, app.ID); err != nil || got != setting {
			t.Fatalf("watch mode = %+v, %v; want %+v", got, err, setting)
		}
	}
	if err := s.SetDevWatch(ctx, app.ID, state.DevWatchSetting{Host: "ignored.gregale.dev"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DevWatch(ctx, app.ID); err != nil || got != (state.DevWatchSetting{}) {
		t.Fatalf("cleared watch mode = %+v, %v", got, err)
	}
	if err := s.SetDevWatch(ctx, app.ID, state.DevWatchSetting{}); err != nil {
		t.Fatalf("clearing unset watch mode: %v", err)
	}
}
