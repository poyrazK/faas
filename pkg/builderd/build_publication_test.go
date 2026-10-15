package builderd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func buildPublicationSigner(t *testing.T) (buildpublisher.Signer, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "publisher.key")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := buildpublisher.NewFileSigner("company", path)
	if err != nil {
		t.Fatal(err)
	}
	return signer, der
}

func TestBuilderApprovedExportPrecedesCompletionAndHandoff(t *testing.T) {
	for _, scenario := range []string{"approved", "approved-alias", "policy-approved", "unapproved", "policy-unapproved", "unsigned-unapproved", "wrong-key", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			s := state.NewMemStore()
			source := filepath.Join(t.TempDir(), "source.tar.gz")
			makeTarballWithName(t, source, []string{"package.json"})
			id, depID, appID := seedDeployment(t, s, source)
			app, err := s.AppByID(t.Context(), appID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "policy-approved" || scenario == "policy-unapproved" {
				policy := api.AppSecurityPolicyEnforce
				app, err = s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &policy})
				if err != nil {
					t.Fatal(err)
				}
			} else if scenario != "unsigned-unapproved" {
				required := true
				app, err = s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetRequireSigned: true, RequireSigned: &required})
				if err != nil {
					t.Fatal(err)
				}
			}
			signer, der := buildPublicationSigner(t)
			if scenario == "wrong-key" {
				_, der = buildPublicationSigner(t)
			}
			name := "company"
			if scenario == "approved-alias" {
				name = "standard-11111111-2222-3333-4444-555555555555"
			}
			if scenario != "unapproved" && scenario != "policy-unapproved" && scenario != "unsigned-unapproved" {
				if _, _, err := s.UpsertAppTrustedSigner(t.Context(), app.AccountID, app.ID, name, der, app.AccountID); err != nil {
					t.Fatal(err)
				}
			}
			export := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(export, []byte("complete builder export fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			vm := &fakeVM{out: BuildOutcome{OCIImage: export}}
			if scenario == "revoked" {
				vm.waitHook = func() {
					if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
						t.Fatal(err)
					}
				}
			}
			handoffs := 0
			notify := completionNotifier(func(ctx context.Context, ch, payload string) error {
				if ch == db.NotifySnapshotBoot {
					handoffs++
					if scenario == "unsigned-unapproved" {
						return nil
					}
					if _, err := s.GetFreshBuildExportPublication(ctx, app.AccountID, app.ID, depID, id); err != nil {
						t.Fatal("handoff preceded completed approved export", err)
					}
				}
				return nil
			})
			b := New(s, notify, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{BuilderNodeID: "builder-one"}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithBuildPublisher(signer)
			result, err := b.ProcessOne(t.Context(), id)
			dep, _ := s.DeploymentByID(t.Context(), depID)
			build, _ := s.BuildByID(t.Context(), id)
			if scenario == "unsigned-unapproved" {
				if err != nil || result.BuildID != id || handoffs != 1 || build.Status != state.BuildSucceeded {
					t.Fatal("permitted unsigned build was blocked by shared publisher configuration", err)
				}
				if _, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, depID, id); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("unsigned build claimed publisher approval", err)
				}
				return
			}
			if scenario != "approved" && scenario != "approved-alias" && scenario != "policy-approved" {
				if err == nil || handoffs != 0 || dep.RootfsPath != "" || build.Status != state.BuildFailed {
					t.Fatal("unapproved export published", err, dep.RootfsPath, build.Status)
				}
				return
			}
			if err != nil || result.BuildID != id || handoffs != 1 || build.Status != state.BuildSucceeded {
				t.Fatal("approved build did not hand off", err)
			}
			value, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, depID, id)
			if err != nil {
				t.Fatal(err)
			}
			if value.Input.Proof.PublisherName != name {
				t.Fatal("company-approved key alias was not retained")
			}
			d, n, err := buildpublisher.MeasureExport(t.Context(), export)
			if err != nil || value.Input.Claims.ExportDigest != d || value.Input.Claims.ExportBytes != n {
				t.Fatal("publisher omitted complete export", err)
			}
		})
	}
}

func TestBuilderApprovedExportCacheHitHasNewClaim(t *testing.T) {
	s := state.NewMemStore()
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, source, []string{"package.json"})
	id, depID, appID := seedDeployment(t, s, source)
	app, err := s.AppByID(t.Context(), appID)
	if err != nil {
		t.Fatal(err)
	}
	signer, der := buildPublicationSigner(t)
	if _, _, err := s.UpsertAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company", der, app.AccountID); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(export, []byte("complete cache export fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	vm := &fakeVM{out: BuildOutcome{OCIImage: export}}
	b := New(s, nil, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{BuilderNodeID: "builder-one"}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithBuildPublisher(signer)
	if _, err := b.ProcessOne(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	first, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, depID, id)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: source, SourceBytes: 100, LogPath: filepath.Join(t.TempDir(), "build.log")})
	if err != nil {
		t.Fatal(err)
	}
	build, err := s.CreateBuild(t.Context(), dep.ID, dep.Kind, 100, dep.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.ProcessOne(t.Context(), build.ID)
	if err != nil || !result.CacheHit || vm.spawnCalls != 1 {
		t.Fatal("cache handoff did not reuse export", err, result.CacheHit, vm.spawnCalls)
	}
	second, err := s.GetFreshBuildExportPublication(t.Context(), app.AccountID, app.ID, dep.ID, build.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Input.Claims.BuildID != canonicalBuildID(build.ID) || second.Input.Claims.DeploymentID != canonicalBuildID(dep.ID) || second.InputHash == first.InputHash || second.ID == first.ID || second.Input.Claims.ExportDigest != first.Input.Claims.ExportDigest {
		t.Fatal("cache evidence reused a previous build claim")
	}
}

func TestBuilderSignedSourceRefusesMissingPublisher(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		required bool
		policy   api.AppSecurityPolicy
	}{{"require-signed", true, api.AppSecurityPolicyOff}, {"enforce-policy", false, api.AppSecurityPolicyEnforce}} {
		t.Run(scenario.name, func(t *testing.T) {
			s := state.NewMemStore()
			id, depID, appID := seedDeployment(t, s, "/source.tar.gz")
			claim, err := s.ClaimQueuedBuild(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.UpdateApp(t.Context(), appID, state.UpdateAppParams{SetRequireSigned: true, RequireSigned: &scenario.required, SetSecurityPolicy: true, SecurityPolicy: &scenario.policy})
			if err != nil {
				t.Fatal(err)
			}
			dep, _ := s.DeploymentByID(t.Context(), depID)
			acct, _ := s.AccountByID(t.Context(), app.AccountID)
			b := New(s, nil, nil, nil, nil, nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if _, err := b.completeBuild(t.Context(), claim, dep, app, acct, "", "", BuildResult{BuildID: id, LayerPath: "/image.tar", LayerBytes: 42}, claim.StartedAt); err == nil {
				t.Fatal("signed source fell back to platform identity")
			}
			build, _ := s.BuildByID(t.Context(), id)
			if build.Status != state.BuildFailed {
				t.Fatal("failed publication left running build")
			}
		})
	}
}
