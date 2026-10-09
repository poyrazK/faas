package builderd

// adr: 739

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// Synthetic storage evidence only, never proof of a native acceptance run.
func builderQualificationFixture(target state.RuntimeRelease) state.RuntimeReleaseQualification {
	now := time.Now().UTC()
	return state.RuntimeReleaseQualification{ReleaseID: target.ID, Profile: state.RuntimeQualificationProfile, Architecture: target.Architecture,
		HostID: uuid.NewString(), KernelBootID: uuid.NewString(), SourceCommit: strings.Repeat("a", 40),
		KernelSHA256: strings.Repeat("b", 64), FirecrackerSHA256: strings.Repeat("c", 64), ReportSHA256: strings.Repeat("d", 64),
		TestMetalSHA256: strings.Repeat("e", 64), LeakcheckSHA256: strings.Repeat("f", 64),
		StartedAt: now.Add(-2 * time.Minute), CompletedAt: now.Add(-time.Minute)}
}

type builderQualificationReader struct {
	runtimeUpgradeTargetReader
	proof state.RuntimeReleaseQualification
	err   error
}

func (r builderQualificationReader) RuntimeReleaseQualification(context.Context, string) (state.RuntimeReleaseQualification, error) {
	return r.proof, r.err
}

func TestRuntimeUpgradeBuildRequiresNativeQualificationWithoutFallback(t *testing.T) {
	target := state.RuntimeRelease{Runtime: "node22", Architecture: runtime.GOARCH, SourceRef: "mirror.example/runtime@sha256:" + strings.Repeat("a", 64), GuestInitSHA256: strings.Repeat("b", 64), LayoutVersion: "v1", BaseSHA256: strings.Repeat("c", 64)}
	target.ID = target.Identity()
	proof := builderQualificationFixture(target)
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
			reader := builderQualificationReader{runtimeUpgradeTargetReader: runtimeUpgradeTargetReader{target: target}, proof: tc.proof, err: tc.err}
			reads := 0
			_, err := resolveDeploymentRuntimeBaseRef(t.Context(), reader, state.App{ID: "app", Type: state.AppTypeFunction, Runtime: target.Runtime}, state.Deployment{ID: "candidate", AppID: "app", SourceSHA256: strings.Repeat("d", 64)}, FrameworkNode, func(string) string { reads++; return "default" })
			if err == nil || reads != 0 {
				t.Fatal("unqualified target reached daemon defaults", err, reads)
			}
		})
	}
}
