// adr: 531
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemSyntheticIngressAuthMode(t *testing.T) {
	store := state.NewMemStore()
	exerciseSyntheticIngressAuthMode(t, store, store.ReadSyntheticIngressAuthMode)
	deletedAt := time.Now()
	for _, tc := range []struct {
		name   string
		status state.AppStatus
		at     *time.Time
	}{
		{name: "deleted status", status: state.AppDeleted},
		{name: "deletion timestamp", status: state.AppActive, at: &deletedAt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := store.CreateApp(t.Context(), state.App{Slug: uuid.NewString(), Status: tc.status, DeletedAt: tc.at})
			if err != nil {
				t.Fatal(err)
			}
			if mode, err := store.ReadSyntheticIngressAuthMode(t.Context(), app.ID); mode != "" || !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("deleted app mode=%q err=%v", mode, err)
			}
		})
	}
}

func exerciseSyntheticIngressAuthMode(t *testing.T, store state.Store, read func(context.Context, string) (string, error)) {
	t.Helper()
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "synth-auth-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	if mode, err := read(t.Context(), app.ID); err != nil || mode != state.AppPublicAuthModeOpen {
		t.Fatalf("initial mode=%q err=%v", mode, err)
	}
	for _, wanted := range []string{state.AppPublicAuthModeInternalOnly, state.AppPublicAuthModeBearer, state.AppPublicAuthModeOpen} {
		if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetPublicAuth: true, PublicAuth: &state.AppPublicAuthUpdate{Mode: wanted}}); err != nil {
			t.Fatal(err)
		}
		if mode, err := read(t.Context(), app.ID); err != nil || mode != wanted {
			t.Fatalf("current mode=%q want=%q err=%v", mode, wanted, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if mode, err := read(cancelled, app.ID); mode != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled mode=%q err=%v", mode, err)
	}
	if mode, err := read(t.Context(), uuid.NewString()); mode != "" || !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing app mode=%q err=%v", mode, err)
	}
	if err := store.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if mode, err := read(t.Context(), app.ID); mode != "" || !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted app mode=%q err=%v", mode, err)
	}
}
