package imaged

// adr: 597

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type failedRuntimeUpgradeStore struct {
	*state.MemStore
}

func (s failedRuntimeUpgradeStore) DeploymentRuntimeUpgradeTarget(context.Context, string) (state.RuntimeRelease, error) {
	return state.RuntimeRelease{}, errors.New("target database unavailable")
}

func TestRuntimeUpgradeImageUsesExactTargetAcrossGuestInitChange(t *testing.T) {
	b := &fakeBuilder{}
	f := newBaseHarness(t, newTwoLayerPuller(t), b)
	s := state.NewMemStore()
	f.h.store = s
	guest := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(guest, []byte("old guest-init"), 0600); err != nil {
		t.Fatal(err)
	}
	f.h.guestInitPath = guest
	ref := "ghcr.io/test/selected@sha256:" + strings.Repeat("1", 64)
	target, err := f.h.ensureRuntimeRelease(t.Context(), s, "node22", runtime.GOARCH, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordRuntimeReleaseQualification(t.Context(), imageQualificationFixture(target)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guest, []byte("new guest-init"), 0600); err != nil {
		t.Fatal(err)
	}
	newDefault, err := f.h.ensureRuntimeRelease(t.Context(), s, "node22", runtime.GOARCH, ref)
	if err != nil || newDefault.ID == target.ID {
		t.Fatal("fixture did not change guest-init generation", newDefault, err)
	}
	acct, err := s.CreateAccount(t.Context(), "exact-runtime@test.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "exact-runtime", Type: state.AppTypeFunction, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourceSHA256: strings.Repeat("c", 64), SourceBytes: 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PinDeploymentRuntimeUpgradeTarget(t.Context(), dep.ID, target.ID, dep.SourceSHA256); err != nil {
		t.Fatal(err)
	}
	key := "apps/exact-runtime/" + dep.ID + ".ext4"
	if _, err := f.h.prepareFunctionRuntimeRelease(t.Context(), app, dep, app.Runtime, key); err == nil {
		t.Fatal("missing build became legacy unpinned imaging")
	}
	build, err := s.CreateBuild(t.Context(), dep.ID, dep.Kind, dep.SourceBytes, "")
	if err != nil {
		t.Fatal(err)
	}
	provenance := state.BuildProvenance{BuildID: build.ID, SourceSHA256: dep.SourceSHA256, RuntimeBaseRef: ref}
	if err := s.CreateBuildProvenance(t.Context(), provenance); err != nil {
		t.Fatal(err)
	}
	// Both the installed guest-init and daemon default have changed. An
	// update must still use the chosen release's exact bytes and scan record.
	f.h.guestInitPath = filepath.Join(t.TempDir(), "not-installed-here")
	f.h.deployBaseRefOverride = "ghcr.io/test/default@sha256:" + strings.Repeat("2", 64)
	got, err := f.h.prepareFunctionRuntimeRelease(t.Context(), app, dep, app.Runtime, key)
	if err != nil || got == nil || got.ID != target.ID || len(b.calls) != 2 {
		t.Fatal("replaced selected generation", got, err, len(b.calls))
	}
	if err := s.SetDeploymentRootfs(t.Context(), dep.ID, "/artifact", key, 20); err != nil {
		t.Fatal(err)
	}
	if err := s.BindDeploymentRuntimeRelease(t.Context(), dep.ID, key, target.ID); err != nil {
		t.Fatal(err)
	}
	f.h.runtimeBaseStagingEnabled = true
	if err := f.h.ensureDeploymentRuntimeBaseForDeployment(t.Context(), app, dep); err != nil || len(b.calls) != 2 {
		t.Fatal("staged unrelated mutable default", err, len(b.calls))
	}
	if _, err := f.h.prepareFunctionRuntimeRelease(t.Context(), app, dep, app.Runtime, key); err != nil {
		t.Fatal("imaging retry lost selected target", err)
	}
	for _, tc := range []struct{ name, ref, source string }{
		{"missing recorded ref", "", dep.SourceSHA256},
		{"wrong runtime ref", f.h.deployBaseRefOverride, dep.SourceSHA256},
		{"wrong source bytes", ref, strings.Repeat("d", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := provenance
			p.RuntimeBaseRef, p.SourceSHA256 = tc.ref, tc.source
			if err := s.CreateBuildProvenance(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			if _, err := f.h.prepareFunctionRuntimeRelease(t.Context(), app, dep, app.Runtime, key); err == nil {
				t.Fatal("accepted mismatched build evidence")
			}
		})
	}
	if err := s.CreateBuildProvenance(t.Context(), provenance); err != nil {
		t.Fatal(err)
	}
	scanKey := wire.ScanKeyForBaseKey(target.BaseKey())
	scan, err := f.be.Get(t.Context(), scanKey)
	if err != nil {
		t.Fatal(err)
	}
	scanBytes, err := io.ReadAll(scan)
	_ = scan.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []string{"", "{}", strings.Repeat("x", api.RuntimeReleaseSidecarMaxBytes+1), `{"image":"wrong","findings":{},"fix_available_findings":{},"scanned_at":"2026-10-05T00:00:00Z"}`} {
		if evidence == "" {
			if err := f.be.Delete(t.Context(), scanKey); err != nil {
				t.Fatal(err)
			}
		} else if err := f.be.Put(t.Context(), scanKey, strings.NewReader(evidence)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.h.prepareFunctionRuntimeRelease(t.Context(), app, dep, app.Runtime, key); err == nil {
			t.Fatal("accepted missing or invalid scan evidence")
		}
	}
	if err := f.be.Put(t.Context(), scanKey, strings.NewReader(string(scanBytes))); err != nil {
		t.Fatal(err)
	}
	if err := f.be.Put(t.Context(), target.BaseKey(), strings.NewReader("changed base bytes")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.prepareFunctionRuntimeRelease(t.Context(), app, dep, app.Runtime, key); err == nil {
		t.Fatal("repaired corrupt target with another generation")
	}
	f.h.store = failedRuntimeUpgradeStore{MemStore: s}
	if _, err := f.h.prepareFunctionRuntimeRelease(t.Context(), app, dep, app.Runtime, key); err == nil {
		t.Fatal("database outage became unpinned fallback")
	}
}
