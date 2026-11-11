//go:build !no_pg

package state_test

// adr: 20261104204500

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_EgressOverageElection pins the election's storage
// (ADR-20261104204500): turning it on keeps an earlier start, turning it off
// stamps the end once, and the bulk read returns only subjects that elected.
func TestPg_EgressOverageElection(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := state.NewPgStore(pool)
	acct, err := s.CreateAccount(ctx, "egress-overage-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateAccount(ctx, "egress-overage-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.EgressOverageElection(ctx, acct.ID); err != nil || got.EnabledAt != nil || got.On() {
		t.Fatalf("initial = %+v, %v", got, err)
	}
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	on, err := s.SetEgressOverage(ctx, acct.ID, true, t0)
	if err != nil || !on.On() || !on.EnabledAt.Equal(t0) {
		t.Fatalf("on = %+v, %v", on, err)
	}
	if again, err := s.SetEgressOverage(ctx, acct.ID, true, t0.Add(time.Hour)); err != nil || !again.EnabledAt.Equal(t0) {
		t.Fatalf("on twice = %+v, %v, want the first start", again, err)
	}
	off, err := s.SetEgressOverage(ctx, acct.ID, false, t0.Add(2*time.Hour))
	if err != nil || off.On() || !off.DisabledAt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("off = %+v, %v", off, err)
	}
	if offAgain, err := s.SetEgressOverage(ctx, acct.ID, false, t0.Add(3*time.Hour)); err != nil || !offAgain.DisabledAt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("off twice = %+v, %v, want the first end", offAgain, err)
	}
	if !off.Covers(t0.Add(time.Hour)) || off.Covers(t0.Add(2*time.Hour)) || off.Covers(t0.Add(-time.Hour)) {
		t.Fatalf("covers wrong for %+v", off)
	}
	back, err := s.SetEgressOverage(ctx, acct.ID, true, t0.Add(4*time.Hour))
	if err != nil || !back.On() || !back.EnabledAt.Equal(t0) {
		t.Fatalf("back on = %+v, %v, want the first start kept", back, err)
	}
	if never, err := s.SetEgressOverage(ctx, other.ID, false, t0); err != nil || never.EnabledAt != nil || never.DisabledAt != nil {
		t.Fatalf("off without an election = %+v, %v", never, err)
	}
	all, err := s.EgressOverageElections(ctx, []string{acct.ID, other.ID, "not-a-uuid"})
	if err != nil || len(all) != 1 || !all[acct.ID].On() {
		t.Fatalf("bulk = %+v, %v", all, err)
	}
	if _, err := s.SetEgressOverage(ctx, uuid.NewString(), true, t0); err == nil {
		t.Fatal("unknown subject: want ErrNotFound")
	}
}
