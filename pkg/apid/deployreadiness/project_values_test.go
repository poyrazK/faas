// adr: 20261104201200000
package deployreadiness

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReadinessUsesProductionMembership(t *testing.T) {
	store := state.NewMemStore()
	account, project, app := seedReadiness(t, store)
	app.Manifest.RevisionPinTTLSeconds = 3600
	_, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest})
	must(t, err)
	seedReadinessGraph(t, store, account, project, app, "production")
	_, err = store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "qa"})
	must(t, err)
	extra, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "qa-only", WorkloadName: "qa-only", Type: state.AppTypeApp, RAMMB: 256})
	must(t, err)
	settings, err := state.WorkloadSettingsFromApp(extra)
	must(t, err)
	_, err = store.PutProjectEnvironmentWorkloadSpec(t.Context(), account.ID, project.ID, "qa", extra.ID, 0, settings)
	must(t, err)
	request := readinessRequest()
	request.Atomic = true
	if report := Check(t.Context(), store, account, project.Slug, request, Config{}); report.Status != "ready" {
		t.Fatalf("unrelated stage service blocked production readiness: %+v", report)
	}
	_, err = store.PutProjectEnvironmentWorkloadSpec(t.Context(), account.ID, project.ID, "production", extra.ID, 0, settings)
	must(t, err)
	if report := Check(t.Context(), store, account, project.Slug, request, Config{}); countCode(report, "atomic_source_incompatible") != 1 {
		t.Fatalf("omitting a production service was accepted: %+v", report)
	}
}

func TestReadinessIncludesInheritedProjectKeys(t *testing.T) {
	for _, baseline := range []bool{false, true} {
		name := "without baseline"
		if baseline {
			name = "with baseline"
		}
		t.Run(name, func(t *testing.T) {
			store := state.NewMemStore()
			account, project, app := seedReadiness(t, store)
			if baseline {
				app.Manifest.RevisionPinTTLSeconds = 3600
				_, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest})
				must(t, err)
				seedReadinessGraph(t, store, account, project, app, "production")
			}
			values, hash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"REGION":"private-region-value","DOCUMENT":{"nested":true}}`))
			must(t, err)
			_, err = store.CreateProjectEnvironmentConfigVersion(t.Context(), state.ProjectEnvironmentConfig{AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "production", ConfigHash: hash, Values: values})
			must(t, err)
			secret := state.ProjectEnvironmentSecret{AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "production", Key: "PAYMENTS_KEY",
				Ciphertext: []byte("sealed-private-value"), Kid: "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3290gq", ValueHash: "aa"}
			_, err = store.SetProjectEnvironmentSecret(t.Context(), secret, 10)
			must(t, err)
			request := readinessRequest()
			request.Workloads[0].Workload.EnvKeys = []string{"REGION", "PAYMENTS_KEY"}
			ready := Check(t.Context(), store, account, project.Slug, request, Config{})
			if ready.Status != "ready" {
				t.Fatalf("inherited keys reported missing: %+v", ready)
			}
			raw, err := json.Marshal(ready)
			must(t, err)
			if strings.Contains(string(raw), "private-region-value") || strings.Contains(string(raw), "sealed-private-value") {
				t.Fatal("project values leaked into readiness report")
			}
			secret.ValueHash = "bb"
			_, err = store.SetProjectEnvironmentSecret(t.Context(), secret, 10)
			must(t, err)
			request.ExpectedStateHash = ready.StateHash
			changed := Check(t.Context(), store, account, project.Slug, request, Config{})
			if countCode(changed, "deployment_inputs_changed") != 1 {
				t.Fatal("shared-secret rotation did not change readiness identity", changed)
			}
			request.ExpectedStateHash = ""
			request.ReleaseConfig = []byte(`{"env":{"api":{}}}`)
			must(t, store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "REGION", "app-override"))
			if report := Check(t.Context(), store, account, project.Slug, request, Config{}); countCode(report, "runtime_key_missing") != 0 {
				t.Fatal("replacing app variables discarded inherited keys", report)
			}
		})
	}
}

func TestReadinessExplicitSelectionDoesNotInheritProjectSecrets(t *testing.T) {
	store := state.NewMemStore()
	account, project, app := seedReadiness(t, store)
	_, err := store.SetProjectEnvironmentSecret(t.Context(), state.ProjectEnvironmentSecret{AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "production", Key: "PAYMENTS_KEY",
		Ciphertext: []byte("sealed"), Kid: "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3290gq", ValueHash: "aa"}, 10)
	must(t, err)
	deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, OverrideEnvSecrets: json.RawMessage(`{}`)})
	must(t, err)
	must(t, store.SetDeploymentRootfs(t.Context(), deployment.ID, "/rootfs/check", "layers/check", 4096))
	must(t, store.MarkDeploymentLive(t.Context(), deployment.ID))
	request := readinessRequest()
	request.Workloads[0].Workload.EnvKeys = []string{"PAYMENTS_KEY"}
	if report := Check(t.Context(), store, account, project.Slug, request, Config{}); countCode(report, "runtime_key_missing") != 1 {
		t.Fatalf("explicit selection inherited a project secret: %+v", report)
	}
}
