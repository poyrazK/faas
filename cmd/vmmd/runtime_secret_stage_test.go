// adr: 569
package main

import (
	"io"
	"log/slog"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRuntimeSecretsStageScopeAndLifetime(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "runtime-secrets@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "runtime-secrets"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "runtime-secrets", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil)
	manager.SetHostIdentity(identity)
	writeSecret := func(scope, value string) {
		t.Helper()
		sealed, err := secretbox.Seal(identity.Recipient(), secretbox.Envelope{"TOKEN": value})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAppSecretInScope(ctx, account.ID, app.ID, scope, "TOKEN", sealed); err != nil {
			t.Fatal(err)
		}
	}
	var stageDeployment state.Deployment
	for _, scope := range []string{"default", "production", "stage", "other"} {
		if scope == "stage" || scope == "other" {
			if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: scope}); err != nil {
				t.Fatal(err)
			}
		}
		if scope != "default" {
			if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, scope, app.ID, 0, settings); err != nil {
				t.Fatal(err)
			}
		}
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		writeSecret(scope, scope+"-private")
		if scope == "stage" {
			stageDeployment = deployment
		}
		manager.RegisterInstanceForTest(scope, deployment.ID, app.ID, account.ID)
	}
	receiver := &runtimeConfigReceiver{ctx: ctx, mgr: manager, store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, scope := range []string{"stage", "default", "other", "production", "stage"} {
		response := sendRuntimeConfigTestRequestForInstance(t, receiver, scope, runtimeConfigRequest{Kind: "secrets"})
		if response.Error != "" || response.Secrets == nil || len(*response.Secrets) != 1 || (*response.Secrets)["TOKEN"] != scope+"-private" {
			t.Fatalf("%s secret projection: %+v", scope, response)
		}
	}
	if err := store.MarkDeploymentSuperseded(ctx, stageDeployment.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	writeSecret("stage", "replacement-private")
	response := sendRuntimeConfigTestRequestForInstance(t, receiver, "stage", runtimeConfigRequest{Kind: "secrets"})
	if response.Error != "secrets_unavailable" || response.Secrets != nil {
		t.Fatalf("old VM read replacement stage secret: %+v", response)
	}
	response = sendRuntimeConfigTestRequestForInstance(t, receiver, "production", runtimeConfigRequest{Kind: "secrets"})
	if response.Error != "" || response.Secrets == nil || (*response.Secrets)["TOKEN"] != "production-private" {
		t.Fatalf("stage replacement affected production: %+v", response)
	}
}
