package grace_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Bug hunt 35: erasing a member's account deleted the rootfs layers and
// snapshots of the company apps the member had created, though the apps
// stay with their organization. The organization was left with apps that
// could neither wake nor cold-boot (invariant 3).
func TestRunOnceKeepsCompanyAppArtifacts(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	member := seedAccount(t, store)
	owner, err := store.CreateAccount(ctx, "company-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "artifact-company", Name: "Artifact Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_artifact_company"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_artifact_company"); err != nil {
		t.Fatal(err)
	}

	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seed := func(app state.App, key string) state.App {
		t.Helper()
		created, err := store.CreateApp(ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeployment(ctx, state.Deployment{ID: app.Slug + "-deployment", AppID: created.ID, Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/unused/rootfs.ext4", key, 4); err != nil {
			t.Fatal(err)
		}
		if err := backend.Put(ctx, key, strings.NewReader("data")); err != nil {
			t.Fatal(err)
		}
		return created
	}
	const personalKey, companyKey = "apps/personal/rootfs.ext4", "apps/company/rootfs.ext4"
	seed(state.App{AccountID: member.ID, Slug: "member-personal", Status: state.AppActive}, personalKey)
	company := seed(state.App{AccountID: member.ID, OrgID: org.ID, Slug: "member-company", Status: state.AppActive}, companyKey)

	if err := store.MarkAccountDeletionPending(ctx, member.ID); err != nil {
		t.Fatalf("mark the member's account for deletion: %v", err)
	}
	p := params(store, &recordingSender{}, makeNotifier(&recordingNotifier{}), nowFrozen(time.Now().Add(31*24*time.Hour)), runOnceInterval)
	p.Artifacts = backend
	if err := grace.New(p).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := backend.Get(ctx, personalKey); !storage.IsNotFound(err) {
		t.Fatalf("personal app artifact Get error = %v, want deleted", err)
	}
	if _, err := backend.Get(ctx, companyKey); err != nil {
		t.Fatalf("company app artifact was deleted with its creator's account: %v", err)
	}
	if kept, err := store.AppByID(ctx, company.ID); err != nil || kept.Status == state.AppDeleted {
		t.Fatalf("company app after its creator's erasure = %+v (%v), want it kept", kept, err)
	}
}
