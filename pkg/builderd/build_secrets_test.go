// adr: 20261022185329
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
	secrets := []api.DockerfileBuildSecret{{ID: "npm", Secret: "NPM_TOKEN"}, {ID: "npm-again", Secret: "NPM_TOKEN"}}
	got, err := loadBuildSecrets(t.Context(), store, app, state.Deployment{}, secrets)
	if err != nil || !reflect.DeepEqual(got, []SealedBuildSecret{{Key: "NPM_TOKEN", Ciphertext: []byte("sealed-npm")}}) {
		t.Fatalf("default scope = %+v, %v; want one deduplicated ciphertext", got, err)
	}
	if got, err = loadBuildSecrets(t.Context(), store, app, state.Deployment{Scope: "staging"}, []api.DockerfileBuildSecret{{ID: "gh", Secret: "GH_TOKEN"}}); err != nil || len(got) != 1 || string(got[0].Ciphertext) != "sealed-gh-staging" {
		t.Fatalf("staging scope = %+v, %v", got, err)
	}
	_, err = loadBuildSecrets(t.Context(), store, app, state.Deployment{}, []api.DockerfileBuildSecret{{ID: "npm", Secret: "NPM_TOKEN"}, {ID: "gh", Secret: "GH_TOKEN"}})
	if !errors.Is(err, errBuildSecretMissing) || !strings.Contains(err.Error(), "GH_TOKEN not set in scope default") || strings.Contains(err.Error(), "sealed") {
		t.Fatalf("missing secret error = %v", err)
	}
	if got, err := loadBuildSecrets(t.Context(), store, app, state.Deployment{}, nil); err != nil || got != nil {
		t.Fatalf("no secrets = %+v, %v", got, err)
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
	got := sealedBuildSecretsToProto([]SealedBuildSecret{{Key: "NPM_TOKEN", Ciphertext: []byte("sealed")}})
	if len(got) != 1 || got[0].GetKey() != "NPM_TOKEN" || string(got[0].GetCiphertext()) != "sealed" || got[0].GetSourceKey() != "" {
		t.Fatalf("proto = %+v", got)
	}
}
