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
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 slices 2 and 3: the branch data reconciler. A branch
// whose parent binds managed PostgreSQL or object storage starts with its
// data pending. The reconciler restores each source database as of the
// branch point (a Neon point-in-time branch) and gives each source bucket an
// overlay bucket (environment_branch_objects.go), binds the copies into the
// branch's scope with fresh credentials, marks the data ready and primes the
// branch's workloads, which stay dark until then. It also deletes the copies
// of branches that are gone.

const (
	environmentBranchDataInterval    = 5 * time.Second
	environmentBranchDataLease       = 10 * time.Minute
	environmentBranchDataBatch       = 10
	environmentBranchDataMaxAttempts = 20
	// A copy or binding the provider is still making is checked again
	// without spending an attempt (environmentBranchDataInProgressDelay), for
	// environmentBranchDataInProgressBudget from the branch's creation. A
	// Neon point-in-time branch is usually ready within seconds, and the
	// doubling retry delay of a failed attempt kept such branches pending
	// for 20-30 s.
	environmentBranchDataInProgressRetry  = time.Second
	environmentBranchDataInProgressBudget = 10 * time.Minute
)

// errEnvironmentBranchDataInProgress marks a copy or binding that is still
// being made, which is not a failed attempt.
var errEnvironmentBranchDataInProgress = errors.New("environment branch data is still being copied")

func (s *server) runEnvironmentBranchDataReconciler(ctx context.Context) {
	store, ok := s.store.(state.ProjectEnvironmentBranchDataStore)
	if !ok {
		return
	}
	wake := s.environmentBranchDataWakeChannel()
	var lastSweep time.Time
	for {
		now := time.Now().UTC()
		// Fast passes for copies in progress skip the orphan sweeps, which
		// keep the ordinary interval.
		sweep := now.Sub(lastSweep) >= environmentBranchDataInterval
		if sweep {
			lastSweep = now
		}
		soon, err := s.reconcileEnvironmentBranchDataPass(ctx, store, now, sweep)
		if err != nil && ctx.Err() == nil && s.log != nil {
			s.log.Warn("environment branch data reconcile failed", "err", err)
		}
		delay := environmentBranchDataInterval
		if soon {
			delay = environmentBranchDataInProgressRetry
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-wake:
			timer.Stop()
		}
	}
}

// environmentBranchDataWakeChannel is the reconciler's wake-up: a branch
// created with pending data starts its copy at once instead of on the next
// tick.
func (s *server) environmentBranchDataWakeChannel() chan struct{} {
	s.environmentBranchDataWakeOnce.Do(func() { s.environmentBranchDataWake = make(chan struct{}, 1) })
	return s.environmentBranchDataWake
}

func (s *server) wakeEnvironmentBranchData() {
	select {
	case s.environmentBranchDataWakeChannel() <- struct{}{}:
	default:
	}
}

// reconcileEnvironmentBranchData deletes orphaned copies, then copies the
// data of due branches, one bounded batch of each.
func (s *server) reconcileEnvironmentBranchData(ctx context.Context, store state.ProjectEnvironmentBranchDataStore, now time.Time) error {
	_, err := s.reconcileEnvironmentBranchDataPass(ctx, store, now, true)
	return err
}

// environmentBranchDataInProgressDelay is when a copy still in progress is
// checked again: every second while a quick copy is expected, then less
// often, so a slow restore does not cost a provider round trip a second.
func environmentBranchDataInProgressDelay(age time.Duration) time.Duration {
	switch {
	case age < 30*time.Second:
		return environmentBranchDataInProgressRetry
	case age < 2*time.Minute:
		return 2 * time.Second
	default:
		return environmentBranchDataInterval
	}
}

// reconcileEnvironmentBranchDataPass is one reconcile; soon reports a copy
// still in progress, which the reconciler checks again shortly.
func (s *server) reconcileEnvironmentBranchDataPass(ctx context.Context, store state.ProjectEnvironmentBranchDataStore, now time.Time, sweep bool) (soon bool, _ error) {
	var errs error
	if sweep {
		errs = s.reconcileOrphanedEnvironmentBranchDatabasesAndBuckets(ctx, store, now)
	}
	for range environmentBranchDataBatch {
		lease := uuid.NewString()
		branch, err := store.ClaimProjectEnvironmentBranchData(ctx, lease, now, environmentBranchDataLease)
		if errors.Is(err, state.ErrNotFound) {
			break
		}
		if err != nil {
			return soon, errors.Join(errs, err)
		}
		copyErr := s.copyEnvironmentBranchData(ctx, store, branch, now)
		if copyErr == nil {
			if err := store.CompleteProjectEnvironmentBranchData(ctx, branch.EnvironmentID, lease); err != nil {
				errs = errors.Join(errs, err)
				continue
			}
			s.activateEnvironmentBranch(ctx, store, branch)
			continue
		}
		if environmentBranchDataInProgress(copyErr) && now.Sub(branch.CreatedAt) < environmentBranchDataInProgressBudget {
			if err := store.DeferProjectEnvironmentBranchData(ctx, branch.EnvironmentID, lease, now.Add(environmentBranchDataInProgressDelay(now.Sub(branch.CreatedAt)))); err != nil {
				errs = errors.Join(errs, err)
			}
			soon = true
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
	return soon, errs
}

func environmentBranchDataInProgress(err error) bool {
	return errors.Is(err, errEnvironmentBranchDataInProgress) || errors.Is(err, errDatabaseCopyInProgress)
}

// reconcileOrphanedEnvironmentBranchDatabasesAndBuckets deletes one bounded
// batch of the database copies and buckets of branches that are gone.
func (s *server) reconcileOrphanedEnvironmentBranchDatabasesAndBuckets(ctx context.Context, store state.ProjectEnvironmentBranchDataStore, now time.Time) error {
	errs := s.reconcileOrphanedEnvironmentBranchBuckets(ctx, now)
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
	return errs
}

// reconcileOrphanedEnvironmentBranchBuckets empties and deletes one bounded
// batch of the buckets of branches that are gone.
func (s *server) reconcileOrphanedEnvironmentBranchBuckets(ctx context.Context, now time.Time) error {
	store, ok := s.store.(state.ProjectEnvironmentBranchBucketStore)
	if !ok {
		return nil
	}
	var errs error
	for range environmentBranchDataBatch {
		lease := uuid.NewString()
		row, err := store.ClaimOrphanedProjectEnvironmentBranchBucket(ctx, lease, now, environmentBranchDataLease)
		if errors.Is(err, state.ErrNotFound) {
			break
		}
		if err != nil {
			return errors.Join(errs, err)
		}
		if err := s.deleteEnvironmentBranchBucket(ctx, row); err != nil {
			errs = errors.Join(errs, fmt.Errorf("delete branch bucket for source %s: %w", row.SourceBucketID, err))
			continue // the lease lapses and a later pass retries
		}
		if err := store.RemoveProjectEnvironmentBranchBucket(ctx, row.ID, lease); err != nil {
			errs = errors.Join(errs, err)
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
	return errors.Is(err, errEnvironmentBranchBucketLimit) || errors.Is(err, managedpostgres.ErrQuotaExceeded) ||
		errors.Is(err, managedpostgres.ErrInvalid) || errors.Is(err, managedpostgres.ErrUnsupported)
}

// environmentBranchDataMessage is what the branch reports to its owner. It
// never carries provider text.
func environmentBranchDataMessage(err error) string {
	switch {
	case errors.Is(err, errEnvironmentBranchBucketLimit):
		return errEnvironmentBranchBucketLimit.Error()
	case errors.Is(err, objectstorage.ErrUnavailable), errors.Is(err, state.ErrObjectUsageStale):
		return "object storage is unavailable; retrying"
	case errors.Is(err, managedpostgres.ErrQuotaExceeded):
		return "the plan has no room for a copy of the parent's database; a branch that lives 7 days or less takes a place in the plan's PostgreSQL branch allowance, a longer one a free database slot, and either needs point-in-time restore"
	case errors.Is(err, managedpostgres.ErrInvalid), errors.Is(err, managedpostgres.ErrUnsupported):
		return "the parent's database cannot be restored to the branch point"
	case errors.Is(err, managedpostgres.ErrUnavailable):
		return "managed PostgreSQL is unavailable; retrying"
	default:
		return "copying the parent's database did not finish; retrying"
	}
}

// environmentBranchDatabaseTTL ends a database copy with its branch. A branch
// that lives at most api.ManagedPostgresAllowanceBranchTTLMax makes a copy
// that sits in the plan's PostgreSQL branch allowance instead of its database
// limit (ADR-20261106103654 amendment 1); a longer one makes an ordinary
// restore that still ends on its own. The TTL is counted from the branch
// point, which every retry shares, so a retry adopts the same copy. The copy
// is restored after the branch is created, so it never ends before the
// branch does; the branch's delete removes it either way.
func environmentBranchDatabaseTTL(branch state.ProjectEnvironmentBranch, point time.Time) time.Duration {
	// Rounded up, so the copy never ends a fraction of a second early.
	ttl := (branch.ExpiresAt.Sub(point) + time.Second - 1).Truncate(time.Second)
	if branch.ExpiresAt.Sub(branch.CreatedAt) <= api.ManagedPostgresAllowanceBranchTTLMax {
		ttl = min(ttl, api.ManagedPostgresAllowanceBranchTTLMax)
	}
	return max(min(ttl, managedpostgres.MaxBranchTTL), managedpostgres.MinBranchTTL)
}

// environmentBranchDatabaseName is the copy's deterministic name: one copy
// per source database per branch environment.
func environmentBranchDatabaseName(branch state.ProjectEnvironmentBranch, sourceDatabaseID string) string {
	sum := sha256.Sum256([]byte(branch.EnvironmentID + "\x00" + sourceDatabaseID))
	return "br-" + branch.Slug + "-" + hex.EncodeToString(sum[:6])
}

// copyEnvironmentBranchData restores each database the parent binds as of
// the branch point, overlays each bucket it binds, and binds the copies into
// the branch's scope once per parent binding. Every step is idempotent, so a
// retry resumes.
func (s *server) copyEnvironmentBranchData(ctx context.Context, store state.ProjectEnvironmentBranchDataStore, branch state.ProjectEnvironmentBranch, now time.Time) error {
	ctx, acct, project, err := s.environmentBranchOwner(ctx, branch)
	if err != nil {
		return err
	}
	apps, snapshot, err := s.captureProjectEnvironmentValues(ctx, acct, project, branch.ParentSlug)
	if err != nil {
		return err
	}
	plans, err := s.planProjectEnvironmentBindingClonesFor(ctx, acct, apps, snapshot.ValueScopes, false, true)
	if err != nil {
		return err
	}
	if err := s.copyEnvironmentBranchDatabases(ctx, store, acct, branch, plans, now); err != nil {
		return err
	}
	return s.overlayEnvironmentBranchBuckets(ctx, acct, branch, plans)
}

// environmentBranchOwner resolves the account a branch's data belongs to the
// way a request does: the project's resource owner, which is the
// organization's account for a company project that owns its resources. A
// company project's managed PostgreSQL work carries the organization's
// ownership, as projectaccess sets it on a request.
func (s *server) environmentBranchOwner(ctx context.Context, branch state.ProjectEnvironmentBranch) (context.Context, state.Account, state.Project, error) {
	project, err := s.store.ProjectByID(ctx, branch.ProjectID)
	if err != nil {
		return ctx, state.Account{}, state.Project{}, err
	}
	ctx, acct, err := s.environmentProjectOwner(ctx, project)
	if err != nil {
		return ctx, state.Account{}, state.Project{}, err
	}
	if acct.ID != branch.AccountID {
		return ctx, state.Account{}, state.Project{}, state.ErrConflict
	}
	return ctx, acct, project, nil
}

// environmentProjectOwner is the account a project's environment work runs
// as, with the organization's managed PostgreSQL ownership on ctx for a
// company project.
func (s *server) environmentProjectOwner(ctx context.Context, project state.Project) (context.Context, state.Account, error) {
	acct, err := state.ProjectResourceAccount(ctx, s.store, project)
	if err != nil {
		return ctx, state.Account{}, err
	}
	if project.OrgID != "" {
		org, err := s.store.OrgByID(ctx, project.OrgID)
		if err != nil {
			return ctx, state.Account{}, err
		}
		if !org.Personal {
			ctx = managedpostgres.WithOwnership(ctx, org.ID, acct.ResourceOwnerID())
		}
	}
	return ctx, acct, nil
}

// copyEnvironmentBranchDatabases restores each database the parent binds as
// of the branch point and binds the copy into the branch's scope.
func (s *server) copyEnvironmentBranchDatabases(ctx context.Context, store state.ProjectEnvironmentBranchDataStore, acct state.Account, branch state.ProjectEnvironmentBranch, plans []projectEnvironmentBindingClone, now time.Time) error {
	sources := map[string]managedpostgres.Database{}
	for _, plan := range plans {
		if plan.kind == "managed_postgres" {
			sources[plan.database.ID] = plan.database
		}
	}
	if len(sources) == 0 {
		return nil
	}
	if s.managedPostgres == nil || s.managedPostgresBindings == nil {
		return managedpostgres.ErrUnavailable
	}
	// The provider cannot restore to an instant it has not yet archived: a
	// point a few hundred milliseconds old is refused as invalid, which fails
	// the branch's data for good. Branch create wakes the reconciler at once,
	// so wait until the point is as old as a latest restore's (prod
	// 2026-11-24: a copy 112 ms after the point was refused). Waiting is not
	// a failed attempt.
	if wait := branch.BranchPoint.Add(managedpostgres.LatestRestoreLag).Sub(now); wait > 0 {
		return fmt.Errorf("the branch point is %s too recent to restore: %w", wait.Round(time.Millisecond), errEnvironmentBranchDataInProgress)
	}
	ids := make([]string, 0, len(sources))
	for id := range sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		source := sources[id]
		row, err := store.RecordProjectEnvironmentBranchDatabase(ctx, state.ProjectEnvironmentBranchDatabase{
			AccountID: acct.ID, ProjectID: branch.ProjectID, EnvironmentID: branch.EnvironmentID, EnvironmentSlug: branch.Slug,
			SourceDatabaseID: id, CopyName: environmentBranchDatabaseName(branch, id), PointInTime: branch.BranchPoint,
		})
		if err != nil {
			return err
		}
		// Copies are never compensated here: a failed copy stays recorded and
		// owned by the branch, so a retry adopts it or the orphan sweep removes it.
		var unused []func(context.Context) error
		copied, err := s.ensureProjectEnvironmentDatabaseClone(ctx, acct, projectEnvironmentDatabaseCopy{source: source, name: row.CopyName, pointInTime: row.PointInTime,
			ttl: environmentBranchDatabaseTTL(branch, row.PointInTime)}, &unused)
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
			if binding.DatabaseID == copied.ID && binding.State == managedpostgres.BindingStateProvisioning {
				return fmt.Errorf("database copy binding for workload %q is not ready: %w%w", plan.app.Slug, errEnvironmentBranchDataInProgress, managedpostgres.ErrUnavailable)
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
	if err := s.deleteEnvironmentBranchCopyBindings(ctx, row.AccountID, copyID); err != nil {
		return err
	}
	if _, err := s.managedPostgres.Delete(ctx, row.AccountID, copyID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
		return err
	}
	return nil
}

// deleteEnvironmentBranchCopyBindings deletes every binding of a branch's
// database copy, and with each its credential in the branch's scope.
func (s *server) deleteEnvironmentBranchCopyBindings(ctx context.Context, accountID, copyID string) error {
	bindings, err := s.managedPostgresBindings.List(ctx, accountID, copyID)
	if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
		return err
	}
	for _, binding := range bindings {
		if _, err := s.managedPostgresBindings.Delete(ctx, accountID, binding.ID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return err
		}
	}
	return nil
}

// releaseEnvironmentBranchCredentials removes the credentials a branch's
// database copies and buckets hold in its scope, leaving the copies and
// buckets to the reconciler. A reset runs it before deleting the branch:
// the branch is made again under the same slug, and a scope still holding
// the old branch's credentials refuses the new one.
func (s *server) releaseEnvironmentBranchCredentials(ctx context.Context, branch state.ProjectEnvironmentBranch) error {
	if store, ok := s.store.(state.ProjectEnvironmentBranchDataStore); ok {
		copies, err := store.ListProjectEnvironmentBranchDatabases(ctx, branch.EnvironmentID)
		if err != nil {
			return err
		}
		for _, row := range copies {
			if row.CopyDatabaseID == "" {
				continue // bindings are made only once the copy is recorded
			}
			if s.managedPostgresBindings == nil {
				return managedpostgres.ErrUnavailable
			}
			if err := s.deleteEnvironmentBranchCopyBindings(ctx, row.AccountID, row.CopyDatabaseID); err != nil {
				return fmt.Errorf("release database copy %s: %w", row.CopyName, err)
			}
		}
	}
	buckets, ok := s.store.(state.ProjectEnvironmentBranchBucketStore)
	if !ok {
		return nil
	}
	rows, err := buckets.ListProjectEnvironmentBranchBuckets(ctx, branch.EnvironmentID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.CopyBucketID == "" {
			continue
		}
		if err := s.revokeEnvironmentBranchBucketCredentials(ctx, row.AccountID, row.CopyBucketID); err != nil {
			return fmt.Errorf("release branch bucket for source %s: %w", row.SourceBucketID, err)
		}
	}
	return nil
}
