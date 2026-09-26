package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectManifestAsyncRoutesRequireNoTriggersOptOut(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "project-async-routes@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.Default(), "gregale.dev", noopNotifier{})
	dir := t.TempDir()
	body := "async_routes: []\n"
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, problem := srv.loadAndResolveManifestPostgresBindings(context.Background(), acct, dir, []string{"reports"}, "", false); problem == nil {
		t.Fatal("project deploy accepted async_routes without --no-triggers")
	}
	if _, problem := srv.loadAndResolveManifestPostgresBindings(context.Background(), acct, dir, []string{"reports"}, "", true); problem != nil {
		t.Fatalf("project deploy with --no-triggers: %+v", problem)
	}
}

func TestResolveManagedPostgresDatabaseRecordByIDOrName(t *testing.T) {
	databases := []managedpostgres.Database{
		{ID: "db-orders", Name: "orders", State: managedpostgres.StateReady},
		{ID: "db-analytics", Name: "analytics", State: managedpostgres.StateReady},
	}
	for _, reference := range []string{"db-orders", "orders"} {
		got, err := resolveManagedPostgresDatabaseRecord(databases, reference)
		if err != nil || got.ID != "db-orders" {
			t.Fatalf("reference %q resolved to %+v, err=%v", reference, got, err)
		}
	}
}

func TestResolveManagedPostgresDatabaseRecordRejectsAmbiguousName(t *testing.T) {
	databases := []managedpostgres.Database{
		{ID: "db-a", Name: "orders", State: managedpostgres.StateReady, CreatedAt: time.Unix(1, 0)},
		{ID: "db-b", Name: "orders", State: managedpostgres.StateReady, CreatedAt: time.Unix(2, 0)},
	}
	_, err := resolveManagedPostgresDatabaseRecord(databases, "orders")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v, want ambiguous database reference", err)
	}
}
