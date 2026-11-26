package previewdb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/previewdb"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// A pull request's branch shares the PR environment's empty database with
// its app preview; releasing the branch leaves the database while the app
// preview binds it, and purging takes it once only leftovers bind it
// (ADR-20261113121342 amendment 2026-11-26).
func TestBranchSharesThePullRequestsEmptyDatabase(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orders := f.database(t, "orders", managedpostgres.PreviewsEmpty,
		managedpostgres.CreateBindingRequest{Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite})
	owner := managedpostgres.PreviewOwner{SourceDatabaseID: orders.ID, EnvironmentID: "project-1", PRNumber: 7}

	if _, err := f.manager.EnsureBranch(ctx, api.Plan("no-such-plan"), account, owner); !errors.Is(err, managedpostgres.ErrPreviewsOff) {
		t.Fatalf("EnsureBranch on a plan without preview databases = %v; want ErrPreviewsOff", err)
	}
	var database managedpostgres.Database
	var err error
	for range 3 {
		if database, err = f.manager.EnsureBranch(ctx, api.PlanPro, account, owner); !errors.Is(err, previewdb.ErrPending) {
			break
		}
	}
	if err != nil || database.Preview != owner {
		t.Fatalf("EnsureBranch = %+v, %v", database, err)
	}
	bind := managedpostgres.CreateBindingRequest{AccountID: account, DatabaseID: database.ID, AppID: f.parent.ID,
		Scope: "pr-7", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite}
	for range 3 {
		if err = f.manager.BindBranch(ctx, bind); !errors.Is(err, previewdb.ErrPending) {
			break
		}
	}
	if err != nil {
		t.Fatalf("BindBranch = %v", err)
	}
	// An app preview of the same pull request, after a fallback, binds the
	// same database.
	appPreview := preview("app-pr-7", 7, "project-1")
	if err := f.manager.Prepare(ctx, api.PlanPro, f.parent, appPreview); err != nil {
		t.Fatal(err)
	}
	if shared, _ := f.service.PreviewDatabases(ctx, account, "project-1", 7); len(shared) != 1 || shared[0].ID != database.ID {
		t.Fatalf("PR 7's databases = %+v; want the branch's one", shared)
	}

	if err := f.manager.ReleaseBranch(ctx, account, "project-1", 7, "pr-7"); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.PurgeUnbound(ctx, account, "project-1", 7); err != nil {
		t.Fatal(err)
	}
	kept, _ := f.service.PreviewDatabases(ctx, account, "project-1", 7)
	if len(kept) != 1 || len(f.previewBindings(t, kept[0], appPreview.ID)) != 1 {
		t.Fatalf("after the branch's release PR 7's databases = %+v; want the app preview's kept", kept)
	}
	all, err := f.bindings.List(ctx, account, database.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range all {
		if binding.Scope == "pr-7" && (binding.State == managedpostgres.BindingStateReady || binding.State == managedpostgres.BindingStateProvisioning) {
			t.Fatalf("the branch still binds the database: %+v", binding)
		}
		// The app preview's teardown stopped before the purge: only a
		// leftover binding is left.
		if binding.AppID == appPreview.ID {
			if _, err := f.bindings.Delete(ctx, account, binding.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.manager.PurgeUnbound(ctx, account, "project-1", 7); err != nil {
		t.Fatal(err)
	}
	if left, _ := f.service.PreviewDatabases(ctx, account, "project-1", 7); len(left) != 0 {
		t.Fatalf("PR 7's databases after the purge = %+v; want none", left)
	}
}
