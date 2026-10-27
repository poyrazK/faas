package state_test

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

// TestMemStoreDeploymentQueries keeps the in-memory projections exercised
// alongside their Postgres counterparts. These reads are used by rollback,
// preview and safe-deploy callers, so their filtering and cursor semantics
// are part of the Store contract rather than test-only conveniences.
func TestMemStoreDeploymentQueries(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	base := time.Now().UTC().Add(-10 * time.Minute)
	(*m.Apps())["app-a"] = state.App{ID: "app-a", AccountID: "acct-a"}
	(*m.Apps())["app-b"] = state.App{ID: "app-b", AccountID: "acct-b"}
	(*m.Apps())["deleted-app"] = state.App{ID: "deleted-app", AccountID: "acct-a", Status: state.AppDeleted}
	(*m.Deployments())["d-old"] = state.Deployment{ID: "d-old", AppID: "app-a", Scope: "prod", Status: state.DeployLive, CreatedAt: base}
	(*m.Deployments())["d-new"] = state.Deployment{ID: "d-new", AppID: "app-a", Scope: "prod", Status: state.DeployLive, CreatedAt: base.Add(2 * time.Minute)}
	(*m.Deployments())["d-default"] = state.Deployment{ID: "d-default", AppID: "app-a", Scope: "", Status: state.DeployLive, CreatedAt: base.Add(time.Minute)}
	(*m.Deployments())["d-superseded"] = state.Deployment{ID: "d-superseded", AppID: "app-a", Scope: "prod", Status: state.DeploySuperseded, CreatedAt: base.Add(3 * time.Minute)}
	(*m.Deployments())["d-other"] = state.Deployment{ID: "d-other", AppID: "app-b", Scope: "prod", Status: state.DeployLive, CreatedAt: base.Add(4 * time.Minute)}
	(*m.Deployments())["d-deleted"] = state.Deployment{ID: "d-deleted", AppID: "deleted-app", Scope: "prod", Status: state.DeployLive, CreatedAt: base.Add(5 * time.Minute)}

	live, err := m.LiveDeployments(ctx, "app-a")
	if err != nil || len(live) != 3 || live[0].ID != "d-new" || live[1].ID != "d-default" || live[2].ID != "d-old" {
		t.Fatalf("LiveDeployments = %+v, %v", live, err)
	}
	if _, err := m.LiveDeploymentForScope(ctx, "app-a", "prod"); err != nil {
		t.Fatalf("LiveDeploymentForScope(prod): %v", err)
	}
	if got, err := m.LiveDeploymentForScope(ctx, "app-a", "missing"); err == nil || got.ID != "" {
		t.Fatalf("missing scope = %+v, %v; want ErrNotFound", got, err)
	}

	page, err := m.ListDeploymentsForApp(ctx, "app-a", 2, 1)
	if err != nil || len(page) != 2 || page[0].ID != "d-new" || page[1].ID != "d-default" {
		t.Fatalf("ListDeploymentsForApp = %+v, %v", page, err)
	}
	if page, err := m.ListDeploymentsForApp(ctx, "app-a", 2, 99); err != nil || page != nil {
		t.Fatalf("past-end page = %+v, %v; want nil, nil", page, err)
	}
	before, err := m.ListDeploymentsForAppBefore(ctx, "app-a", base.Add(2*time.Minute), 10)
	if err != nil || len(before) != 2 || before[0].ID != "d-default" || before[1].ID != "d-old" {
		t.Fatalf("ListDeploymentsForAppBefore = %+v, %v", before, err)
	}

	account, err := m.ListDeploymentsForAccount(ctx, "acct-a", time.Time{}, 2)
	if err != nil || len(account) != 2 || account[0].ID != "d-superseded" || account[1].ID != "d-new" {
		t.Fatalf("ListDeploymentsForAccount = %+v, %v", account, err)
	}
}
