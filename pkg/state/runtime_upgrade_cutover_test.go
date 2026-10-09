package state_test

// adr: 689

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// Qualification/readiness are synthetic fixtures, never native acceptance.
func runtimeUpgradeCutoverFixture(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) (state.App, state.Deployment, state.Deployment, state.RuntimeUpgradeCutoverRequest) {
	t.Helper()
	app, serving, candidate, p := runtimeUpgradeAcceptanceFixture(t, s, releases)
	if _, err := s.PublishOwnedInstanceRuntime(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
		t.Fatal("zero-traffic preparation", err)
	}
	r := state.RuntimeUpgradeCutoverRequest{AccountID: app.AccountID, AppID: app.ID, DeploymentID: candidate.ID,
		ExpectedServingID: serving.ID, ExpectedWakeID: p.WakeID, ExpectedTargetReleaseID: p.RuntimeUpgradeColdBoot.TargetReleaseID,
		ExpectedQualificationReportSHA256: strings.Repeat("d", 64)}
	return app, serving, candidate, r
}

func assertRuntimeUpgradeTraffic(t *testing.T, s state.Store, serving, candidate state.Deployment, applied bool) {
	t.Helper()
	a, err := s.DeploymentByID(t.Context(), serving.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.DeploymentByID(t.Context(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := 100
	if applied {
		want = 0
	}
	if a.TrafficPercent != want || b.TrafficPercent != 100-want || a.Status != state.DeployLive || b.Status != state.DeployLive {
		t.Fatal("partial/unexpected cutover", a.ID, a.Status, a.TrafficPercent, b.ID, b.Status, b.TrafficPercent)
	}
	_, err = s.(state.RuntimeUpgradeCutoverStore).DeploymentRuntimeUpgradeCutover(t.Context(), candidate.ID)
	if applied && err != nil || !applied && !errors.Is(err, state.ErrNotFound) {
		t.Fatal("traffic/receipt diverged", err)
	}
}

func TestRuntimeUpgradeCutoverAtomicAndHistoricalReplay(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		_, serving, candidate, r := runtimeUpgradeCutoverFixture(t, s, releases)
		cutovers := s.(state.RuntimeUpgradeCutoverStore)
		assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
		first, err := cutovers.CutoverDeploymentRuntimeUpgrade(t.Context(), r)
		if err != nil || first.WakeID != r.ExpectedWakeID || first.TargetReleaseID != r.ExpectedTargetReleaseID || first.CutoverAt.IsZero() {
			t.Fatal(first, err)
		}
		assertRuntimeUpgradeTraffic(t, s, serving, candidate, true)
		second, err := cutovers.CutoverDeploymentRuntimeUpgrade(t.Context(), r)
		if err != nil || first != second {
			t.Fatal("non-idempotent cutover", first, second, err)
		}
		bad := r
		bad.ExpectedWakeID = uuid.NewString()
		if _, err := cutovers.CutoverDeploymentRuntimeUpgrade(t.Context(), bad); !errors.Is(err, state.ErrConflict) {
			t.Fatal("changed retry adopted history", err)
		}
		// Existing rollback can return to the retained serving artifact.
		id, err := s.AutoRollbackDeploymentsTx(t.Context(), r.AppID, candidate.ID)
		if err != nil || id != serving.ID {
			t.Fatal("retained baseline was not rollback eligible", id, err)
		}
		if err := s.(state.RuntimeReleaseQualificationStore).RevokeRuntimeReleaseQualification(t.Context(), r.ExpectedTargetReleaseID,
			r.ExpectedQualificationReportSHA256, strings.Repeat("1", 64)); err != nil {
			t.Fatal(err)
		}
		second, err = cutovers.CutoverDeploymentRuntimeUpgrade(t.Context(), r)
		if err != nil || first != second {
			t.Fatal("history depended on current qualification", second, err)
		}
		rolledBack, err := s.DeploymentByID(t.Context(), serving.ID)
		oldCandidate, candidateErr := s.DeploymentByID(t.Context(), candidate.ID)
		if err != nil || candidateErr != nil || rolledBack.TrafficPercent != 100 || oldCandidate.TrafficPercent != 0 {
			t.Fatal("retry reactivated rolled-back candidate", rolledBack, oldCandidate, err, candidateErr)
		}
	})
}

func TestRuntimeUpgradeCutoverRejectsUnreviewedIntent(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		for _, tc := range []struct {
			name string
			edit func(*state.RuntimeUpgradeCutoverRequest)
		}{
			{"account", func(r *state.RuntimeUpgradeCutoverRequest) { r.AccountID = uuid.NewString() }},
			{"app", func(r *state.RuntimeUpgradeCutoverRequest) { r.AppID = uuid.NewString() }},
			{"serving", func(r *state.RuntimeUpgradeCutoverRequest) { r.ExpectedServingID = uuid.NewString() }},
			{"wake", func(r *state.RuntimeUpgradeCutoverRequest) { r.ExpectedWakeID = uuid.NewString() }},
			{"target", func(r *state.RuntimeUpgradeCutoverRequest) { r.ExpectedTargetReleaseID = strings.Repeat("1", 64) }},
			{"qualification", func(r *state.RuntimeUpgradeCutoverRequest) {
				r.ExpectedQualificationReportSHA256 = strings.Repeat("1", 64)
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, serving, candidate, r := runtimeUpgradeCutoverFixture(t, s, releases)
				tc.edit(&r)
				if _, err := s.(state.RuntimeUpgradeCutoverStore).CutoverDeploymentRuntimeUpgrade(t.Context(), r); !errors.Is(err, state.ErrConflict) {
					t.Fatal("unreviewed cutover", err)
				}
				assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
			})
		}
	})
}

func TestRuntimeUpgradeCutoverRejectsDrift(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		for _, tc := range []struct {
			name string
			edit func(context.Context, state.Store, state.App, state.Deployment, state.Deployment, state.RuntimeUpgradeCutoverRequest) error
		}{
			{"environment", func(ctx context.Context, s state.Store, a state.App, _, _ state.Deployment, _ state.RuntimeUpgradeCutoverRequest) error {
				return s.UpsertAppEnv(ctx, a.AccountID, a.ID, "REGION", "new-region")
			}},
			{"secret version", func(ctx context.Context, s state.Store, a state.App, _, _ state.Deployment, _ state.RuntimeUpgradeCutoverRequest) error {
				return s.UpsertAppSecret(ctx, a.AccountID, a.ID, "TOKEN", []byte("new-sealed-fixture"))
			}},
			{"serving layer", func(ctx context.Context, s state.Store, _ state.App, serving, _ state.Deployment, _ state.RuntimeUpgradeCutoverRequest) error {
				return s.SetDeploymentRootfs(ctx, serving.ID, "/changed", "changed-serving.ext4", 30)
			}},
			{"candidate layer", func(ctx context.Context, s state.Store, _ state.App, _, candidate state.Deployment, _ state.RuntimeUpgradeCutoverRequest) error {
				return s.SetDeploymentRootfs(ctx, candidate.ID, "/changed", "changed-candidate.ext4", 30)
			}},
			{"unfinished pipeline", func(ctx context.Context, s state.Store, _ state.App, _, candidate state.Deployment, _ state.RuntimeUpgradeCutoverRequest) error {
				return s.UpdateDeploymentStatus(ctx, candidate.ID, state.DeploySnapshotting, "")
			}},
			{"revocation", func(ctx context.Context, s state.Store, _ state.App, _, _ state.Deployment, r state.RuntimeUpgradeCutoverRequest) error {
				return s.(state.RuntimeReleaseQualificationStore).RevokeRuntimeReleaseQualification(ctx, r.ExpectedTargetReleaseID,
					r.ExpectedQualificationReportSHA256, strings.Repeat("1", 64))
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				app, serving, candidate, r := runtimeUpgradeCutoverFixture(t, s, releases)
				if err := tc.edit(t.Context(), s, app, serving, candidate, r); err != nil {
					t.Fatal(err)
				}
				if _, err := s.(state.RuntimeUpgradeCutoverStore).CutoverDeploymentRuntimeUpgrade(t.Context(), r); !errors.Is(err, state.ErrConflict) {
					t.Fatal("stale cutover committed", err)
				}
				current, err := s.DeploymentByID(t.Context(), serving.ID)
				dark, darkErr := s.DeploymentByID(t.Context(), candidate.ID)
				if err != nil || darkErr != nil || current.TrafficPercent != 100 || dark.TrafficPercent != 0 {
					t.Fatal("rejected cutover changed traffic", current, dark, err, darkErr)
				}
				if _, err := s.(state.RuntimeUpgradeCutoverStore).DeploymentRuntimeUpgradeCutover(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("rejected cutover retained authorization", err)
				}
			})
		}
	})
}

func TestRuntimeUpgradeCutoverTrafficWritersCannotBypass(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		_, serving, candidate, _ := runtimeUpgradeCutoverFixture(t, s, releases)
		for _, tc := range []struct {
			name string
			id   string
			pct  int
		}{
			{"direct promotion", candidate.ID, 100},
			{"direct split", candidate.ID, 25},
			{"indirect redistribution", serving.ID, 0},
			{"indirect split", serving.ID, 75},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := s.UpdateDeploymentTraffic(t.Context(), tc.id, tc.pct); !errors.Is(err, state.ErrConflict) {
					t.Fatal("generic traffic bypass", err)
				}
				assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
			})
		}

	})
}

func TestRuntimeUpgradeCutoverMissingReadinessAndFailureFallback(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		app, serving, candidate, p := runtimeUpgradeAcceptanceFixture(t, s, releases)
		if err := s.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
			t.Fatal(err)
		}
		r := state.RuntimeUpgradeCutoverRequest{AccountID: app.AccountID, AppID: app.ID, DeploymentID: candidate.ID,
			ExpectedServingID: serving.ID, ExpectedWakeID: p.WakeID, ExpectedTargetReleaseID: p.RuntimeUpgradeColdBoot.TargetReleaseID,
			ExpectedQualificationReportSHA256: strings.Repeat("d", 64)}
		if _, err := s.(state.RuntimeUpgradeCutoverStore).CutoverDeploymentRuntimeUpgrade(t.Context(), r); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cutover invented readiness", err)
		}
		assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
		if err := s.UpdateDeploymentStatus(t.Context(), serving.ID, state.DeployFailed, "serving failed"); err != nil {
			t.Fatal("failure could not commit safely", err)
		}
		dark, err := s.DeploymentByID(t.Context(), candidate.ID)
		if err != nil || dark.TrafficPercent != 0 {
			t.Fatal("failure activated unapproved upgrade", dark, err)
		}
	})
}

func TestRuntimeUpgradeCutoverConcurrentReplay(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		_, serving, candidate, r := runtimeUpgradeCutoverFixture(t, s, releases)
		start := make(chan struct{})
		var wg sync.WaitGroup
		results := make([]state.RuntimeUpgradeCutover, 2)
		errs := make([]error, 2)
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				results[i], errs[i] = s.(state.RuntimeUpgradeCutoverStore).CutoverDeploymentRuntimeUpgrade(t.Context(), r)
			}(i)
		}
		close(start)
		wg.Wait()
		if errs[0] != nil || errs[1] != nil || results[0] != results[1] {
			t.Fatal("concurrent replay diverged", results, errs)
		}
		assertRuntimeUpgradeTraffic(t, s, serving, candidate, true)
	})
}

func TestMemRuntimeUpgradeCutoverCanaryAndRecoveryCannotBypass(t *testing.T) {
	s := state.NewMemStore()
	_, serving, candidate, _ := runtimeUpgradeCutoverFixture(t, s, s)
	// Turning the candidate into a legacy canary cannot authorize it.
	if err := s.SetDeploymentCanaryState(t.Context(), candidate.ID, "custom", 0, 2, candidate.CreatedAt, "pending"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdvanceCanary(t.Context(), candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 100}); err == nil {
		t.Fatal("canary bypass")
	}
	if _, _, err := s.RecoverRollout(t.Context(), candidate.AppID, "promote", "test bypass"); err == nil {
		t.Fatal("recovery promotion bypass")
	}
	assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
	if err := s.SetDeploymentCanaryState(t.Context(), candidate.ID, "none", 0, 0, candidate.CreatedAt, "rolling_out"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinalizeServiceRollout(t.Context(), candidate.ID); err == nil {
		t.Fatal("service finalizer bypass")
	}
	assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
}

func TestRuntimeUpgradeCutoverMarkLiveCannotActivatePositivePreparation(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		_, serving, candidate := runtimeUpgradeBaselineFixture(t, s, releases, func(d *state.Deployment) { d.TrafficPercent = 25 })
		for _, activate := range []func(context.Context, string) error{
			s.MarkDeploymentLive,
			func(ctx context.Context, id string) error {
				return s.UpdateDeploymentStatus(ctx, id, state.DeployLive, "")
			},
		} {
			assertRuntimeUpgradeWriterFence(t, activate(t.Context(), candidate.ID))
			old, err := s.DeploymentByID(t.Context(), serving.ID)
			dark, darkErr := s.DeploymentByID(t.Context(), candidate.ID)
			if err != nil || darkErr != nil || old.TrafficPercent != 100 || old.Status != state.DeployLive || dark.Status != state.DeployPending {
				t.Fatal("failed activation partially mutated deployment", old, dark, err, darkErr)
			}
		}
	})
}
