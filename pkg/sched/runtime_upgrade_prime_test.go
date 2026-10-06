package sched

// adr: 602

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedRuntimeUpgradePrime(t *testing.T, mode string) (*state.MemStore, state.App, state.Deployment, state.RuntimeRelease) {
	t.Helper()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(t.Context(), "prime-upgrade@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	input := state.App{AccountID: acct.ID, Slug: "upgrade-prime", Type: state.AppTypeFunction, Runtime: "node22", RAMMB: 128, MaxConcurrency: 2, IdleTimeoutS: 60}
	input.Manifest.ExecutionMode = mode
	app, err := s.CreateApp(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	current := state.RuntimeRelease{Runtime: "node22", Architecture: runtime.GOARCH, SourceRef: "ghcr.io/test/node@sha256:" + strings.Repeat("1", 64),
		GuestInitSHA256: strings.Repeat("2", 64), LayoutVersion: "test-layout", BaseSHA256: strings.Repeat("3", 64)}
	current.ID = current.Identity()
	current, err = s.PublishRuntimeRelease(t.Context(), current)
	if err != nil {
		t.Fatal(err)
	}
	serving, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball,
		SourceSHA256: strings.Repeat("c", 64), SourceBytes: 20, Handler: "index.handler"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(t.Context(), serving.ID, "/serving", "apps/upgrade-prime/serving.ext4", 20); err != nil {
		t.Fatal(err)
	}
	if err := s.BindDeploymentRuntimeRelease(t.Context(), serving.ID, "apps/upgrade-prime/serving.ext4", current.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(t.Context(), serving.ID); err != nil {
		t.Fatal(err)
	}
	candidate, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: serving.Kind, SourceSHA256: serving.SourceSHA256,
		SourceBytes: serving.SourceBytes, Handler: serving.Handler, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	target := current
	target.SourceRef = "ghcr.io/test/node@sha256:" + strings.Repeat("4", 64)
	target.ID = target.Identity()
	target, err = s.PublishRuntimeRelease(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PinDeploymentRuntimeUpgradeTarget(t.Context(), candidate.ID, target.ID, candidate.SourceSHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureDeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID, serving.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	// Synthetic fixture only; this never qualifies a release on native hardware.
	if _, err := s.RecordRuntimeReleaseQualification(t.Context(), state.RuntimeReleaseQualification{ReleaseID: target.ID, Profile: state.RuntimeQualificationProfile,
		Architecture: target.Architecture, HostID: uuid.NewString(), KernelBootID: uuid.NewString(), SourceCommit: strings.Repeat("a", 40),
		KernelSHA256: strings.Repeat("b", 64), FirecrackerSHA256: strings.Repeat("c", 64), ReportSHA256: strings.Repeat("d", 64),
		TestMetalSHA256: strings.Repeat("e", 64), LeakcheckSHA256: strings.Repeat("f", 64), StartedAt: now.Add(-2 * time.Minute), CompletedAt: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(t.Context(), candidate.ID, "/candidate", "apps/upgrade-prime/candidate.ext4", 20); err != nil {
		t.Fatal(err)
	}
	if err := s.BindDeploymentRuntimeRelease(t.Context(), candidate.ID, "apps/upgrade-prime/candidate.ext4", target.ID); err != nil {
		t.Fatal(err)
	}
	candidate, err = s.DeploymentByID(t.Context(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, app, candidate, target
}

type upgradeAcceptanceNotifier struct {
	fakeNotifier
	t            *testing.T
	store        *state.MemStore
	deploymentID string
}

func (n *upgradeAcceptanceNotifier) Notify(ctx context.Context, channel, payload string) error {
	if channel == db.NotifySnapshotWritten || channel == db.NotifyDeploymentReady {
		if _, err := n.store.DeploymentRuntimeUpgradeAcceptance(ctx, n.deploymentID); err != nil {
			n.t.Errorf("activation notified before candidate acceptance: %v", err)
		}
	}
	return n.fakeNotifier.Notify(ctx, channel, payload)
}

func TestRuntimeUpgradePrimeRecordsBeforeNotification(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeRequest, api.ExecutionModeWorker} {
		t.Run(mode, func(t *testing.T) {
			s, app, candidate, target := seedRuntimeUpgradePrime(t, mode)
			vmm := &fakeVMM{}
			notif := &upgradeAcceptanceNotifier{t: t, store: s, deploymentID: candidate.ID}
			e := newEngine(t, s, vmm, notif, "1.10.0")
			if err := e.Prime(t.Context(), app.ID, candidate.ID); err != nil {
				t.Fatal(err)
			}
			a, err := s.DeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID)
			if err != nil || a.RootfsKey != candidate.RootfsKey || a.TargetReleaseID != target.ID || vmm.coldBoots != 1 || vmm.restores != 0 ||
				vmm.lastColdBootSpec.BaseKey != target.BaseKey() {
				t.Fatal(a, err, vmm.coldBoots, vmm.restores)
			}
			if notif.count(db.NotifySnapshotWritten)+notif.count(db.NotifyDeploymentReady) != 1 {
				t.Fatal("missing candidate handoff")
			}
			if err := s.ValidateDeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID); err != nil {
				t.Fatal(err)
			}
			if err := e.Prime(t.Context(), app.ID, candidate.ID); !errors.Is(err, state.ErrConflict) {
				t.Fatal("same candidate refreshed acceptance", err)
			}
			if vmm.coldBoots != 1 {
				t.Fatal("replay booted another VM")
			}
		})
	}
}

type changedUpgradeOutcomeVMM struct {
	fakeVMM
	change func(*WakeOutcome) *WakeOutcome
}

func (v *changedUpgradeOutcomeVMM) CreateColdBoot(ctx context.Context, node, id string, spec AppSpec) (*WakeOutcome, error) {
	out, err := v.fakeVMM.CreateColdBoot(ctx, node, id, spec)
	if err != nil {
		return nil, err
	}
	return v.change(out), nil
}

func TestRuntimeUpgradePrimeRejectsNonColdBootResponse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*WakeOutcome) *WakeOutcome
	}{
		{"nil", func(*WakeOutcome) *WakeOutcome { return nil }},
		{"foreign instance", func(out *WakeOutcome) *WakeOutcome { out.Instance = uuid.NewString(); return out }},
		{"restore", func(out *WakeOutcome) *WakeOutcome { out.Method = vmmdpb.WakeMethod_WAKE_RESTORE; return out }},
		{"unknown", func(out *WakeOutcome) *WakeOutcome { out.Method = vmmdpb.WakeMethod_WAKE_UNKNOWN; return out }},
		{"restore fallback", func(out *WakeOutcome) *WakeOutcome { out.RestoreFallbackReason = "snapshot_stale"; return out }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, app, candidate, _ := seedRuntimeUpgradePrime(t, api.ExecutionModeRequest)
			vmm := &changedUpgradeOutcomeVMM{change: tc.change}
			notif := &fakeNotifier{}
			e := newEngine(t, s, vmm, notif, "1.10.0")
			if err := e.Prime(t.Context(), app.ID, candidate.ID); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unproven boot accepted", err)
			}
			if _, err := s.DeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("invalid boot left receipt", err)
			}
			if vmm.destroys != 1 || notif.count(db.NotifySnapshotWritten)+notif.count(db.NotifyDeploymentReady) != 0 {
				t.Fatal("invalid boot escaped cleanup or notified activation")
			}
		})
	}
}

func TestRuntimeUpgradePrimeRejectsRevocationAndDrift(t *testing.T) {
	for _, during := range []bool{false, true} {
		for _, revoked := range []bool{false, true} {
			name := "before-admission/configuration"
			if during {
				name = "during-boot/configuration"
			}
			if revoked {
				name += "/revocation"
			}
			t.Run(name, func(t *testing.T) {
				s, app, candidate, target := seedRuntimeUpgradePrime(t, api.ExecutionModeRequest)
				change := func() {
					var err error
					if revoked {
						err = s.RevokeRuntimeReleaseQualification(t.Context(), target.ID, strings.Repeat("d", 64), strings.Repeat("1", 64))
					} else {
						err = s.UpsertAppEnv(t.Context(), app.AccountID, app.ID, "REGION", "changed")
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				vmm := &fakeVMM{}
				if during {
					vmm.coldBootHook = change
				} else {
					change()
				}
				notif := &fakeNotifier{}
				e := newEngine(t, s, vmm, notif, "1.10.0")
				if err := e.Prime(t.Context(), app.ID, candidate.ID); !errors.Is(err, state.ErrConflict) {
					t.Fatal("stale upgrade primed", err)
				}
				if _, err := s.DeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("stale boot left receipt", err)
				}
				want := 0
				if during {
					want = 1
				}
				if vmm.coldBoots != want || vmm.destroys != want || notif.count(db.NotifySnapshotWritten)+notif.count(db.NotifyDeploymentReady) != 0 {
					t.Fatal("stale boot admitted or escaped cleanup")
				}
			})
		}
	}
}

func TestRuntimeUpgradePrimeRejectsJobArtifactOnlyAcceptance(t *testing.T) {
	s, app, candidate, _ := seedRuntimeUpgradePrime(t, api.ExecutionModeJob)
	vmm, notif := &fakeVMM{}, &fakeNotifier{}
	e := newEngine(t, s, vmm, notif, "1.10.0")
	if err := e.Prime(t.Context(), app.ID, candidate.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("job artifact qualified cold boot", err)
	}
	if vmm.coldBoots != 0 || notif.count(db.NotifyDeploymentReady) != 0 {
		t.Fatal("job upgrade admitted or notified")
	}
}
