package runtimeupgrade

// adr: 692

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func controlStores(t *testing.T, run func(*testing.T, state.Store, *pgxpool.Pool)) {
	t.Helper()
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			var pool *pgxpool.Pool
			if kind == "postgres" {
				pool = pgtest.OpenMigrated(t)
				if err := db.MigrateUp(t.Context(), pool); err != nil {
					t.Fatal(err)
				}
				store = state.NewPgStore(pool)
			}
			run(t, store, pool)
		})
	}
}

func TestReservationAtomicReplayAndAdmissionFences(t *testing.T) {
	controlStores(t, func(t *testing.T, store state.Store, _ *pgxpool.Pool) {
		s, r, serving := stagingCandidateFixture(t, store, false)
		ops := store.(state.RuntimeUpgradeReservationStore)
		path, _ := s.CandidatePath(r.ID)
		invalid := r
		invalid.QualificationReportSHA256 = strings.Repeat("a", 64)
		if _, err := ops.ReserveRuntimeUpgradeOperation(t.Context(), invalid, path); err == nil {
			t.Fatal("unqualified reservation admitted")
		}
		if _, err := store.DeploymentByID(t.Context(), r.DeploymentID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("candidate escaped rejected reservation", err)
		}
		if _, err := ops.RuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("journal escaped rejected reservation", err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for range 4 {
			wg.Go(func() { _, err := ops.ReserveRuntimeUpgradeOperation(t.Context(), r, path); errs <- err })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal("identical concurrent reservation", err)
			}
		}
		op, err := ops.RuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID)
		if err != nil || op.Phase != state.RuntimeUpgradeReserved || op.SourcePath != path {
			t.Fatal(op, err)
		}
		candidate, err := store.DeploymentByID(t.Context(), r.DeploymentID)
		if err != nil || candidate.Status != state.DeployPending || candidate.TrafficPercent != 0 || !candidate.TrafficPercentExplicit || candidate.RootfsKey != "" || candidate.BuildID != "" ||
			candidate.SourceSHA256 != serving.SourceSHA256 || candidate.Handler != serving.Handler || candidate.SourceRoot != serving.SourceRoot || candidate.SourceBytes != serving.SourceBytes || candidate.SourcePath != path || candidate.GitHubSourceRef != "" || candidate.GitHubInstallationID != 0 || !sameJSON(candidate.OverrideEnv, serving.OverrideEnv) || len(candidate.OverrideCmd) != 1 || candidate.OverrideCmd[0] != serving.OverrideCmd[0] ||
			candidate.DisableStartupCPUBoost != serving.DisableStartupCPUBoost || candidate.RollbackOn5xx != serving.RollbackOn5xx {
			t.Fatal("candidate inputs/output state", candidate, err)
		}
		baseline, err := store.(state.RuntimeUpgradeBaselineStore).DeploymentRuntimeUpgradeBaseline(t.Context(), r.DeploymentID)
		if err != nil || baseline.ServingDeploymentID != serving.ID {
			t.Fatal(baseline, err)
		}
		if _, err := ops.ClaimRuntimeUpgradeOperation(t.Context()); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("unstaged reservation claimable", err)
		}
		if _, err := store.CreateBuildWithID(t.Context(), r.ID, r.DeploymentID, candidate.Kind, candidate.SourceBytes, ""); err == nil {
			t.Fatal("generic queue bypassed staging")
		}
		if _, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), r); !errors.Is(err, state.ErrConflict) {
			t.Fatal("legacy registration bypassed reservation", err)
		}
		other := r
		other.ID, other.DeploymentID = uuid.NewString(), uuid.NewString()
		otherPath, _ := s.CandidatePath(other.ID)
		if _, err := ops.ReserveRuntimeUpgradeOperation(t.Context(), other, otherPath); !errors.Is(err, state.ErrConflict) {
			t.Fatal("second active app operation", err)
		}
		if _, err := store.DeploymentByID(t.Context(), other.DeploymentID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("conflicting candidate retained", err)
		}
		replay, err := ops.ReserveRuntimeUpgradeOperation(t.Context(), r, path)
		if err != nil || replay != op {
			t.Fatal("reservation replay changed history", replay, err)
		}
		if _, err := ops.ReserveRuntimeUpgradeOperation(t.Context(), r, "/another/"+r.ID+".tar.gz"); !errors.Is(err, state.ErrConflict) {
			t.Fatal("path rebound", err)
		}
	})
}

type reservationResponseLoss struct {
	StagingStore
	Reservations state.RuntimeUpgradeReservationStore
}

func (s reservationResponseLoss) ReserveRuntimeUpgradeOperation(ctx context.Context, r state.RuntimeUpgradeOperationRequest, path string) (state.RuntimeUpgradeOperation, error) {
	if _, err := s.Reservations.ReserveRuntimeUpgradeOperation(ctx, r, path); err != nil {
		return state.RuntimeUpgradeOperation{}, err
	}
	return state.RuntimeUpgradeOperation{}, errLostRegistrationResponse
}
func (s reservationResponseLoss) PrepareReservedRuntimeUpgradeOperation(ctx context.Context, account, id string) (state.RuntimeUpgradeOperation, error) {
	return s.Reservations.PrepareReservedRuntimeUpgradeOperation(ctx, account, id)
}
func (s reservationResponseLoss) CancelRuntimeUpgradeOperation(ctx context.Context, account, id string) (state.RuntimeUpgradeOperation, error) {
	return s.Reservations.CancelRuntimeUpgradeOperation(ctx, account, id)
}

func TestReservationLostResponseSourceRetryAndPrivateStatus(t *testing.T) {
	controlStores(t, func(t *testing.T, store state.Store, _ *pgxpool.Pool) {
		s, r, serving := stagingCandidateFixture(t, store, false)
		ops := store.(state.RuntimeUpgradeReservationStore)
		s.Store = reservationResponseLoss{s.Store, ops}
		if _, err := s.ReserveAndStage(t.Context(), r); !errors.Is(err, errLostRegistrationResponse) {
			t.Fatal("reservation response loss", err)
		}
		path, _ := s.CandidatePath(r.ID)
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("source I/O ran before reservation acknowledgement", err)
		}
		s.Store = store.(StagingStore)
		original := s.Source
		s.Source = failingSource{StorageBackend: original}
		if _, err := s.ReserveAndStage(t.Context(), r); err == nil {
			t.Fatal("source outage ignored")
		}
		status, err := (Controls{ops}).Status(t.Context(), r.AccountID, r.ID)
		if err != nil || status.Phase != state.RuntimeUpgradeReserved || !status.CanCancel {
			t.Fatal(status, err)
		}
		if _, err := (Controls{ops}).Status(t.Context(), uuid.NewString(), r.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cross-account status", err)
		}
		raw, _ := json.Marshal(status)
		for _, text := range []string{"lease_token", "source_path", s.SpoolRoot, serving.SourcePath, "retained-value"} {
			if strings.Contains(string(raw), text) {
				t.Fatal("private status leaked journal material", string(raw))
			}
		}
		s.Source = original
		prepared, err := s.ReserveAndStage(t.Context(), r)
		if err != nil || prepared.Phase != state.RuntimeUpgradePrepared {
			t.Fatal(prepared, err)
		}
		s.Source = failingSource{StorageBackend: original}
		replay, err := s.ReserveAndStage(t.Context(), r)
		if err != nil || replay != prepared {
			t.Fatal("prepared replay performed I/O", replay, err)
		}
		if worked, err := (Executor{Store: ops}).RunOnce(t.Context()); !worked || err != nil {
			t.Fatal(worked, err)
		}
		claim, err := store.ClaimQueuedBuild(t.Context(), r.ID)
		if err != nil || claim.ID != r.ID {
			t.Fatal(claim, err)
		}
		workerClaim, err := ops.ClaimRuntimeUpgradeOperation(t.Context())
		// The next operation poll may not be due; cancellation still clears any lease.
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			t.Fatal(err)
		}
		cancelled, err := (Controls{ops}).Cancel(t.Context(), r.AccountID, r.ID)
		if err != nil || cancelled.Phase != state.RuntimeUpgradeCancelled || cancelled.CanCancel {
			t.Fatal(cancelled, err)
		}
		build, err := store.BuildByDeployment(t.Context(), r.DeploymentID)
		if err != nil || build.Status != state.BuildCancelled || !build.CancelledByDeploymentCascade {
			t.Fatal(build, err)
		}
		cleanup, err := store.(state.BuildVMCleanupStore).ClaimBuildVMCleanup(t.Context(), 1)
		if err != nil || len(cleanup) != 1 || cleanup[0].BuildID != r.ID {
			t.Fatal("running VM cleanup was not durable", cleanup, err)
		}
		if workerClaim.ID != "" {
			if _, err := ops.AdvanceRuntimeUpgradeOperation(t.Context(), workerClaim); !errors.Is(err, state.ErrConflict) {
				t.Fatal("cancelled lease mutated", err)
			}
		}
		stable, err := store.DeploymentByID(t.Context(), serving.ID)
		if err != nil || stable.Status != state.DeployLive || stable.TrafficPercent != 100 {
			t.Fatal("cancellation changed serving release", stable, err)
		}
		repeated, err := (Controls{ops}).Cancel(t.Context(), r.AccountID, r.ID)
		if err != nil || repeated != cancelled {
			t.Fatal("cancel response replay", repeated, err)
		}
		historical, err := s.ReserveAndStage(t.Context(), r)
		if err != nil || historical.Phase != state.RuntimeUpgradeCancelled {
			t.Fatal("cancelled preparation reopened", historical, err)
		}
	})
}

func TestReservationCancellationDuringSourcePublication(t *testing.T) {
	controlStores(t, func(t *testing.T, store state.Store, _ *pgxpool.Pool) {
		s, r, serving := stagingCandidateFixture(t, store, false)
		ops := store.(state.RuntimeUpgradeReservationStore)
		s.Source = publicationFault{StorageBackend: s.Source, after: func() error {
			if _, err := ops.CancelRuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID); err != nil {
				return err
			}
			return nil
		}}
		op, err := s.ReserveAndStage(t.Context(), r)
		if err != nil || op.Phase != state.RuntimeUpgradeCancelled {
			t.Fatal("publication raced cancellation", op, err)
		}
		if _, err := ops.ClaimRuntimeUpgradeOperation(t.Context()); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cancelled reservation claimable", err)
		}
		if _, err := store.BuildByDeployment(t.Context(), r.DeploymentID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cancelled source queued", err)
		}
		stable, err := store.DeploymentByID(t.Context(), serving.ID)
		if err != nil || stable.TrafficPercent != 100 {
			t.Fatal(stable, err)
		}
	})
}

func TestReservationQualificationDriftKeepsOriginalBaseline(t *testing.T) {
	controlStores(t, func(t *testing.T, store state.Store, _ *pgxpool.Pool) {
		s, r, _ := stagingCandidateFixture(t, store, false)
		s.Source = publicationFault{StorageBackend: s.Source, after: func() error {
			if err := store.(state.RuntimeReleaseQualificationStore).RevokeRuntimeReleaseQualification(t.Context(), r.TargetReleaseID, r.QualificationReportSHA256, strings.Repeat("a", 64)); err != nil {
				return err
			}
			return nil
		}}
		op, err := s.ReserveAndStage(t.Context(), r)
		if err != nil || op.Phase != state.RuntimeUpgradeBlocked || op.Blocker != "qualification_changed" {
			t.Fatal("qualification changed during staging", op, err)
		}
		if _, err := store.BuildByDeployment(t.Context(), r.DeploymentID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("revoked reservation queued", err)
		}
		if _, err := store.(state.RuntimeUpgradeReservationStore).CancelRuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatal("terminal history replaced", err)
		}
	})
}

func TestReservationConfigurationDriftDuringStaging(t *testing.T) {
	controlStores(t, func(t *testing.T, store state.Store, _ *pgxpool.Pool) {
		s, r, _ := stagingCandidateFixture(t, store, false)
		ops := store.(state.RuntimeUpgradeReservationStore)
		var before state.RuntimeUpgradeBaseline
		s.Source = publicationFault{StorageBackend: s.Source, after: func() error {
			var err error
			before, err = store.(state.RuntimeUpgradeBaselineStore).DeploymentRuntimeUpgradeBaseline(t.Context(), r.DeploymentID)
			if err != nil {
				return err
			}
			return store.UpsertAppEnv(t.Context(), r.AccountID, r.AppID, "STAGING_REVISION", "changed")
		}}
		op, err := s.ReserveAndStage(t.Context(), r)
		if err != nil || op.Phase != state.RuntimeUpgradeBlocked || op.Blocker != "baseline_changed" {
			t.Fatal("configuration adopted during I/O", op, err)
		}
		after, err := store.(state.RuntimeUpgradeBaselineStore).DeploymentRuntimeUpgradeBaseline(t.Context(), r.DeploymentID)
		if err != nil || after != before {
			t.Fatal("reservation baseline recaptured", after, err)
		}
		if _, err := ops.ClaimRuntimeUpgradeOperation(t.Context()); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("drifted reservation runnable", err)
		}
	})
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}
