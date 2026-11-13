// adr: 20261106103654 — `gregale postgres branch` maps --from and --ttl onto
// the restore request and only deletes copies of the named database.
package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresBranchPointLeavesLiveToTheServer(t *testing.T) {
	for _, from := range []string{"", "live", "LIVE", "latest", "now", "  live "} {
		if got, err := postgresBranchPoint(from); err != nil || got != "" {
			t.Errorf("postgresBranchPoint(%q) = %q, %v; want the latest point", from, got, err)
		}
	}
	if got, err := postgresBranchPoint("2026-11-06T09:00:00Z"); err != nil || got != "2026-11-06T09:00:00Z" {
		t.Fatalf("an RFC3339 point = %q, %v", got, err)
	}
	if _, err := postgresBranchPoint("yesterday"); err == nil {
		t.Fatal("an unparseable --from was accepted")
	}
}

func TestPostgresBranchTTLBounds(t *testing.T) {
	for raw, want := range map[string]int64{"": 0, "5m": 300, "2h": 7200, "90m": 5400, "7d": 7 * 86400, "30d": api.ManagedPostgresBranchTTLMaxSeconds} {
		if got, err := postgresBranchTTL(raw); err != nil || got != want {
			t.Errorf("postgresBranchTTL(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"4m", "31d", "0d", "-1h", "1.5s", "soon", "2h30.5s"} {
		if got, err := postgresBranchTTL(raw); err == nil {
			t.Errorf("postgresBranchTTL(%q) = %d, want an error", raw, got)
		}
	}
}

func TestPostgresBranchesOfKeepsOnlyLiveCopiesOfTheSource(t *testing.T) {
	databases := []api.ManagedPostgresDatabase{
		{ID: "source", Name: "orders", State: "ready"},
		{ID: "b1", Name: "pr-1", State: "ready", RestoreSourceDatabaseID: "source", RestorePointInTime: "2026-11-06T09:00:00Z", ExpiresAt: "2026-11-06T11:00:00Z"},
		{ID: "b2", Name: "pr-2", State: "deleted", RestoreSourceDatabaseID: "source"},
		{ID: "x", Name: "other-copy", State: "ready", RestoreSourceDatabaseID: "other"},
	}
	branches := postgresBranchesOf(databases, "source")
	if len(branches) != 1 || branches[0].ID != "b1" {
		t.Fatalf("branches = %+v, want only pr-1", branches)
	}
	var out bytes.Buffer
	renderPostgresBranches(&out, "orders", branches)
	if !strings.Contains(out.String(), "pr-1") || !strings.Contains(out.String(), "2026-11-06T11:00:00Z") {
		t.Fatalf("branch table = %q", out.String())
	}
	out.Reset()
	renderPostgresBranches(&out, "orders", nil)
	if out.String() != "No branches of orders.\n" {
		t.Fatalf("empty table = %q", out.String())
	}
}

func TestPostgresBranchDeleteRefusesADatabaseThatIsNotACopyOfTheSource(t *testing.T) {
	if err := postgresBranchOf(api.ManagedPostgresDatabase{Name: "pr-1", RestoreSourceDatabaseID: "source"}, "source", "orders"); err != nil {
		t.Fatalf("a branch of the source was refused: %v", err)
	}
	for _, database := range []api.ManagedPostgresDatabase{
		{Name: "invoices"},
		{Name: "pr-9", RestoreSourceDatabaseID: "other"},
	} {
		err := postgresBranchOf(database, "source", "orders")
		if err == nil || !strings.Contains(err.Error(), "is not a branch of orders") {
			t.Errorf("postgresBranchOf(%s) = %v, want a refusal", database.Name, err)
		}
	}
}
