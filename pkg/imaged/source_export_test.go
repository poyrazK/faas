package imaged

// adr: 435. Real signed OCI/layer streams; fake ext4 conversion, no native proof.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type sourceExportFixture struct {
	store   *state.MemStore
	h       *Handler
	builder *fakeBuilder
	app     state.App
	dep     state.Deployment
	acct    state.Account
}

func newSourceExportFixture(t *testing.T, kind state.DeploymentKind, function, publish bool) sourceExportFixture {
	t.Helper()
	s := state.NewMemStore()
	owner, err := s.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	in := state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "source-" + uuid.NewString()[:8], RAMMB: 128, MaxConcurrency: 2, RequireSigned: publish}
	if function {
		in.Type, in.Runtime = state.AppTypeFunction, RuntimeGo124
	}
	app, err := s.CreateApp(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: kind, SourceSHA256: strings.Repeat("a", 64), SourcePath: "/must-not-read-source.tar"})
	if err != nil {
		t.Fatal(err)
	}
	archive := goArtifactFixture(t, "app/out", map[string]any{"Cmd": []string{"./out"}, "WorkingDir": "/app"})
	dep = completeSourceFixture(t, s, app, dep, archive, publish)
	builder := &fakeBuilder{bytesOut: 9}
	h := newHandlerWithBuilder(s, builder)
	h.appsRoot = t.TempDir()
	return sourceExportFixture{store: s, h: h, builder: builder, app: app, dep: dep, acct: owner.Account}
}

func completeSourceFixture(t *testing.T, s *state.MemStore, app state.App, dep state.Deployment, archive string, publish bool) state.Deployment {
	t.Helper()
	b, err := s.CreateBuild(t.Context(), dep.ID, dep.Kind, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.ClaimQueuedBuild(t.Context(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	digest, size, err := buildpublisher.MeasureExport(t.Context(), archive)
	if err != nil {
		t.Fatal(err)
	}
	if publish {
		publishSourceFixture(t, s, app, dep, b, digest, size)
	}
	p := state.BuildProvenance{BuildID: b.ID, SourceSHA256: dep.SourceSHA256, BuilderNodeID: "test-builder", StartedAt: b.StartedAt, FinishedAt: time.Now()}
	if err := s.CompleteBuild(t.Context(), b, archive, sched.AppLayerKey(app.Slug, dep.ID), size, p); err != nil {
		t.Fatal(err)
	}
	dep, err = s.DeploymentByID(t.Context(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	return dep
}

func publishSourceFixture(t *testing.T, s *state.MemStore, app state.App, dep state.Deployment, b state.Build, digest string, size int64) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company", der, app.AccountID); err != nil {
		t.Fatal(err)
	}
	c := buildpublisher.Claims{Format: buildpublisher.Format, AccountID: uuid.MustParse(app.AccountID).String(), OrgID: uuid.MustParse(app.OrgID).String(), AppID: uuid.MustParse(app.ID).String(), DeploymentID: uuid.MustParse(dep.ID).String(), BuildID: uuid.MustParse(b.ID).String(), ClaimStartedAt: b.StartedAt.UTC().Format(time.RFC3339Nano), SourceSHA256: dep.SourceSHA256, ExportDigest: digest, ExportBytes: size, Runtime: app.Runtime, BuilderNodeID: "test-builder"}
	proof, err := buildpublisher.Sign(t.Context(), c, "company", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordBuildExportPublication(t.Context(), state.BuildExportPublicationInput{ID: uuid.NewString(), Claims: c, Proof: proof}); err != nil {
		t.Fatal(err)
	}
}

func assertSourceLayers(t *testing.T, b *fakeBuilder, function bool) {
	t.Helper()
	if len(b.calls) != 1 {
		t.Fatal("unexpected conversion calls", len(b.calls))
	}
	in := b.calls[0]
	if in.TarballPath != "" {
		t.Fatal("conversion reapplied unsigned source", in.TarballPath)
	}
	target := t.TempDir()
	for _, layer := range in.Layers {
		if err := rootfs.ApplyLayerGz(target, layer); err != nil {
			t.Fatal(err)
		}
	}
	name := "out"
	if function {
		name = "server"
	}
	body, err := os.ReadFile(filepath.Join(target, "app", name))
	if err != nil || string(body) != "compiled-go-handler" {
		t.Fatal("approved layer not consumed", string(body), err)
	}
}

func TestApprovedSourceExportConsumesExactContainerAndFunction(t *testing.T) {
	for _, kind := range []state.DeploymentKind{state.DeploymentKindTarball, state.DeploymentKindDockerfile, state.DeploymentKindGitHub, state.DeploymentKindPreview} {
		for _, function := range []bool{false, true} {
			t.Run(string(kind)+"/"+map[bool]string{false: "container", true: "function"}[function], func(t *testing.T) {
				f := newSourceExportFixture(t, kind, function, true)
				if function {
					f.h.runtimeBaseStagingEnabled = true
					f.h.WithFunctionRunnerGo124("/test/runner")
				}
				f.builder.buildHook = func() {
					// Replace the original while the actual layer streams are still
					// consumed: only the private approved copy can supply their bytes.
					if err := os.WriteFile(f.dep.RootfsPath, []byte("replacement"), 0600); err != nil {
						t.Fatal(err)
					}
					assertSourceLayers(t, f.builder, function)
				}
				if err := f.h.consumeSourceBuild(t.Context(), f.app, f.dep, f.acct); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestApprovedSourceExportRefusesChangedOrRevokedInput(t *testing.T) {
	for _, mode := range []string{"changed archive", "revoked publisher", "different latest build", "cross account", "expired approval", "function legacy seam"} {
		t.Run(mode, func(t *testing.T) {
			f := newSourceExportFixture(t, state.DeploymentKindTarball, mode == "function legacy seam", true)
			switch mode {
			case "changed archive":
				body, err := os.ReadFile(f.dep.RootfsPath)
				if err != nil {
					t.Fatal(err)
				}
				body[len(body)-1] ^= 1
				if err := os.WriteFile(f.dep.RootfsPath, body, 0600); err != nil {
					t.Fatal(err)
				}
			case "revoked publisher":
				if err := f.store.DeleteAppTrustedSigner(t.Context(), f.app.AccountID, f.app.ID, "company"); err != nil {
					t.Fatal(err)
				}
			case "different latest build":
				b, err := f.store.CreateBuild(t.Context(), f.dep.ID, f.dep.Kind, 1, "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.ClaimQueuedBuild(t.Context(), b.ID); err != nil {
					t.Fatal(err)
				}
				// Removing require_signed must not turn retained, stale history
				// into an unsigned fallback for a replacement build.
				f.app.RequireSigned = false
			case "cross account":
				f.app.AccountID = uuid.NewString()
			case "expired approval":
				f.h.store = expiredSourceStore{f.store}
			}
			if err := f.h.consumeSourceBuild(t.Context(), f.app, f.dep, f.acct); err == nil {
				t.Fatal("unapproved source converted")
			}
			if len(f.builder.calls) != 0 {
				t.Fatal("conversion began before approval", len(f.builder.calls))
			}
		})
	}
}

type expiredSourceStore struct{ *state.MemStore }

func (s expiredSourceStore) GetFreshBuildExportPublication(context.Context, string, string, string, string) (state.BuildExportPublication, error) {
	return state.BuildExportPublication{}, state.ErrApplicationStandardRuntimeStale
}

func TestSourceExportChangesDuringConversionStopSnapshotHandoff(t *testing.T) {
	for _, mode := range []string{"late revocation", "late require_signed", "late enforce", "unsigned legacy"} {
		t.Run(mode, func(t *testing.T) {
			f := newSourceExportFixture(t, state.DeploymentKindTarball, false, mode == "late revocation")
			f.builder.buildHook = func() {
				var err error
				switch mode {
				case "late revocation":
					err = f.store.DeleteAppTrustedSigner(t.Context(), f.app.AccountID, f.app.ID, "company")
				case "late require_signed":
					required := true
					_, err = f.store.UpdateApp(t.Context(), f.app.ID, state.UpdateAppParams{RequireSigned: &required, SetRequireSigned: true})
				case "late enforce":
					policy := api.AppSecurityPolicyEnforce
					_, err = f.store.UpdateApp(t.Context(), f.app.ID, state.UpdateAppParams{SecurityPolicy: &policy, SetSecurityPolicy: true})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			err := f.h.handleSnapshotBoot(t.Context(), snapshotBootPayload{AppID: f.app.ID, DeploymentID: f.dep.ID})
			if mode == "unsigned legacy" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("late approval change reached handoff")
			}
			if len(f.builder.calls) != 1 {
				t.Fatal("late change did not happen during conversion", len(f.builder.calls))
			}
			for _, call := range f.h.notif.(*fakeNotifier).calls {
				if call.channel == db.NotifySnapshotPrime {
					t.Fatal("unapproved snapshot handoff emitted")
				}
			}
			dep, err := f.store.DeploymentByID(t.Context(), f.dep.ID)
			if err != nil || dep.Status != state.DeployFailed {
				t.Fatal("failed consumer not terminal", dep.Status, err)
			}
		})
	}
}

func TestRequiredSourceExportWithoutPublicationRefusesBeforeConversion(t *testing.T) {
	f := newSourceExportFixture(t, state.DeploymentKindTarball, false, false)
	f.app.RequireSigned = true
	if err := f.h.consumeSourceBuild(t.Context(), f.app, f.dep, f.acct); err == nil {
		t.Fatal("required publication missing but accepted")
	}
	if len(f.builder.calls) != 0 {
		t.Fatal("unsigned conversion began")
	}
	if err := f.h.recheckSourceApproval(t.Context(), f.app, f.dep, &state.BuildExportPublication{}); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatal("approval removal accepted", err)
	}
}

func TestApprovedSourceExportStillRequiresValidOCI(t *testing.T) {
	f := newSourceExportFixture(t, state.DeploymentKindTarball, false, false)
	body, err := os.ReadFile(f.dep.RootfsPath)
	if err != nil {
		t.Fatal(err)
	}
	copy(body, []byte("invalid tar header"))
	if err := os.WriteFile(f.dep.RootfsPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := buildpublisher.MeasureExport(t.Context(), f.dep.RootfsPath)
	if err != nil {
		t.Fatal(err)
	}
	build, err := f.store.BuildByDeployment(t.Context(), f.dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	publishSourceFixture(t, f.store, f.app, f.dep, build, digest, size)
	if approval, err := f.h.approvedSourceExport(t.Context(), f.app, f.dep); err != nil || approval == nil {
		t.Fatal("fixture lacks authentic current approval", approval, err)
	}
	if err := f.h.consumeSourceBuild(t.Context(), f.app, f.dep, f.acct); err == nil || !strings.Contains(err.Error(), "read OCI index") {
		t.Fatal("signature bypassed OCI validation", err)
	}
	if len(f.builder.calls) != 0 {
		t.Fatal("invalid signed archive reached conversion")
	}
}
