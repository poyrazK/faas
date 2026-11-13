// adr: 20261104201200000
package apidserver_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectPromotionRejectsRecreatedSharedSecret(t *testing.T) {
	srv, store, account, project, app := apidserver.NewProjectLifecycleFixture(t)
	ctx := t.Context()
	ram := 256
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	var err error
	app, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &ram, Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	var source state.Deployment
	for _, environment := range []string{"staging", "production"} {
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: environment, ImageDigest: "sha256:" + environment})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/"+deployment.ID, "apps/"+deployment.ID, 42); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		if environment == "staging" {
			source = deployment
		}
	}
	release, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	secret := state.ProjectEnvironmentSecret{AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "staging", Key: "PAYMENTS_KEY",
		Ciphertext: []byte("sealed"), Kid: "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3290gq", ValueHash: "aa"}
	if _, err := store.SetProjectEnvironmentSecret(ctx, secret, 10); err != nil {
		t.Fatal(err)
	}
	snapshot, problem := srv.LoadProjectEnvironmentState(ctx, account, project.Slug, "staging")
	if problem != nil || !api.ValidProjectEnvironmentConfigHash(snapshot.ProjectSecretsRevisionHash) {
		t.Fatalf("environment state omitted the observed secret-set revision: %+v %v", snapshot, problem)
	}
	qualification := createProjectEnvironmentQualificationForTest(t, store, account, project, "staging", release.ID, "passed", "passed")
	if qualification.SecretRevisionHashes[api.ProjectSecretsRevisionKey] != snapshot.ProjectSecretsRevisionHash {
		t.Fatal("qualification replaced the observed shared-secret identity")
	}
	plan, problem := srv.BuildProjectEnvironmentPromotionPlan(ctx, account, project.Slug, "staging", "production", false)
	if problem != nil || !plan.Preview.CanPromote {
		t.Fatalf("fresh qualification could not promote: %+v %v", plan.Preview, problem)
	}
	if err := store.DeleteProjectEnvironmentSecret(ctx, account.ID, project.ID, "staging", secret.Key); err != nil {
		t.Fatal(err)
	}
	secret.ValueHash, secret.Ciphertext = "bb", []byte("replacement")
	if _, err := store.SetProjectEnvironmentSecret(ctx, secret, 10); err != nil {
		t.Fatal(err)
	}
	plan, problem = srv.BuildProjectEnvironmentPromotionPlan(ctx, account, project.Slug, "staging", "production", false)
	if problem != nil || plan.Preview.CanPromote || !strings.Contains(strings.Join(plan.Preview.BlockingReasons, " "), "secret revisions changed after qualification") {
		t.Fatalf("recreated credential retained its qualification: %+v %v", plan.Preview, problem)
	}
}
