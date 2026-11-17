// adr: 20261106103654 (amendment 1) — a branch with a TTL of at most 7 days
// takes a place in the plan's branch allowance instead of its database limit
// or the restore slot, and plan excess never ranks it.
package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestShortLivedBranchesSitOutsideTheDatabaseLimit(t *testing.T) {
	f := newBranchFixture(t)
	ctx := context.Background()
	*f.limit, *f.branchAllowance = 1, 2
	source := f.source(t)
	restore := func(name string, ttl time.Duration) (Database, error) {
		return f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: name, Latest: true, TTL: ttl})
	}
	var short []Database
	for _, name := range []string{"pr-1", "pr-2"} {
		branch, err := restore(name, api.ManagedPostgresAllowanceBranchTTLMax)
		if err != nil || !branch.InBranchAllowance() || !branch.RestoreSlotUntil.IsZero() {
			t.Fatalf("Restore %s = %+v, %v; want a branch in the allowance", name, branch, err)
		}
		short = append(short, branch)
	}
	_, err := restore("pr-3", time.Hour)
	if !errors.Is(err, ErrBranchLimit) || !errors.Is(err, ErrQuotaExceeded) || !strings.Contains(err.Error(), "2 of 2") {
		t.Fatalf("third branch = %v, want the branch limit naming 2 of 2", err)
	}
	if status, code, _ := QuotaProblem(err); status != 403 || code != "managed_postgres_branch_limit" {
		t.Fatalf("QuotaProblem = %d %s", status, code)
	}
	// The branches leave the database limit to databases: a second database
	// fits once the plan allows two.
	*f.limit = 2
	if second, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "users", Spec: testSpec()}); err != nil || second.State != StateReady {
		t.Fatalf("Create beside two branches = %+v, %v", second, err)
	}
	long, err := restore("pr-long", api.ManagedPostgresAllowanceBranchTTLMax+time.Hour)
	if err != nil || long.InBranchAllowance() || long.RestoreSlotUntil.IsZero() {
		t.Fatalf("Restore past the allowance TTL = %+v, %v; want the restore slot", long, err)
	}
	all, err := f.store.List(ctx, "account-a")
	if err != nil {
		t.Fatal(err)
	}
	excess := PlanExcess(all, api.ManagedPostgresPlanLimits{DatabasesMax: 1})
	for _, branch := range short {
		if reason, ok := excess[branch.ID]; ok {
			t.Fatalf("plan excess ranked branch %s (%s)", branch.Name, reason)
		}
	}
}

func TestBranchAllowanceFailsClosed(t *testing.T) {
	for name, allowance := range map[string]int{"no allowance": 0, "negative": -1} {
		t.Run(name, func(t *testing.T) {
			f := newBranchFixture(t)
			*f.branchAllowance = allowance
			source := f.source(t)
			_, err := f.service.Restore(context.Background(), RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "pr-1", Latest: true, TTL: time.Hour})
			if !errors.Is(err, ErrBranchLimit) {
				t.Fatalf("Restore = %v, want the branch limit", err)
			}
		})
	}
	f := newBranchFixture(t)
	f.service.maxBranchesPerAccount = nil
	source := f.source(t)
	if _, err := f.service.Restore(context.Background(), RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "pr-1", Latest: true, TTL: time.Hour}); !errors.Is(err, ErrBranchLimit) {
		t.Fatalf("Restore without the hook = %v, want the branch limit", err)
	}
	within := RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "switch", Latest: true, TTL: time.Hour, WithinLimit: true}
	if _, err := f.service.Restore(context.Background(), within); !errors.Is(err, ErrInvalid) {
		t.Fatalf("within-limit restore with a TTL = %v, want invalid", err)
	}
}

func TestBranchesMaxByPlan(t *testing.T) {
	for plan, want := range map[api.Plan]int{api.PlanFree: 0, api.PlanHobby: 2, api.PlanPro: 5, api.PlanScale: 20, api.Plan("unknown"): 0} {
		if got := api.ManagedPostgresBranchesMax(plan); got != want {
			t.Errorf("ManagedPostgresBranchesMax(%s) = %d, want %d", plan, got, want)
		}
	}
}
