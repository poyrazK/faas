// adr: 569
package sched

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type rotatingRuntimeValuesStore struct {
	state.Store
	afterRead func()
	reads     int
}

func (s *rotatingRuntimeValuesStore) RuntimeAppValuesForDeployment(ctx context.Context, accountID, appID, deploymentID string) (state.RuntimeAppValuesSnapshot, error) {
	snapshot, err := s.Store.RuntimeAppValuesForDeployment(ctx, accountID, appID, deploymentID)
	s.reads++
	if err == nil && s.afterRead != nil {
		s.afterRead()
	}
	return snapshot, err
}

func TestBuildAppSpecForMigrationUsesOneOwnedValuesSnapshot(t *testing.T) {
	ctx := t.Context()
	mem := state.NewMemStore()
	account, _, _ := seedApp(t, mem, api.PlanPro, 256, 5)
	project, err := mem.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "migration-values"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "migration-values", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "stage", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	deployment, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "stage", Status: state.DeployLive,
		OverrideEnvSecrets: json.RawMessage(`{"MAIN_TOKEN":"secret:MAIN_TOKEN"}`),
		Sidecars:           json.RawMessage(`[{"name":"proxy","type":"sidecar","env_secrets":{"DATABASE_URL":"secret:DATABASE_URL"}}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := mem.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 256, "dying", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{DeploymentID: deployment.ID, SidecarName: "proxy", StorageKey: "apps/app/proxy.ext4"}); err != nil {
		t.Fatal(err)
	}
	write := func(scope, version string) {
		t.Helper()
		if err := mem.UpsertAppEnvInScope(ctx, account.ID, app.ID, scope, "MODE", scope+"-"+version); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"MAIN_TOKEN", "DATABASE_URL", "UNGRANTED_TOKEN"} {
			if err := mem.UpsertAppSecretInScope(ctx, account.ID, app.ID, scope, key, []byte(scope+"-"+key+"-"+version)); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("default", "production")
	write("stage", "before")
	store := &rotatingRuntimeValuesStore{Store: mem, afterRead: func() { write("stage", "after") }}
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	spec, err := engine.BuildAppSpecForMigration(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	mode := ""
	for _, row := range spec.APIEnv {
		if row.Key == "MODE" {
			mode = row.Value
		}
	}
	if store.reads != 1 || mode != "stage-before" || len(spec.SealedEnv) != 1 || spec.SealedEnv[0].Key != "MAIN_TOKEN" || string(spec.SealedEnv[0].Ciphertext) != "stage-MAIN_TOKEN-before" ||
		len(spec.Sidecars) != 1 || len(spec.Sidecars[0].SealedSecrets) != 1 || spec.Sidecars[0].SealedSecrets[0].Key != "DATABASE_URL" || string(spec.Sidecars[0].SealedSecrets[0].Ciphertext) != "stage-DATABASE_URL-before" {
		t.Fatalf("mixed boot inputs: reads=%d mode=%q main=%+v sidecars=%+v", store.reads, mode, spec.SealedEnv, spec.Sidecars)
	}
	if err := mem.MarkDeploymentSuperseded(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	if err := mem.DeleteProjectEnvironment(ctx, account.ID, project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	write("stage", "replacement")
	if spec, err := engine.BuildAppSpecForMigration(ctx, instance.ID); err == nil || len(spec.APIEnv) != 0 || len(spec.SealedEnv) != 0 || len(spec.Sidecars) != 0 {
		t.Fatalf("old instance built replacement stage spec: %+v %v", spec, err)
	}
}

type staleSecretPolicyStore struct {
	state.Store
	policyReads int
}

func (s *staleSecretPolicyStore) ListAppSecretsInScope(context.Context, string, string, string) ([]state.AppSecret, error) {
	s.policyReads++
	return nil, nil
}

func TestEngineWakeSnapshotPolicyUsesOwnedValues(t *testing.T) {
	ctx := t.Context()
	mem := state.NewMemStore()
	account, app, dep := seedApp(t, mem, api.PlanPro, 512, 5)
	if _, err := mem.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: 512 << 20, StorageKey: SnapshotMemKey(dep.ID)}); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertAppSecretWithClassInScope(ctx, account.ID, app.ID, api.DefaultEnvScope, "SESSION_TOKEN", "kid", "hash", state.SecretClassEphemeral, []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	store := &staleSecretPolicyStore{Store: mem}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := engine.Wake(ctx, app.ID, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if vmm.restores != 0 || vmm.coldBoots != 1 || store.policyReads != 0 {
		t.Fatalf("snapshot policy detached from payload: restores=%d cold=%d separate_reads=%d", vmm.restores, vmm.coldBoots, store.policyReads)
	}
}
