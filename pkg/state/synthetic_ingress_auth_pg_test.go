//go:build !no_pg

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

func TestPgSyntheticIngressAuthMode(t *testing.T) {
	store, _, pool := pgWithPool(t)
	exerciseSyntheticIngressAuthMode(t, store, store.ReadSyntheticIngressAuthMode)
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "synth-pg-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"UPDATE apps SET status='deleted' WHERE id=$1", "UPDATE apps SET status='active', deleted_at=now() WHERE id=$1"} {
		if _, err := pool.Exec(t.Context(), query, app.ID); err != nil {
			t.Fatal(err)
		}
		if mode, err := store.ReadSyntheticIngressAuthMode(t.Context(), app.ID); mode != "" || !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("deleted projection mode=%q err=%v", mode, err)
		}
	}
}

func TestPgSyntheticIngressAuthModeTimeoutReleasesConnection(t *testing.T) {
	store, _, pool := pgWithPool(t)
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "synth-lock-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(t.Context()) }()
	if _, err := lock.Exec(t.Context(), "LOCK TABLE apps IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(t.Context(), api.TrafficServicePolicyReadTimeout)
	defer cancel()
	started := time.Now()
	mode, err := store.ReadSyntheticIngressAuthMode(readCtx, app.ID)
	if mode != "" || err == nil || time.Since(started) > time.Second {
		t.Fatalf("blocked read mode=%q err=%v time=%s", mode, err, time.Since(started))
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if mode, err := store.ReadSyntheticIngressAuthMode(t.Context(), app.ID); mode != state.AppPublicAuthModeOpen || err != nil {
		t.Fatalf("recovered read mode=%q err=%v", mode, err)
	}
	until := time.Now().Add(time.Second)
	for pool.Stat().AcquiredConns() != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("lookup retained %d pool connections", acquired)
	}
}
