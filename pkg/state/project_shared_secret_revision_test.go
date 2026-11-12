// adr: 20261104201200000
package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemSharedSecretQualificationRevisions(t *testing.T) {
	for _, mutation := range []string{"add", "rotate", "delete", "recreate"} {
		t.Run(mutation, func(t *testing.T) { testSharedSecretQualificationRevisions(t, state.NewMemStore(), mutation) })
	}
}

func testSharedSecretQualificationRevisions(t *testing.T, store state.Store, mutation string) {
	t.Helper()
	ctx := t.Context()
	account, project, app := environmentLifecycleFixture(t, store)
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	app, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	deployment := lifecycleLiveDeployment(t, store, app, "production")
	release, err := store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 300, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: deployment.ID}})
	if err != nil {
		t.Fatal(err)
	}
	secrets := store.(state.ProjectEnvironmentSecretStore)
	reader := store.(state.ProjectSharedSecretRevisionReader)
	qualifications := store.(state.ProjectEnvironmentQualificationStore)
	secret := state.ProjectEnvironmentSecret{AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "production",
		Key: "PAYMENTS_KEY", Ciphertext: []byte("sealed"), Kid: "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3290gq", ValueHash: "aa"}
	if mutation != "add" {
		if _, err := secrets.SetProjectEnvironmentSecret(ctx, secret, 10); err != nil {
			t.Fatal(err)
		}
	}
	observed, err := reader.ProjectEnvironmentSharedSecretRevision(ctx, account.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	workloadSecretHash, err := api.ProjectEnvironmentSecretRevisionHash(nil)
	if err != nil {
		t.Fatal(err)
	}
	workloadHashes, _, err := store.(state.ProjectEnvironmentWorkloadQualificationStore).ProjectEnvironmentWorkloadConfigHashes(ctx, account.ID, project.ID, "production", release.ID)
	if err != nil {
		t.Fatal(err)
	}
	httpOK := 204
	result := state.ProjectEnvironmentQualificationResult{WorkloadSlug: app.Slug, DeploymentID: deployment.ID, Status: "passed", HTTPStatus: &httpOK}
	checks := []state.ProjectEnvironmentQualificationCheck{
		{Name: "health", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{result}},
		{Name: "smoke", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{result}},
	}
	client := map[string]string{app.Slug: workloadSecretHash, api.ProjectSecretsRevisionKey: observed.Hash}
	post := func(hashes map[string]string) (state.ProjectEnvironmentQualification, error) {
		return qualifications.CreateProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", release.ID, 0, api.EmptyProjectEnvironmentConfigHash(), hashes, checks, workloadHashes)
	}
	if !observed.Tracked {
		if _, err := post(map[string]string{app.Slug: workloadSecretHash}); err != nil {
			t.Fatalf("legacy client refused for an environment without shared secrets: %v", err)
		}
	}
	passing, err := post(client)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Tracked && passing.SecretRevisionHashes[api.ProjectSecretsRevisionKey] != observed.Hash {
		t.Fatal("qualification did not preserve the observed secret set")
	}
	lifecycleEnvironment(t, store, account, project, "staging")
	candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindTarball, ImageDigest: deployment.ImageDigest, TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, candidate.ID, "/rootfs/"+candidate.ID, "apps/"+candidate.ID, 42); err != nil {
		t.Fatal(err)
	}
	if err := store.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	createPromotion := func(qualification state.ProjectEnvironmentQualification, key string) state.ProjectEnvironmentPromotion {
		t.Helper()
		promotion, _, err := store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{
			AccountID: account.ID, ProjectID: project.ID, ProjectSlug: project.Slug, FromEnvironment: "production", ToEnvironment: "staging",
			Status: "running", VerificationStatus: "pending", PromotionHash: observed.Hash, IdempotencyKey: key,
			ReleaseGraphMode: true, SourceReleaseSetID: release.ID, SourceQualificationID: qualification.ID, ReleaseTTLSeconds: 300,
		}, []state.ProjectEnvironmentPromotionWorkload{{WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName, SourceDeploymentID: deployment.ID, TargetDeploymentID: candidate.ID, Status: "promoted"}})
		if err != nil {
			t.Fatal(err)
		}
		return promotion
	}
	promotion := createPromotion(passing, "before-secret-change")
	if mutation == "delete" || mutation == "recreate" {
		if err := secrets.DeleteProjectEnvironmentSecret(ctx, account.ID, project.ID, "production", secret.Key); err != nil {
			t.Fatal(err)
		}
	}
	if mutation != "delete" {
		secret.ValueHash, secret.Ciphertext = "bb", []byte("replacement")
		updated, err := secrets.SetProjectEnvironmentSecret(ctx, secret, 10)
		if err != nil {
			t.Fatal(err)
		}
		if mutation == "recreate" && updated.SecretVersion != 1 {
			t.Fatal("recreation did not exercise reused key/version metadata")
		}
	}
	current, err := reader.ProjectEnvironmentSharedSecretRevision(ctx, account.ID, project.ID, "production")
	if err != nil || current.Hash == observed.Hash || !current.Tracked {
		t.Fatalf("secret mutation retained qualification identity: %+v %v", current, err)
	}
	if _, err := post(client); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale probe evidence accepted after %s: %v", mutation, err)
	}
	publisher := store.(state.ProjectEnvironmentPromotionReleaseSetStore)
	if _, err := publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, 300, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: candidate.ID}}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("final publication accepted stale qualification after %s: %v", mutation, err)
	}
	if _, err := post(map[string]string{app.Slug: workloadSecretHash}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("legacy evidence accepted for a changed secret set: %v", err)
	}
	client[api.ProjectSecretsRevisionKey] = current.Hash
	fresh, err := post(client)
	if err != nil {
		t.Fatalf("fresh probe evidence refused: %v", err)
	}
	freshPromotion := createPromotion(fresh, "after-secret-change")
	if _, err := publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, account.ID, freshPromotion.ID, 300, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: candidate.ID}}); err != nil {
		t.Fatalf("fresh qualification could not publish the candidate: %v", err)
	}
}
