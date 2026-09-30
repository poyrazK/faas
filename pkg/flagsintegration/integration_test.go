package flagsintegration_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
	"sync"
	"testing"
)

func exerciseFeatureFlags(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	fs := s.(state.FeatureFlagStore)
	acct, err := s.CreateAccount(ctx, "flags@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "flags", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	scope := state.FeatureFlagScope{AccountID: acct.ID, ProjectID: project.ID, EnvironmentID: env.ID}
	tenants := s.(state.PlatformTenantStore)
	customer, _, err := tenants.CreatePlatformTenant(ctx, acct.ID, "42", "Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	config := flags.Config{Flags: []flags.Flag{{Key: "export", Enabled: true, Rules: []flags.Rule{{ID: "customers", Customers: []string{customer.ID}, Value: true}}}}}
	v, err := fs.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, Config: config, Actor: acct.ID})
	if err != nil {
		t.Fatal(err)
	}
	decision := flags.Evaluate(v.Bundle, "export", customer.ID, false)
	value, isBoolean := decision.Value.(bool)
	if v.Version != 1 || !isBoolean || !value || v.Flags[0].Seed == "" {
		t.Fatal(v)
	}
	firstSeed := v.Flags[0].Seed
	v.Flags[0].Enabled = false
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, conflicts := 0, 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := fs.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: 1, Config: v.Config, Actor: acct.ID})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else if errors.Is(err, state.ErrConflict) {
				conflicts++
			} else {
				t.Errorf("write: %v", err)
			}
		}()
	}
	wg.Wait()
	if success != 1 || conflicts != 7 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
	old, err := fs.GetFeatureFlags(ctx, scope, 1)
	if err != nil || !old.Flags[0].Enabled {
		t.Fatalf("history mutated: %+v %v", old, err)
	}
	restored, err := fs.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: 2, RestoreVersion: 1, Actor: acct.ID})
	if err != nil || restored.Version != 3 || restored.RestoredFrom != 1 || restored.Flags[0].Seed != firstSeed {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	foreign, err := s.CreateAccount(ctx, "foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	other := scope
	other.AccountID = foreign.ID
	if _, err := fs.GetFeatureFlags(ctx, other, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account read: %v", err)
	}
	outsider, _, err := tenants.CreatePlatformTenant(ctx, foreign.ID, "x", "Foreign", 100)
	if err != nil {
		t.Fatal(err)
	}
	restored.Flags[0].Rules[0].Customers = []string{outsider.ID}
	if _, err := fs.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: 3, Config: restored.Config, Actor: acct.ID}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign targeting: %v", err)
	}
	rows, err := fs.ListFeatureFlagVersions(ctx, scope, 3)
	if err != nil || len(rows) != 2 || rows[0].Version != 2 {
		t.Fatalf("history page: %+v %v", rows, err)
	}
	if _, err = fs.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: 3, Config: flags.Config{}, Actor: acct.ID}); err != nil {
		t.Fatal(err)
	}
	// Recreating a retired key keeps allocation, and empty rules/groups have
	// canonical array shapes that the runtime SDK can consume.
	emptyRules := flags.Config{Groups: map[string][]string{"empty": nil}, Flags: []flags.Flag{{Key: "export", Enabled: true}}}
	recreated, err := fs.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: 4, Config: emptyRules, Actor: acct.ID})
	if err != nil || recreated.Flags[0].Seed != firstSeed || recreated.Flags[0].Rules == nil || recreated.Groups["empty"] == nil {
		t.Fatalf("recreation: %+v %v", recreated, err)
	}
	if emptyRules.Flags[0].Seed != "" || emptyRules.Groups["empty"] != nil {
		t.Fatal("publication mutated caller configuration")
	}
}
func TestFeatureFlagsMemStore(t *testing.T) { exerciseFeatureFlags(t, state.NewMemStore()) }
func TestFeatureFlagsPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	exerciseFeatureFlags(t, s)
}
