package state_test

// adr: 690

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runtimeupgrade"
	"github.com/onebox-faas/faas/pkg/state"
)

// Source, build and native readiness observations here are synthetic fixtures.
func runtimeUpgradeOperationFixture(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) (state.App, state.Deployment, state.Deployment, state.RuntimeUpgradeOperationRequest) {
	t.Helper()
	sha := strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
	app, serving, old := runtimeUpgradeBaselineFixture(t, s, acceptanceFixtureReleases{releases, sha})
	target, err := s.(state.RuntimeUpgradeTargetStore).DeploymentRuntimeUpgradeTarget(t.Context(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.(state.RuntimeReleaseQualificationStore).RecordRuntimeReleaseQualification(t.Context(), runtimeQualificationFixture(target)); err != nil {
		t.Fatal(err)
	}
	candidate, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: old.Kind, SourcePath: "/tmp/retained-source.tar.gz", SourceSHA256: old.SourceSHA256, SourceBytes: old.SourceBytes, SourceRoot: old.SourceRoot, Handler: old.Handler, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	r := state.RuntimeUpgradeOperationRequest{ID: uuid.NewString(), AccountID: app.AccountID, AppID: app.ID, DeploymentID: candidate.ID, ServingDeploymentID: serving.ID, TargetReleaseID: target.ID, SourceSHA256: candidate.SourceSHA256, QualificationReportSHA256: strings.Repeat("d", 64)}
	return app, serving, candidate, r
}

func runtimeUpgradeOperationReady(t *testing.T, s state.Store, releases state.RuntimeReleaseStore, app state.App, candidate state.Deployment, r state.RuntimeUpgradeOperationRequest) string {
	t.Helper()
	if _, err := s.ClaimQueuedBuild(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateBuildStatus(t.Context(), r.ID, state.BuildSucceeded, "", false, true); err != nil {
		t.Fatal(err)
	}
	key := "apps/" + app.Slug + "/candidate.ext4"
	if err := s.SetDeploymentRootfs(t.Context(), candidate.ID, "/candidate", key, 20); err != nil {
		t.Fatal(err)
	}
	if err := releases.BindDeploymentRuntimeRelease(t.Context(), candidate.ID, key, r.TargetReleaseID); err != nil {
		t.Fatal(err)
	}
	node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{Name: "operation-" + uuid.NewString(), TargetURL: "unix:///tmp/operation.sock", VPCPUs: 8, MemMB: 4096, MaxConcurrency: 16, AdmissionCeilingMB: 4096, VCPUBudget: 8, Lifecycle: state.NodeLifecycleActive, LastHeartbeatAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(t.Context(), app.ID, candidate.ID, string(state.StateColdBooting), 128, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	values, err := s.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(t.Context(), app.AccountID, app.ID, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(values)
	if err != nil {
		t.Fatal(err)
	}
	target, err := releases.RuntimeReleaseByID(t.Context(), r.TargetReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	p := state.RuntimeInstancePublication{AccountID: app.AccountID, AppID: app.ID, InstanceID: instance.ID, NodeID: node.ID, WakeID: instance.WakeID, ExpectedState: string(state.StateColdBooting), Netns: "fc-" + instance.ID, HostIP: "10.100.0.8", GuestUID: 20008, Fence: fence.SecretFence, ConfigFence: fence,
		RuntimeUpgradeColdBoot: &state.RuntimeUpgradeColdBoot{TargetReleaseID: r.TargetReleaseID, BaseKey: target.BaseKey(), LayerKey: key, StartedAt: time.Now().UTC()}}
	if _, err := s.PublishOwnedInstanceRuntime(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
		t.Fatal(err)
	}
	return instance.WakeID
}

func claimRuntimeUpgradeOperation(t *testing.T, s state.RuntimeUpgradeOperationStore) state.RuntimeUpgradeOperationClaim {
	t.Helper()
	deadline := time.Now().Add(api.RuntimeUpgradeOperationInterval + 3*time.Second)
	for {
		claim, err := s.ClaimRuntimeUpgradeOperation(t.Context())
		if err == nil {
			return claim
		}
		if !errors.Is(err, state.ErrNotFound) || time.Now().After(deadline) {
			t.Fatal("claim", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type lostRuntimeUpgradeResponse struct {
	state.RuntimeUpgradeOperationStore
}

func (s lostRuntimeUpgradeResponse) AdvanceRuntimeUpgradeOperation(ctx context.Context, c state.RuntimeUpgradeOperationClaim) (state.RuntimeUpgradeOperation, error) {
	if _, err := s.RuntimeUpgradeOperationStore.AdvanceRuntimeUpgradeOperation(ctx, c); err != nil {
		return state.RuntimeUpgradeOperation{}, err
	}
	return state.RuntimeUpgradeOperation{}, errors.New("synthetic lost commit response")
}

func TestRuntimeUpgradeOperationCrashRecoveryAndHistoricalCompletion(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		app, serving, candidate, r := runtimeUpgradeOperationFixture(t, s, releases)
		ops := s.(state.RuntimeUpgradeOperationStore)
		registered, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), r)
		if err != nil || registered.Phase != state.RuntimeUpgradePrepared {
			t.Fatal(registered, err)
		}
		// Response loss after queue commit; a new executor must not queue again.
		if worked, err := (runtimeupgrade.Executor{Store: lostRuntimeUpgradeResponse{ops}}).RunOnce(t.Context()); !worked || err == nil {
			t.Fatal(worked, err)
		}
		waiting, err := ops.RuntimeUpgradeOperation(t.Context(), app.AccountID, r.ID)
		if err != nil || waiting.Phase != state.RuntimeUpgradeWaiting || waiting.LeaseToken != "" {
			t.Fatal(waiting, err)
		}
		build, err := s.BuildByDeployment(t.Context(), candidate.ID)
		if err != nil || build.ID != r.ID {
			t.Fatal(build, err)
		}
		claim := claimRuntimeUpgradeOperation(t, ops)
		if op, err := ops.AdvanceRuntimeUpgradeOperation(t.Context(), claim); err != nil || op.Phase != state.RuntimeUpgradeWaiting {
			t.Fatal(op, err)
		}
		stable, stableErr := s.DeploymentByID(t.Context(), serving.ID)
		dark, darkErr := s.DeploymentByID(t.Context(), candidate.ID)
		if stableErr != nil || darkErr != nil || stable.TrafficPercent != 100 || dark.TrafficPercent != 0 || dark.Status != state.DeployBuilding {
			t.Fatal("queue/readiness wait changed serving traffic", stable, dark, stableErr, darkErr)
		}
		wake := runtimeUpgradeOperationReady(t, s, releases, app, candidate, r)
		claim = claimRuntimeUpgradeOperation(t, ops)
		if _, err := (lostRuntimeUpgradeResponse{ops}).AdvanceRuntimeUpgradeOperation(t.Context(), claim); err == nil {
			t.Fatal("lost response not injected")
		}
		complete, err := ops.RuntimeUpgradeOperation(t.Context(), app.AccountID, r.ID)
		if err != nil || complete.Phase != state.RuntimeUpgradeComplete || complete.WakeID != wake || complete.FinishedAt.IsZero() {
			t.Fatal(complete, err)
		}
		assertRuntimeUpgradeTraffic(t, s, serving, candidate, true)
		if _, err := s.UpdateDeploymentTraffic(t.Context(), serving.ID, 100, candidate.ID); err != nil {
			t.Fatal(err)
		}
		replay, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), r)
		if err != nil || replay != complete {
			t.Fatal("operation replay changed history", replay, err)
		}
		if worked, err := (runtimeupgrade.Executor{Store: ops}).RunOnce(t.Context()); worked || err != nil {
			t.Fatal("terminal upgrade repeated", worked, err)
		}
		old, _ := s.DeploymentByID(t.Context(), serving.ID)
		current, _ := s.DeploymentByID(t.Context(), candidate.ID)
		if old.TrafficPercent != 100 || current.TrafficPercent != 0 {
			t.Fatal("rollback was undone")
		}
	})
}

func TestRuntimeUpgradeOperationIntentAndAtomicPreparation(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		app, _, candidate, r := runtimeUpgradeOperationFixture(t, s, releases)
		ops := s.(state.RuntimeUpgradeOperationStore)
		for _, edit := range []func(*state.RuntimeUpgradeOperationRequest){func(r *state.RuntimeUpgradeOperationRequest) { r.AccountID = uuid.NewString() }, func(r *state.RuntimeUpgradeOperationRequest) { r.SourceSHA256 = strings.Repeat("e", 64) }, func(r *state.RuntimeUpgradeOperationRequest) { r.QualificationReportSHA256 = strings.Repeat("e", 64) }} {
			bad := r
			edit(&bad)
			if _, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), bad); err == nil {
				t.Fatal("changed review accepted")
			}
			if _, err := ops.RuntimeUpgradeOperation(t.Context(), app.AccountID, r.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("partial operation", err)
			}
			if _, err := s.(state.RuntimeUpgradeTargetStore).DeploymentRuntimeUpgradeTarget(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("partial target", err)
			}
		}
		original, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		if replay, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), r); err != nil || replay != original {
			t.Fatal(replay, err)
		}
		bad := r
		bad.SourceSHA256 = strings.Repeat("e", 64)
		if _, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), bad); err == nil {
			t.Fatal("journal intent replaced")
		}
		bad = r
		bad.ID = uuid.NewString()
		if _, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), bad); err == nil {
			t.Fatal("candidate acquired second operation")
		}
		if _, err := ops.RuntimeUpgradeOperation(t.Context(), uuid.NewString(), r.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cross-account operation exposed", err)
		}
		if _, err := s.(state.RuntimeUpgradeBaselineStore).DeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID); err != nil {
			t.Fatal("preparation not committed with journal", err)
		}
	})
}

func TestRuntimeUpgradeOperationConcurrentClaimsAndStaleLease(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		_, _, _, r := runtimeUpgradeOperationFixture(t, s, releases)
		ops := s.(state.RuntimeUpgradeOperationStore)
		if _, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), r); err != nil {
			t.Fatal(err)
		}
		claims := make(chan state.RuntimeUpgradeOperationClaim, 8)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c, err := ops.ClaimRuntimeUpgradeOperation(t.Context())
				if err == nil {
					claims <- c
				} else if !errors.Is(err, state.ErrNotFound) {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		close(claims)
		if len(claims) != 1 {
			t.Fatal("duplicate owner", len(claims))
		}
		old := <-claims
		bad := old
		bad.LeaseToken = uuid.NewString()
		if _, err := ops.AdvanceRuntimeUpgradeOperation(t.Context(), bad); !errors.Is(err, state.ErrConflict) {
			t.Fatal(err)
		}
		// Real elapsed expiry: this also tests MemStore takeover without test clocks.
		time.Sleep(api.RuntimeUpgradeOperationLease + 100*time.Millisecond)
		fresh := claimRuntimeUpgradeOperation(t, ops)
		if fresh.LeaseToken == old.LeaseToken {
			t.Fatal("lease token reused")
		}
		if _, err := ops.AdvanceRuntimeUpgradeOperation(t.Context(), old); !errors.Is(err, state.ErrConflict) {
			t.Fatal("old executor mutated", err)
		}
		if op, err := ops.AdvanceRuntimeUpgradeOperation(t.Context(), fresh); err != nil || op.Phase != state.RuntimeUpgradeWaiting {
			t.Fatal(op, err)
		}
		if _, err := ops.AdvanceRuntimeUpgradeOperation(t.Context(), fresh); !errors.Is(err, state.ErrConflict) {
			t.Fatal("released claim reused", err)
		}
	})
}

func TestRuntimeUpgradeOperationWaitingCandidateDoesNotStarveOtherApps(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		_, _, _, first := runtimeUpgradeOperationFixture(t, s, releases)
		ops := s.(state.RuntimeUpgradeOperationStore)
		if _, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), first); err != nil {
			t.Fatal(err)
		}
		if _, err := ops.AdvanceRuntimeUpgradeOperation(t.Context(), claimRuntimeUpgradeOperation(t, ops)); err != nil {
			t.Fatal(err)
		}
		_, _, _, second := runtimeUpgradeOperationFixture(t, s, releases)
		if _, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), second); err != nil {
			t.Fatal(err)
		}
		// Both are due when the next worker tick arrives. The older waiting
		// operation must not win every tick solely because of creation order.
		time.Sleep(api.RuntimeUpgradeOperationInterval + 100*time.Millisecond)
		claim := claimRuntimeUpgradeOperation(t, ops)
		if claim.ID != second.ID {
			t.Fatal("waiting candidate starved another app", claim.ID)
		}
	})
}

func TestRuntimeUpgradeOperationBlocksReviewedInputDrift(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		for _, kind := range []string{"configuration", "qualification", "candidate"} {
			t.Run(kind, func(t *testing.T) {
				app, serving, candidate, r := runtimeUpgradeOperationFixture(t, s, releases)
				ops := s.(state.RuntimeUpgradeOperationStore)
				if _, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), r); err != nil {
					t.Fatal(err)
				}
				var err error
				switch kind {
				case "configuration":
					err = s.UpsertAppEnv(t.Context(), app.AccountID, app.ID, "REGION", "changed")
				case "qualification":
					err = s.(state.RuntimeReleaseQualificationStore).RevokeRuntimeReleaseQualification(t.Context(), r.TargetReleaseID, r.QualificationReportSHA256, strings.Repeat("f", 64))
				case "candidate":
					err = s.UpdateDeploymentStatus(t.Context(), candidate.ID, state.DeployFailed, "synthetic failure")
				}
				if err != nil {
					t.Fatal(err)
				}
				claim := claimRuntimeUpgradeOperation(t, ops)
				op, err := ops.AdvanceRuntimeUpgradeOperation(t.Context(), claim)
				if err != nil || op.Phase != state.RuntimeUpgradeBlocked || op.Blocker == "" || op.FinishedAt.IsZero() {
					t.Fatal(op, err)
				}
				if _, err := s.BuildByDeployment(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("blocked operation queued build", err)
				}
				stable, _ := s.DeploymentByID(t.Context(), serving.ID)
				if stable.TrafficPercent != 100 {
					t.Fatal("blocked upgrade changed traffic")
				}
			})
		}
	})
}
