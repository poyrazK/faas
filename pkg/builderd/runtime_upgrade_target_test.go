package builderd

// adr: 737

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeUpgradeTargetReader struct {
	state.RuntimeUpgradeTargetStore
	target state.RuntimeRelease
	err    error
}

func TestRuntimeUpgradeBuildPersistsChosenRuntimeAndSource(t *testing.T) {
	s := state.NewMemStore()
	acct, err := s.CreateAccount(t.Context(), "pinned-build@test.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "pinned-build", Type: state.AppTypeFunction, Runtime: "node22", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, source, []string{"handler.js"})
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	sha := sha256.Sum256(raw)
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: source, SourceSHA256: hex.EncodeToString(sha[:]), SourceBytes: int64(len(raw)), LogPath: filepath.Join(t.TempDir(), "build.log")})
	if err != nil {
		t.Fatal(err)
	}
	target := state.RuntimeRelease{Runtime: app.Runtime, Architecture: runtime.GOARCH, SourceRef: "mirror.example/chosen@sha256:" + strings.Repeat("a", 64), GuestInitSHA256: strings.Repeat("b", 64), LayoutVersion: "v1", BaseSHA256: strings.Repeat("c", 64)}
	target.ID = target.Identity()
	target, err = s.PublishRuntimeRelease(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordRuntimeReleaseQualification(t.Context(), builderQualificationFixture(target)); err != nil {
		t.Fatal(err)
	}
	if err := s.PinDeploymentRuntimeUpgradeTarget(t.Context(), dep.ID, target.ID, dep.SourceSHA256); err != nil {
		t.Fatal(err)
	}
	build, err := s.CreateBuild(t.Context(), dep.ID, dep.Kind, dep.SourceBytes, dep.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	layer := filepath.Join(t.TempDir(), "export.tar")
	if err := os.WriteFile(layer, []byte("built OCI export"), 0600); err != nil {
		t.Fatal(err)
	}
	vm := &fakeVM{out: BuildOutcome{OCIImage: layer}}
	b := New(s, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Setenv("FAAS_DEPLOY_BASE_REF_NODE22", "mirror.example/new-default@sha256:"+strings.Repeat("d", 64))
	if _, err := b.ProcessOne(t.Context(), build.ID); err != nil {
		t.Fatal(err)
	}
	if vm.lastRequest.RuntimeBaseRef != target.SourceRef {
		t.Fatal("VM built from a daemon default", vm.lastRequest.RuntimeBaseRef)
	}
	provenance, err := s.BuildProvenanceByBuildID(t.Context(), build.ID)
	if err != nil || provenance.RuntimeBaseRef != target.SourceRef || provenance.SourceSHA256 != dep.SourceSHA256 {
		t.Fatal("lost exact build input", provenance, err)
	}
}

func (r runtimeUpgradeTargetReader) DeploymentRuntimeUpgradeTarget(context.Context, string) (state.RuntimeRelease, error) {
	return r.target, r.err
}

func (r runtimeUpgradeTargetReader) RuntimeReleaseQualification(context.Context, string) (state.RuntimeReleaseQualification, error) {
	q := builderQualificationFixture(r.target)
	q.RecordedAt = time.Now().UTC()
	return q, nil
}

func TestRuntimeUpgradeBuildUsesTargetAndRejectsLookupFailures(t *testing.T) {
	target := state.RuntimeRelease{Runtime: "node22", Architecture: runtime.GOARCH, SourceRef: "mirror.example/exact@sha256:" + strings.Repeat("a", 64), GuestInitSHA256: strings.Repeat("b", 64), LayoutVersion: "v1", BaseSHA256: strings.Repeat("c", 64)}
	target.ID = target.Identity()
	app := state.App{ID: "app", Type: state.AppTypeFunction, Runtime: "node22"}
	dep := state.Deployment{ID: "candidate", AppID: app.ID, SourceSHA256: strings.Repeat("d", 64)}
	envReads := 0
	env := func(string) string { envReads++; return "new-daemon-default:latest" }
	got, err := resolveDeploymentRuntimeBaseRef(t.Context(), runtimeUpgradeTargetReader{target: target}, app, dep, FrameworkNode, env)
	if err != nil || got != target.SourceRef || envReads != 0 {
		t.Fatal("daemon changed selected target", got, err, envReads)
	}
	for _, tc := range []struct {
		name   string
		target state.RuntimeRelease
		err    error
		fw     Framework
	}{
		{"database outage", target, errors.New("database unavailable"), FrameworkNode},
		{"invalid identity", state.RuntimeRelease{}, nil, FrameworkNode},
		{"Dockerfile", target, nil, FrameworkDocker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := resolveDeploymentRuntimeBaseRef(t.Context(), runtimeUpgradeTargetReader{target: tc.target, err: tc.err}, app, dep, tc.fw, env); err == nil || envReads != 0 {
				t.Fatal("fell back to operator defaults", err, envReads)
			}
		})
	}
	wrong := target
	wrong.Runtime = "node24"
	wrong.ID = wrong.Identity()
	if _, err := resolveDeploymentRuntimeBaseRef(t.Context(), runtimeUpgradeTargetReader{target: wrong}, app, dep, FrameworkNode, env); !errors.Is(err, state.ErrConflict) {
		t.Fatal("runtime family change accepted", err)
	}
	if runtime.GOARCH == "amd64" {
		wrong = target
		wrong.Architecture = "arm64"
	} else {
		wrong = target
		wrong.Architecture = "amd64"
	}
	wrong.ID = wrong.Identity()
	if _, err := resolveDeploymentRuntimeBaseRef(t.Context(), runtimeUpgradeTargetReader{target: wrong}, app, dep, FrameworkNode, env); !errors.Is(err, state.ErrConflict) {
		t.Fatal("wrong build architecture accepted", err)
	}
}

func TestRuntimeUpgradeBuildUnpinnedRetainsDefaultSelection(t *testing.T) {
	want := "mirror.example/default@sha256:" + strings.Repeat("c", 64)
	env := func(key string) string {
		if key == "FAAS_DEPLOY_BASE_REF_NODE22" {
			return want
		}
		return ""
	}
	got, err := resolveDeploymentRuntimeBaseRef(t.Context(), runtimeUpgradeTargetReader{err: state.ErrNotFound}, state.App{Runtime: "node22"}, state.Deployment{}, FrameworkNode, env)
	if err != nil || got != want {
		t.Fatal("changed ordinary deployment behavior", got, err)
	}
}
