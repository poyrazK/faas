package apidserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 slice 2: the branch data reconciler. A branch whose
// parent binds managed PostgreSQL starts with its data pending. The
// reconciler restores each source database as of the branch point (a Neon
// point-in-time branch), binds the copy into the branch's scope with fresh
// credentials, marks the data ready and primes the branch's workloads, which
// stay dark until then. It also deletes the copies of branches that are gone.

const (
	environmentBranchDataInterval    = 5 * time.Second
	environmentBranchDataLease       = 10 * time.Minute
	environmentBranchDataBatch       = 10
	environmentBranchDataMaxAttempts = 20
)

var errEnvironmentBranchObjectStorage = errors.New("the parent binds object storage, which branches cannot copy yet")

func (s *server) runEnvironmentBranchDataReconciler(ctx context.Context) {
	store, ok := s.store.(state.ProjectEnvironmentBranchDataStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(environmentBranchDataInterval)
	defer ticker.Stop()
	for {
		if err := s.reconcileEnvironmentBranchData(ctx, store, time.Now().UTC()); err != nil && ctx.Err() == nil && s.log != nil {
			s.log.Warn("environment branch data reconcile failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// reconcileEnvironmentBranchData deletes orphaned copies, then copies the
// data of due branches, one bounded batch of each.
func (s *server) reconcileEnvironmentBranchData(ctx context.Context, store state.ProjectEnvironmentBranchDataStore, now time.Time) error {
	var errs error
	for range environmentBranchDataBatch {
		lease := uuid.NewString()
		row, err := store.ClaimOrphanedProjectEnvironmentBranchDatabase(ctx, lease, now, environmentBranchDataLease)
		if errors.Is(err, state.ErrNotFound) {
			break
		}
		if err != nil {
			return errors.Join(errs, err)
		}
		if err := s.deleteEnvironmentBranchDatabase(ctx, row); err != nil {
			errs = errors.Join(errs, fmt.Errorf("delete branch database copy %s: %w", row.CopyName, err))
			continue // the lease lapses and a later pass retries
		}
		if err := store.RemoveProjectEnvironmentBranchDatabase(ctx, row.ID, lease); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	for range environmentBranchDataBatch {
		lease := uuid.NewString()
		branch, err := store.ClaimProjectEnvironmentBranchData(ctx, lease, now, environmentBranchDataLease)
		if errors.Is(err, state.ErrNotFound) {
			break
		}
		if err != nil {
			return errors.Join(errs, err)
		}
		copyErr := s.copyEnvironmentBranchData(ctx, store, branch)
		if copyErr == nil {
			if err := store.CompleteProjectEnvironmentBranchData(ctx, branch.EnvironmentID, lease); err != nil {
				errs = errors.Join(errs, err)
				continue
			}
			s.activateEnvironmentBranch(ctx, store, branch)
			continue
		}
		attempts := branch.DataAttempts + 1
		failed := environmentBranchDataPermanent(copyErr) || attempts >= environmentBranchDataMaxAttempts
		next := now.Add(environmentBranchDataRetryDelay(attempts))
		if err := store.RetryProjectEnvironmentBranchData(ctx, branch.EnvironmentID, lease, next, environmentBranchDataMessage(copyErr), failed); err != nil {
			errs = errors.Join(errs, err)
		}
		if s.log != nil {
			s.log.Warn("environment branch data copy did not finish", "environment_id", branch.EnvironmentID,
				"environment", branch.Slug, "attempt", attempts, "failed", failed, "err", copyErr)
		}
	}
	return errs
}

func environmentBranchDataRetryDelay(attempt int) time.Duration {
	delay := 5 * time.Second
	for i := 1; i < attempt && delay < 10*time.Minute; i++ {
		delay *= 2
	}
	return min(delay, 10*time.Minute)
}

// Permanent errors fail the branch's data instead of retrying: the plan
// forbids the copy, or the request can never succeed.
func environmentBranchDataPermanent(err error) bool {
	return errors.Is(err, errEnvironmentBranchObjectStorage) || errors.Is(err, managedpostgres.ErrQuotaExceeded) ||
		errors.Is(err, managedpostgres.ErrInvalid) || errors.Is(err, managedpostgres.ErrUnsupported)
}

// environmentBranchDataMessage is what the branch reports to its owner. It
// never carries provider text.
func environmentBranchDataMessage(err error) string {
	switch {
	case errors.Is(err, errEnvironmentBranchObjectStorage):
		return errEnvironmentBranchObjectStorage.Error()
	case errors.Is(err, managedpostgres.ErrQuotaExceeded):
		return "the plan has no room for a copy of the parent's database; branching a database needs point-in-time restore and a free database slot"
	case errors.Is(err, managedpostgres.ErrInvalid), errors.Is(err, managedpostgres.ErrUnsupported):
		return "the parent's database cannot be restored to the branch point"
	case errors.Is(err, managedpostgres.ErrUnavailable):
		return "managed PostgreSQL is unavailable; retrying"
	default:
		return "copying the parent's database did not finish; retrying"
	}
}

// environmentBranchDatabaseName is the copy's deterministic name: one copy
// per source database per branch environment.
func environmentBranchDatabaseName(branch state.ProjectEnvironmentBranch, sourceDatabaseID string) string {
	sum := sha256.Sum256([]byte(branch.EnvironmentID + "\x00" + sourceDatabaseID))
	return "br-" + branch.Slug + "-" + hex.EncodeToString(sum[:6])
}

// copyEnvironmentBranchData restores each database the parent binds as of
// the branch point and binds the copy into the branch's scope, once per
// parent binding. Every step is idempotent, so a retry resumes.
func (s *server) copyEnvironmentBranchData(ctx context.Context, store state.ProjectEnvironmentBranchDataStore, branch state.ProjectEnvironmentBranch) error {
	if s.managedPostgres == nil || s.managedPostgresBindings == nil {
		return managedpostgres.ErrUnavailable
	}
	acct, err := s.store.AccountByID(ctx, branch.AccountID)
	if err != nil {
		return err
	}
	project, err := s.store.ProjectByID(ctx, branch.ProjectID)
	if err != nil {
		return err
	}
	if project.AccountID != acct.ID {
		return state.ErrConflict
	}
	apps, snapshot, err := s.captureProjectEnvironmentValues(ctx, acct, project, branch.ParentSlug)
	if err != nil {
		return err
	}
	plans, err := s.planProjectEnvironmentBindingClones(ctx, acct, apps, snapshot.ValueScopes, false)
	if err != nil {
		return err
	}
	sources := map[string]managedpostgres.Database{}
	for _, plan := range plans {
		switch plan.kind {
		case "managed_postgres":
			sources[plan.database.ID] = plan.database
		case "object_storage":
			return errEnvironmentBranchObjectStorage
		}
	}
	ids := make([]string, 0, len(sources))
	for id := range sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		source := sources[id]
		row, err := store.RecordProjectEnvironmentBranchDatabase(ctx, state.ProjectEnvironmentBranchDatabase{
			AccountID: acct.ID, ProjectID: project.ID, EnvironmentID: branch.EnvironmentID, EnvironmentSlug: branch.Slug,
			SourceDatabaseID: id, CopyName: environmentBranchDatabaseName(branch, id), PointInTime: branch.BranchPoint,
		})
		if err != nil {
			return err
		}
		// Copies are never compensated here: a failed copy stays recorded and
		// owned by the branch, so a retry adopts it or the orphan sweep removes it.
		var unused []func(context.Context) error
		copied, err := s.ensureProjectEnvironmentDatabaseClone(ctx, acct, projectEnvironmentDatabaseCopy{source: source, name: row.CopyName, pointInTime: row.PointInTime}, &unused)
		if err != nil {
			return err
		}
		if err := store.SetProjectEnvironmentBranchDatabaseCopy(ctx, row.ID, copied.ID); err != nil {
			return err
		}
		for _, plan := range plans {
			if plan.kind != "managed_postgres" || plan.database.ID != id {
				continue
			}
			binding, _, err := s.managedPostgresBindings.CreateWithResult(ctx, managedpostgres.CreateBindingRequest{
				AccountID: acct.ID, DatabaseID: copied.ID, AppID: plan.app.ID,
				Scope: branch.Slug, EnvironmentKey: plan.postgres.EnvironmentKey, Access: plan.postgres.Access,
			})
			if err != nil {
				return fmt.Errorf("bind database copy for workload %q: %w", plan.app.Slug, err)
			}
			if binding.State != managedpostgres.BindingStateReady || binding.DatabaseID != copied.ID {
				return fmt.Errorf("database copy binding for workload %q is not ready: %w", plan.app.Slug, managedpostgres.ErrUnavailable)
			}
		}
	}
	return nil
}

// activateEnvironmentBranch primes the branch's dark deployments now that
// their data exists. A lost notification is recovered by schedd's prime
// recovery, which re-primes deployments left in snapshotting.
func (s *server) activateEnvironmentBranch(ctx context.Context, store state.ProjectEnvironmentBranchDataStore, branch state.ProjectEnvironmentBranch) {
	deployments, err := store.ProjectEnvironmentBranchDeploymentsAwaitingActivation(ctx, branch.EnvironmentID)
	if err != nil {
		if s.log != nil {
			s.log.Warn("environment branch activation could not list deployments; prime recovery will retry", "environment_id", branch.EnvironmentID, "err", err)
		}
		return
	}
	for _, d := range deployments {
		payload, err := json.Marshal(map[string]string{"app_id": d.AppID, "deployment_id": d.ID})
		if err != nil || s.notif == nil {
			continue
		}
		if err := s.notif.Notify(ctx, db.NotifySnapshotPrime, string(payload)); err != nil && s.log != nil {
			s.log.Warn("environment branch activation notification failed; prime recovery will retry", "deployment_id", d.ID, "err", err)
		}
	}
	s.audit.Emit(ctx, "project.environment.branch_data_ready", &branch.AccountID, map[string]any{
		"project_id": branch.ProjectID, "environment_id": branch.EnvironmentID, "environment_slug": branch.Slug,
		"workloads": len(deployments),
	})
}

// deleteEnvironmentBranchDatabase deletes a copy's bindings, then the copy.
// A copy recorded before its restore finished is found by name.
func (s *server) deleteEnvironmentBranchDatabase(ctx context.Context, row state.ProjectEnvironmentBranchDatabase) error {
	if s.managedPostgres == nil || s.managedPostgresBindings == nil {
		return managedpostgres.ErrUnavailable
	}
	copyID := row.CopyDatabaseID
	if copyID == "" {
		databases, err := s.managedPostgres.List(ctx, row.AccountID)
		if err != nil {
			return err
		}
		for _, database := range databases {
			if database.Name == row.CopyName && database.RestoreSourceDatabaseID == row.SourceDatabaseID &&
				database.State != managedpostgres.StateDeleted {
				copyID = database.ID
			}
		}
		if copyID == "" {
			return nil // the restore never started
		}
	}
	bindings, err := s.managedPostgresBindings.List(ctx, row.AccountID, copyID)
	if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
		return err
	}
	for _, binding := range bindings {
		if _, err := s.managedPostgresBindings.Delete(ctx, row.AccountID, binding.ID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return err
		}
	}
	if _, err := s.managedPostgres.Delete(ctx, row.AccountID, copyID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
		return err
	}
	return nil
}
