// adr: 1139
package sched

import (
	"context"
	"encoding/json"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// A project environment's secrets reach the guest as sealed entries ahead of
// the app's own, each bound to its one key, and never as delivery candidates.
// A deployment with an explicit secret selection inherits none.
func TestRuntimeValuesDeliverProjectSecretsBelowAppSecrets(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "project-secrets-env@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "project-secrets-env"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "project-secrets-env-api",
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	kid := identity.Recipient().String()
	if err := store.UpsertAppSecretWithClassInScope(ctx, account.ID, app.ID, "default", "APP_OWN", kid, "aa", state.SecretClassPersistent, []byte("app-envelope")); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"DATABASE_PASSWORD", "APP_OWN"} {
		if _, err := store.SetProjectEnvironmentSecret(ctx, state.ProjectEnvironmentSecret{AccountID: account.ID, ProjectID: project.ID,
			EnvironmentSlug: "production", Key: key, Ciphertext: []byte("project-envelope-" + key), Kid: kid, ValueHash: "bb"}, 10); err != nil {
			t.Fatal(err)
		}
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}

	engine := &Engine{store: store}
	loaded, err := engine.loadRuntimeDeploymentValues(ctx, app, dep)
	if err != nil {
		t.Fatalf("loadRuntimeDeploymentValues: %v", err)
	}
	entries := loaded.MainSecrets.Entries
	if len(entries) != 2 || entries[0].Key != "DATABASE_PASSWORD" || entries[0].SourceKey != "DATABASE_PASSWORD" ||
		string(entries[0].Ciphertext) != "project-envelope-DATABASE_PASSWORD" || entries[1].Key != "APP_OWN" || string(entries[1].Ciphertext) != "app-envelope" {
		t.Fatalf("sealed entries = %+v, want the project secret first and the app's own APP_OWN only", entries)
	}
	for _, candidate := range loaded.MainSecrets.Candidates {
		if candidate.Key != "APP_OWN" {
			t.Fatalf("delivery candidates = %+v, want only the app's own secrets", loaded.MainSecrets.Candidates)
		}
	}
	if len(loaded.Inputs.SecretVersions) > 1 {
		t.Fatalf("receipt secret inputs = %v, want only the app's own", loaded.Inputs.SecretVersions)
	}

	// An explicit selection is a least-privilege grant.
	grants, _ := json.Marshal(map[string]string{"APP_OWN": api.SecretRefPrefix + "APP_OWN"})
	selected, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployPending, OverrideEnvSecrets: grants})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = engine.loadRuntimeDeploymentValues(ctx, app, selected)
	if err != nil {
		t.Fatalf("loadRuntimeDeploymentValues(selected): %v", err)
	}
	for _, entry := range loaded.MainSecrets.Entries {
		if entry.Key == "DATABASE_PASSWORD" {
			t.Fatalf("an explicit selection inherited a project secret: %+v", loaded.MainSecrets.Entries)
		}
	}
}

func TestWithInheritedProjectSecretsSkipsDeliveredKeys(t *testing.T) {
	inherited := []state.ProjectEnvironmentSecret{{Key: "A", Ciphertext: []byte("a")}, {Key: "B", Ciphertext: []byte("b")}}
	if got := withInheritedProjectSecrets(nil, nil); got != nil {
		t.Fatalf("nothing inherited = %+v", got)
	}
	got := withInheritedProjectSecrets(inherited, nil)
	if len(got) != 2 || got[0].SourceKey != "A" || got[1].SourceKey != "B" {
		t.Fatalf("entries = %+v", got)
	}
	// B reaches the app through one of its own entries (an alias, say), so the
	// inherited B is dropped rather than shadowed.
	own := []fcvm.SealedEnvEntry{{Key: "B", Ciphertext: []byte("own-b"), SourceKey: "SOURCE"}}
	got = withInheritedProjectSecrets(inherited, own)
	if len(got) != 2 || got[0].Key != "A" || got[1].Key != "B" || string(got[1].Ciphertext) != "own-b" {
		t.Fatalf("entries = %+v, want inherited A then the app's own B", got)
	}
}
