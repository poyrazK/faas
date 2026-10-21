package state_test

// adr: 202610171905

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemOrgProjectOwnership(t *testing.T) { orgProjectOwnership(t, state.NewMemStore()) }

func orgProjectOwnership(t *testing.T, store state.Store) {
	t.Helper()
	f := setupOffboarding(t, store)
	ctx := t.Context()
	if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_project_company"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_project_company"); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: f.actor.ID, OrgID: f.org.ID, Slug: "company-project"})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: project.AccountID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	values, hash, err := api.NormalizeProjectEnvironmentConfig(json.RawMessage(`{"FEATURE":"enabled"}`))
	if err != nil {
		t.Fatal(err)
	}
	config, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{AccountID: project.AccountID, ProjectID: project.ID, EnvironmentSlug: stage.Slug, ConfigHash: hash, Values: values})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: project.AccountID, ProjectID: project.ID, OrgID: project.OrgID, Slug: "company-project-api", WorkloadName: "api", Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	sealed := []byte("opaque sealed project environment binding")
	if err := store.UpsertAppSecretInScope(ctx, app.AccountID, app.ID, stage.Slug, "TOKEN", sealed); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: stage.Slug, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "/project/rootfs", "project/rootfs", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	release, err := store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, project.AccountID, project.ID, stage.Slug, 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	promotion, _, err := store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{AccountID: project.AccountID, ProjectID: project.ID, ProjectSlug: project.Slug, FromEnvironment: stage.Slug, ToEnvironment: "production", PromotionHash: strings.Repeat("b", 64), IdempotencyKey: "historical-promotion", Status: "succeeded"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	personal, err := store.CreateProject(ctx, state.Project{AccountID: f.actor.ID, Slug: "personal-project"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(ctx, state.Project{AccountID: f.owner.ID, OrgID: f.org.ID, Slug: project.Slug}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate org slug accepted: %v", err)
	}
	snapshot, err := store.OrgAccessSnapshot(ctx, f.org.ID, f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale, stop := state.WithOrgAccessFence(ctx, state.OrgAccessFence{OrgID: f.org.ID, AccountID: f.actor.ID, Revision: snapshot.Revision})
	defer stop(nil)
	if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(stale, state.ProjectEnvironment{AccountID: project.AccountID, ProjectID: project.ID, Slug: "forbidden"}); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("offboarded writer mutated project: %v", err)
	}
	if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProjectByID(ctx, personal.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("personal project survived erasure: %v", err)
	}
	saved, err := store.ProjectByOrgSlug(ctx, f.org.ID, project.Slug)
	if err != nil || saved.ID != project.ID || saved.AccountID != project.AccountID {
		t.Fatalf("company project changed: %+v %v", saved, err)
	}
	env, err := store.ProjectEnvironmentBySlug(ctx, project.AccountID, project.ID, stage.Slug)
	if err != nil || env.ID != stage.ID {
		t.Fatalf("environment changed: %+v %v", env, err)
	}
	got, err := store.ProjectEnvironmentConfigLatest(ctx, project.AccountID, project.ID, stage.Slug)
	if err != nil || got.ID != config.ID || got.ConfigHash != config.ConfigHash || !bytes.Equal(got.Values, config.Values) {
		t.Fatalf("configuration changed: %+v %v", got, err)
	}
	secret, err := store.GetAppSecretInScope(ctx, app.AccountID, app.ID, stage.Slug, "TOKEN")
	if err != nil || secret == nil || !bytes.Equal(secret.Ciphertext, sealed) {
		t.Fatalf("sealed binding changed: %+v %v", secret, err)
	}
	if _, _, err := store.ProjectEnvironmentPromotionByID(ctx, project.AccountID, project.Slug, "production", promotion.ID); err != nil {
		t.Fatalf("promotion history erased: %v", err)
	}
	retained, err := store.(state.ProjectReleaseSetReader).ProjectReleaseSetByID(ctx, project.AccountID, project.ID, stage.Slug, release.ID)
	if err != nil || retained.ID != release.ID {
		t.Fatalf("release history erased: %+v %v", retained, err)
	}
	owner, err := state.ProjectResourceAccount(ctx, store, saved)
	if err != nil || !owner.Active() || owner.ResourceOwnerID() != f.org.ID || owner.Plan != api.PlanPro {
		t.Fatalf("project policy follows creator: %+v %v", owner, err)
	}
	// Both the legacy creator namespace and an organization UUID admit future workloads.
	for _, namespace := range []string{project.AccountID, f.org.ID} {
		p := project
		if namespace != project.AccountID {
			p, err = store.CreateProject(ctx, state.Project{AccountID: namespace, OrgID: f.org.ID, Slug: "new-company-project"})
			if err != nil {
				t.Fatal(err)
			}
		}
		created, err := store.CreateAppIfUnderQuota(ctx, state.App{AccountID: namespace, ProjectID: p.ID, Slug: "after-erasure-" + p.Slug, WorkloadName: "worker"}, api.MustLimitsFor(api.PlanPro))
		if err != nil || created.OrgID != f.org.ID {
			t.Fatalf("future workload lost organization: %+v %v", created, err)
		}
	}
	if _, err := store.AccountByID(ctx, f.org.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("resource namespace authenticates: %v", err)
	}
}
