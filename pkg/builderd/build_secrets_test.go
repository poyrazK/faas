// adr: 20261022185329, 20261024140000
package builderd

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestLoadBuildSecrets(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(t.Context(), "build-secrets@example.com", api.PlanPro)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "build-secrets", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretInScope(t.Context(), acct.ID, app.ID, api.DefaultEnvScope, "NPM_TOKEN", []byte("sealed-npm")); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretInScope(t.Context(), acct.ID, app.ID, "staging", "GH_TOKEN", []byte("sealed-gh-staging")); err != nil {
		t.Fatal(err)
	}
	deployIn := func(scope string) state.Deployment {
		t.Helper()
		dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindDockerfile})
		if err != nil {
			t.Fatal(err)
		}
		return dep
	}
	defaultDep, stagingDep := deployIn(""), deployIn("staging")
	secrets := []api.DockerfileBuildSecret{{ID: "npm", Secret: "NPM_TOKEN"}, {ID: "npm-again", Secret: "NPM_TOKEN"}}
	got, err := loadBuildSecrets(t.Context(), store, app, defaultDep, secrets)
	if err != nil || !reflect.DeepEqual(got, []SealedBuildSecret{{Key: "NPM_TOKEN", Ciphertext: []byte("sealed-npm")}}) {
		t.Fatalf("default scope = %+v, %v; want one deduplicated ciphertext", got, err)
	}
	if got, err = loadBuildSecrets(t.Context(), store, app, stagingDep, []api.DockerfileBuildSecret{{ID: "gh", Secret: "GH_TOKEN"}}); err != nil || len(got) != 1 || string(got[0].Ciphertext) != "sealed-gh-staging" {
		t.Fatalf("staging scope = %+v, %v", got, err)
	}
	_, err = loadBuildSecrets(t.Context(), store, app, defaultDep, []api.DockerfileBuildSecret{{ID: "npm", Secret: "NPM_TOKEN"}, {ID: "gh", Secret: "GH_TOKEN"}})
	if !errors.Is(err, errBuildSecretMissing) || !strings.Contains(err.Error(), "GH_TOKEN not set in scope default") || strings.Contains(err.Error(), "sealed") {
		t.Fatalf("missing secret error = %v", err)
	}
	if got, err := loadBuildSecrets(t.Context(), store, app, defaultDep, nil); err != nil || got != nil {
		t.Fatalf("no secrets = %+v, %v", got, err)
	}
}

// A build secret reads what the app reads as that variable at runtime
// (ADR-20261024140000): an alias the environment declares, a project
// environment secret the app inherits, and nothing a grant leaves out.
func TestLoadBuildSecretsResolvesLikeTheRuntime(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "build-secret-sources@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "build-secret-sources"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "production"); errors.Is(err, state.ErrNotFound) {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "production"}); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "build-secret-sources", Type: state.AppTypeApp,
		RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetProjectEnvironmentSecret(ctx, state.ProjectEnvironmentSecret{AccountID: acct.ID, ProjectID: project.ID,
		EnvironmentSlug: "production", Key: "SHARED_TOKEN", Ciphertext: []byte("sealed-shared"), Kid: "age1buildtestkid", ValueHash: "hash-1"}, 10); err != nil {
		t.Fatal(err)
	}
	// References belong to a project environment, so the app deploys there.
	const scope = "production"
	if err := store.UpsertAppSecretInScope(ctx, acct.ID, app.ID, scope, "REAL_NPM_TOKEN", []byte("sealed-npm")); err != nil {
		t.Fatal(err)
	}
	if err := store.PutAppEnvironmentSecretReference(ctx, acct.ID, app.ID, scope, "NPM_TOKEN", api.SecretRefPrefix+"REAL_NPM_TOKEN"); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindDockerfile})
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadBuildSecrets(ctx, store, app, dep, []api.DockerfileBuildSecret{{ID: "npm", Secret: "NPM_TOKEN"}, {ID: "shared", Secret: "SHARED_TOKEN"}})
	want := []SealedBuildSecret{
		{Key: "NPM_TOKEN", SourceKey: "REAL_NPM_TOKEN", Ciphertext: []byte("sealed-npm")},
		{Key: "SHARED_TOKEN", SourceKey: "SHARED_TOKEN", Ciphertext: []byte("sealed-shared")},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved = %+v, %v; want %+v", got, err, want)
	}
	if !buildSecretsNeedAliasSupport(got) || buildSecretsNeedAliasSupport([]SealedBuildSecret{{Key: "A", Ciphertext: []byte("x")}}) {
		t.Fatal("alias support detection is wrong")
	}

	// An explicit grant is least privilege: it inherits no project secret.
	granted, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindDockerfile,
		OverrideEnvSecrets: json.RawMessage(`{"NPM_TOKEN":"secret:REAL_NPM_TOKEN"}`)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = loadBuildSecrets(ctx, store, app, granted, []api.DockerfileBuildSecret{{ID: "shared", Secret: "SHARED_TOKEN"}})
	if !errors.Is(err, errBuildSecretMissing) || !strings.Contains(err.Error(), "SHARED_TOKEN not set") {
		t.Fatalf("granted deployment read a project secret: %v", err)
	}
}

func TestBuildSecretsReachTheBuilderWithoutAWarmVM(t *testing.T) {
	options := &api.DockerfileBuildOptions{Secrets: []api.DockerfileBuildSecret{{ID: "npm", Secret: "NPM_TOKEN"}}}
	for _, tc := range []struct {
		name      string
		setSecret bool
		wantErr   bool
	}{
		{"secret set", true, false},
		{"secret missing", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			acct, _ := store.CreateAccount(t.Context(), "secret-build@example.com", api.PlanPro)
			app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "secret-build", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5,
				Manifest: state.AppManifest{BuildDockerfile: "Dockerfile", DockerfileBuild: options}})
			if err != nil {
				t.Fatal(err)
			}
			if tc.setSecret {
				if err := store.UpsertAppSecretInScope(t.Context(), acct.ID, app.ID, api.DefaultEnvScope, "NPM_TOKEN", []byte("sealed-npm")); err != nil {
					t.Fatal(err)
				}
			}
			profile, err := json.Marshal(frameworkprofile.Profile{Version: frameworkprofile.Version, Framework: "docker", DockerfilePath: "Dockerfile", DockerfileBuild: options})
			if err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(t.TempDir(), "src.tar.gz")
			makeTarballWithName(t, src, []string{"Dockerfile", "server.js"})
			dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, SourcePath: src,
				InferredProfile: profile, LogPath: filepath.Join(t.TempDir(), "build.log")})
			if err != nil {
				t.Fatal(err)
			}
			build, err := store.CreateBuild(t.Context(), dep.ID, state.DeploymentKindDockerfile, 100, dep.LogPath)
			if err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(artifact, []byte("with secrets"), 0o600); err != nil {
				t.Fatal(err)
			}
			vm := &fakeVM{out: BuildOutcome{OCIImage: artifact, DockerfileBuildSHA256: options.SHA256()}}
			b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			_, err = b.ProcessOne(t.Context(), build.ID)
			if tc.wantErr {
				if !errors.Is(err, errBuildSecretMissing) || vm.spawnCalls != 0 {
					t.Fatalf("missing secret: err=%v spawns=%d", err, vm.spawnCalls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(vm.lastRequest.BuildSecrets, []SealedBuildSecret{{Key: "NPM_TOKEN", Ciphertext: []byte("sealed-npm")}}) {
				t.Fatalf("builder received %+v", vm.lastRequest.BuildSecrets)
			}
			if vm.lastRequest.WarmScopeKey != "" {
				t.Fatalf("a build with secrets kept warm scope %q", vm.lastRequest.WarmScopeKey)
			}
		})
	}
}

func TestSealedBuildSecretsToProto(t *testing.T) {
	if got := sealedBuildSecretsToProto(nil); got != nil {
		t.Fatalf("nil secrets = %v", got)
	}
	got := sealedBuildSecretsToProto([]SealedBuildSecret{{Key: "NPM_TOKEN", Ciphertext: []byte("sealed")}, {Key: "ALIAS", SourceKey: "REAL", Ciphertext: []byte("sealed-real")}})
	if len(got) != 2 || got[0].GetKey() != "NPM_TOKEN" || string(got[0].GetCiphertext()) != "sealed" || got[0].GetSourceKey() != "" ||
		got[1].GetKey() != "ALIAS" || got[1].GetSourceKey() != "REAL" {
		t.Fatalf("proto = %+v", got)
	}
}
