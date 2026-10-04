// spec: §4.6, §17
package imaged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFullRootfsCommandPATH(t *testing.T) {
	cases := []struct {
		name           string
		scope          string
		envScope       string
		secretScope    string
		selected       map[string]string
		command        string
		want           string
		deferred       bool
		failEnvRead    bool
		failSecretRead bool
	}{
		{name: "image PATH", want: "/image/bin"},
		{name: "scoped API PATH wins", scope: "prod", envScope: "prod", want: "/runtime/bin"},
		{name: "empty scope uses default", envScope: api.DefaultEnvScope, want: "/runtime/bin"},
		{name: "other scope env ignored", scope: "prod", envScope: api.DefaultEnvScope, want: "/image/bin"},
		{name: "legacy secret PATH deferred", secretScope: api.DefaultEnvScope, deferred: true},
		{name: "scoped secret PATH deferred", scope: "prod", secretScope: "prod", deferred: true},
		{name: "other scope secret ignored", scope: "prod", secretScope: api.DefaultEnvScope, want: "/image/bin"},
		{name: "explicit sealed PATH deferred", selected: map[string]string{"PATH": "secret:PATH"}, deferred: true},
		{name: "unselected PATH secret ignored", selected: map[string]string{"TOKEN": "secret:TOKEN"}, secretScope: api.DefaultEnvScope, want: "/image/bin"},
		{name: "API read failure defers", failEnvRead: true, deferred: true},
		{name: "secret read failure defers", failSecretRead: true, deferred: true},
		{name: "absolute command needs no env reads", command: "/app/server", failEnvRead: true, failSecretRead: true, deferred: true},
		{name: "relative command needs no env reads", command: "./server", failEnvRead: true, failSecretRead: true, deferred: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "launch@example.com", "pro")
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "launch", RAMMB: 512, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			if tc.envScope != "" {
				if err := store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, tc.envScope, "PATH", "/runtime/bin"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.secretScope != "" {
				if err := store.UpsertAppSecretInScope(ctx, acct.ID, app.ID, tc.secretScope, "PATH", []byte("sealed-not-plaintext")); err != nil {
					t.Fatal(err)
				}
			}
			command := tc.command
			if command == "" {
				command = "server"
			}
			m := api.AppManifest{Entrypoint: []string{command}, Env: map[string]string{"PATH": "/image/bin"}, EnvSecrets: tc.selected}
			reads := &launchEnvReadStore{Store: store, failEnv: tc.failEnvRead, failSecret: tc.failSecretRead}
			got := fullRootfsCommandPATH(ctx, reads, app, state.Deployment{Scope: tc.scope}, m)
			if tc.deferred {
				if got != nil {
					t.Fatalf("unknown/unused PATH was read: %q", *got)
				}
			} else if got == nil || *got != tc.want {
				t.Fatalf("PATH = %v, want %q", got, tc.want)
			}
			if m.Env["PATH"] != "/image/bin" {
				t.Fatal("runtime PATH was persisted into image manifest")
			}
			if tc.command != "" && reads.calls != 0 {
				t.Fatal("explicit command performed unnecessary env reads")
			}
		})
	}
}

type launchEnvReadStore struct {
	state.Store
	failEnv, failSecret bool
	calls               int
}

func (s *launchEnvReadStore) ListAppEnvInScope(ctx context.Context, account, app, scope string) ([]state.AppEnv, error) {
	s.calls++
	if s.failEnv {
		return nil, errors.New("env read failed")
	}
	return s.Store.ListAppEnvInScope(ctx, account, app, scope)
}

func (s *launchEnvReadStore) ListAppSecretsInScope(ctx context.Context, account, app, scope string) ([]state.AppSecret, error) {
	s.calls++
	if s.failSecret {
		return nil, errors.New("secret read failed")
	}
	return s.Store.ListAppSecretsInScope(ctx, account, app, scope)
}

// Exercise the real assembler through imaged's dispatch seam: a bad image
// must become a customer-visible failed deployment, without publishing an
// artifact, invoking mkfs, or asking schedd to boot it.
func TestFullRootfsInvalidLaunchPersistsDeploymentFailure(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "launch@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "invalid-launch", RAMMB: 512, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "ghcr.io/org/app@sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	layer := "sha256:" + strings.Repeat("b", 64)
	puller := &fakeManifestPuller{
		appRef: dep.ImageDigest, appManifest: oci.Manifest{Layers: []oci.Descriptor{{Digest: layer}}},
		layerBlobs: map[string][]byte{layer: gzTar(t, nil)},
	}
	gi := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(gi, []byte("init"), 0o755); err != nil {
		t.Fatal(err)
	}
	notif := &fakeNotifier{}
	run := &rejectLaunchMkfsRunner{t: t}
	h := New(store, notif, puller, rootfs.NewBuilder(run), gi, t.TempDir(), silentLogger())
	be := mustLocalStorage(t, t.TempDir())
	h.storage = be
	manifest, err := applyOverrides(api.AppManifest{Entrypoint: []string{"/image/original"}}, state.Deployment{
		OverrideEntrypoint: []string{"/app/missing", "argument-not-for-diagnostics"},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = h.buildFullRootfsLayer(ctx, app, dep, acct, manifest, nil)
	if !errors.Is(err, oci.ErrImageManifestInvalid) {
		t.Fatalf("build error = %v", err)
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.DeployFailed || got.ErrorCode != api.CodeImageManifestInvalid || !strings.Contains(got.Error, "/app/missing") {
		t.Fatalf("deployment failure = %s %q %q", got.Status, got.ErrorCode, got.Error)
	}
	if strings.Contains(got.Error, "argument-not-for-diagnostics") || len(notif.calls) != 0 {
		t.Fatalf("invalid launch leaked argv or dispatched downstream: %q %v", got.Error, notif.calls)
	}
}

type rejectLaunchMkfsRunner struct{ t *testing.T }

func (r *rejectLaunchMkfsRunner) Run(context.Context, []string) error {
	r.t.Fatal("invalid launch reached mkfs")
	return nil
}
