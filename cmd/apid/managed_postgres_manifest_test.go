package main

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

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
