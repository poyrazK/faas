package reconcile

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReconcilePlatformTenantPolicyLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	svc := NewService(store, newFakeAuditor(store), nil)
	for _, tc := range []struct {
		name    string
		policy  *bool
		command []string
		want    bool
	}{
		{name: "create protected", policy: reconcileTenantBool(true), want: true},
		{name: "omit on no-op", want: true},
		{name: "omit on update", command: []string{"serve"}, want: true},
		{name: "explicit disable", policy: reconcileTenantBool(false), command: []string{"serve"}},
		{name: "explicit enable", policy: reconcileTenantBool(true), command: []string{"serve"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
				{Name: "customer-api", Class: reposcan.ClassHTTP, Tier: reposcan.TierCompose, Source: "compose.yaml: customer-api",
					Command: tc.command, PlatformTenantRequired: tc.policy},
			}}
			if _, err := svc.Reconcile(ctx, project, scan, "sha", "main", nil); err != nil {
				t.Fatal(err)
			}
			apps, err := store.AppsForProject(ctx, project.AccountID, project.ID)
			if err != nil || len(apps) != 1 {
				t.Fatalf("apps = %+v, err = %v", apps, err)
			}
			if apps[0].PlatformTenantRequired != tc.want {
				t.Fatalf("policy = %v, want %v", apps[0].PlatformTenantRequired, tc.want)
			}
		})
	}
}

func TestReconcilePlatformTenantPolicyFreePlanRejected(t *testing.T) {
	store := newFakeStore()
	store.accountPlan = api.PlanFree
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	svc := NewService(store, newFakeAuditor(store), nil)
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Name: "customer-api", Tier: reposcan.TierCompose, Source: "compose.yaml: customer-api", PlatformTenantRequired: reconcileTenantBool(true)},
	}}
	for _, apply := range []bool{false, true} {
		var err error
		if apply {
			_, err = svc.Reconcile(context.Background(), project, scan, "sha", "main", nil)
		} else {
			_, err = svc.Plan(context.Background(), project, scan, "sha", "main", nil)
		}
		var problem *api.APIError
		if !errors.As(err, &problem) || problem.Problem.Status != 402 {
			t.Fatalf("Free policy rejection (apply=%v): %v", apply, err)
		}
	}
	apps, err := store.AppsForProject(context.Background(), project.AccountID, project.ID)
	if err != nil || len(apps) != 0 {
		t.Fatalf("rejected policy wrote apps: %+v, err=%v", apps, err)
	}
}

func reconcileTenantBool(v bool) *bool { return &v }
