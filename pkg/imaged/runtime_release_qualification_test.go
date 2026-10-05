package imaged

// adr: 599

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Synthetic storage evidence only, never proof of a native acceptance run.
func imageQualificationFixture(target state.RuntimeRelease) state.RuntimeReleaseQualification {
	now := time.Now().UTC()
	return state.RuntimeReleaseQualification{ReleaseID: target.ID, Profile: state.RuntimeQualificationProfile, Architecture: target.Architecture,
		HostID: uuid.NewString(), KernelBootID: uuid.NewString(), SourceCommit: strings.Repeat("a", 40),
		KernelSHA256: strings.Repeat("b", 64), FirecrackerSHA256: strings.Repeat("c", 64), ReportSHA256: strings.Repeat("d", 64),
		TestMetalSHA256: strings.Repeat("e", 64), LeakcheckSHA256: strings.Repeat("f", 64),
		StartedAt: now.Add(-2 * time.Minute), CompletedAt: now.Add(-time.Minute)}
}

type imageQualificationReader struct {
	*state.MemStore
	proof state.RuntimeReleaseQualification
	err   error
}

func (r imageQualificationReader) RuntimeReleaseQualification(context.Context, string) (state.RuntimeReleaseQualification, error) {
	return r.proof, r.err
}

func TestRuntimeUpgradeImageRequiresNativeQualificationBeforeMaterialization(t *testing.T) {
	s := state.NewMemStore()
	acct, err := s.CreateAccount(t.Context(), "image-qualification@test.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "image-qualification", Type: state.AppTypeFunction, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourceSHA256: strings.Repeat("a", 64), SourceBytes: 20})
	if err != nil {
		t.Fatal(err)
	}
	target := state.RuntimeRelease{Runtime: app.Runtime, Architecture: runtime.GOARCH, SourceRef: "mirror.example/runtime@sha256:" + strings.Repeat("a", 64), GuestInitSHA256: strings.Repeat("b", 64), LayoutVersion: "v1", BaseSHA256: strings.Repeat("c", 64)}
	target.ID = target.Identity()
	target, err = s.PublishRuntimeRelease(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PinDeploymentRuntimeUpgradeTarget(t.Context(), dep.ID, target.ID, dep.SourceSHA256); err != nil {
		t.Fatal(err)
	}
	proof := imageQualificationFixture(target)
	proof.RecordedAt = time.Now().UTC()
	revoked := proof
	revoked.RevokedAt = time.Now().UTC()
	revoked.RevocationSHA256 = strings.Repeat("1", 64)
	wrong := proof
	wrong.ReleaseID = strings.Repeat("2", 64)
	for _, tc := range []struct {
		name  string
		proof state.RuntimeReleaseQualification
		err   error
	}{
		{"missing", state.RuntimeReleaseQualification{}, state.ErrNotFound},
		{"outage", state.RuntimeReleaseQualification{}, errors.New("qualification database unavailable")},
		{"revoked", revoked, nil},
		{"wrong target", wrong, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{store: imageQualificationReader{MemStore: s, proof: tc.proof, err: tc.err}}
			// No storage/puller/builder is installed: refusal must precede them.
			if _, err := h.prepareFunctionRuntimeRelease(t.Context(), app, dep, app.Runtime, "candidate.ext4"); err == nil {
				t.Fatal("unqualified target reached imaging")
			}
			if err := h.ensureDeploymentRuntimeBaseForDeployment(t.Context(), app, dep); err == nil {
				t.Fatal("unqualified target reached final preparation")
			}
		})
	}
}
