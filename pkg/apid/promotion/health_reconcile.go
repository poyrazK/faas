package promotion

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type HealthActions struct {
	RecoveryProbe  func(context.Context, state.ProjectEnvironmentPromotion, api.ProjectPromotionHealthPolicy, state.ProjectReleaseSet) ([]api.ProjectReleaseProbeEvidence, error)
	RecoveryRecord func(context.Context, state.PromotionHealthJob, *api.ProjectPromotionMigrationRecovery)

	Now      func() time.Time
	Record   func(context.Context, state.ProjectEnvironmentPromotion, *api.ProjectPromotionHealth)
	Prepare  func(context.Context, state.ProjectEnvironmentPromotion, []state.ProjectEnvironmentPromotionWorkload) error
	Cutover  func(context.Context, state.ProjectEnvironmentPromotion, []state.ProjectEnvironmentPromotionWorkload) error
	Verify   func(context.Context, state.ProjectEnvironmentPromotion) error
	Rollback func(context.Context, state.ProjectEnvironmentPromotion) error
	Probe    func(context.Context, state.ProjectEnvironmentPromotion, []state.ProjectEnvironmentPromotionWorkload, api.ProjectPromotionHealthPolicy) ([]api.ProjectReleaseProbeEvidence, error)
}

// ReconcileHealth performs at most one external action or sample round. All
// decisions use persisted generations; external calls hold no database locks.
func ReconcileHealth(ctx context.Context, store state.Store, job state.PromotionHealthJob, a HealthActions) error {
	health, ok := store.(state.ProjectPromotionHealthStore)
	if !ok {
		return nil
	}
	token := uuid.NewString()
	claimed, err := health.ClaimProjectEnvironmentPromotionHealth(ctx, job.AccountID, job.PromotionID, token)
	if err != nil || !claimed {
		return err
	}
	return reconcileOwnedHealth(ctx, store, job, a, token)
}

// ReconcileClaimedHealth consumes a scheduler claim without taking a second lease.
func ReconcileClaimedHealth(ctx context.Context, store state.Store, job state.PromotionHealthJob, a HealthActions, token string) error {
	scheduler, ok := store.(state.ProjectPromotionHealthScheduler)
	if !ok {
		return fmt.Errorf("promotion scheduling unavailable")
	}
	health, ok := store.(state.ProjectPromotionHealthStore)
	if !ok {
		return fmt.Errorf("promotion health storage unavailable")
	}
	current, err := scheduler.ProjectEnvironmentPromotionHealthLeaseCurrent(ctx, job.AccountID, job.PromotionID, token)
	if err != nil || !current {
		return errors.Join(err, releaseHealthLease(ctx, health, job.PromotionID, token))
	}
	return reconcileOwnedHealth(ctx, store, job, a, token)
}

func reconcileOwnedHealth(ctx context.Context, store state.Store, job state.PromotionHealthJob, a HealthActions, token string) (resultErr error) {
	health := store.(state.ProjectPromotionHealthStore)
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	defer func() { resultErr = errors.Join(resultErr, releaseHealthLease(ctx, health, job.PromotionID, token)) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if recoveries, ok := store.(state.ProjectPromotionMigrationRecoveryStore); ok {
		r, readErr := recoveries.ProjectPromotionMigrationRecovery(ctx, job.AccountID, job.PromotionID)
		if readErr != nil {
			return readErr
		}
		if r != nil && (r.Status == "quiescing" || r.Status == "verifying") {
			return reconcileMigrationRecovery(ctx, store, job, a, r)
		}
	}
	p, workloads, err := store.ProjectEnvironmentPromotionByID(ctx, job.AccountID, job.ProjectSlug, job.ToEnvironment, job.PromotionID)
	if err != nil || p.Status != "running" {
		return err
	}
	h, err := health.ProjectEnvironmentPromotionHealth(ctx, p.AccountID, p.ID)
	if err != nil || h == nil {
		return err
	}
	if h.Phase == "queued" {
		admission, ok := store.(state.ProjectPromotionAdmissionStore)
		if !ok {
			return fmt.Errorf("promotion admission storage unavailable")
		}
		next, err := admission.DispatchProjectEnvironmentPromotion(ctx, p.AccountID, p.ID, token, h.Generation)
		if errors.Is(err, state.ErrConflict) {
			return nil
		}
		if err == nil && next != nil && next.Generation > h.Generation && a.Record != nil {
			a.Record(ctx, p, next)
		}
		return err
	}
	advance := func(action string, evidence []api.ProjectReleaseProbeEvidence) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, err := health.AdvanceProjectEnvironmentPromotionHealth(ctx, p.AccountID, p.ID, state.PromotionHealthObservation{LeaseToken: token, Generation: h.Generation, Action: action, Evidence: evidence})
		if errors.Is(err, state.ErrConflict) {
			return nil
		}
		if err == nil && next != nil && next.Generation > h.Generation && a.Record != nil {
			a.Record(ctx, p, next)
		}
		return err
	}
	if h.Control != nil && h.Control.Status == "pending" && !now.Before(h.Control.Deadline) && p.RestoredTargetReleaseSetID == "" {
		return advance("rollback_failed", nil)
	}
	if h.Phase != "rolling_back" && h.Phase != "migrating" && !now.Before(h.Deadline) {
		return advance("", nil)
	}
	switch h.Phase {
	case "restoring":
		if h.CheckedAt != nil && now.Sub(*h.CheckedAt) < api.ProjectReleaseVerificationInterval {
			return nil
		}
		reader, ok := store.(state.ProjectReleaseSetReader)
		if !ok || a.RecoveryProbe == nil {
			return advance("rollback_failed", nil)
		}
		graph, readErr := reader.ProjectReleaseSetByID(ctx, p.AccountID, p.ProjectID, p.ToEnvironment, p.PreviousTargetReleaseSetID)
		if readErr != nil {
			if errors.Is(readErr, state.ErrNotFound) || errors.Is(readErr, state.ErrConflict) {
				return advance("rollback_failed", nil)
			}
			return readErr
		}
		if !graph.Active && (graph.ExpiresAt == nil || !now.Before(*graph.ExpiresAt)) {
			return advance("rollback_failed", nil)
		}
		probeCtx, cancel := context.WithTimeout(ctx, api.ProjectPromotionHealthRoundTimeout)
		evidence, probeErr := a.RecoveryProbe(probeCtx, p, h.Policy, graph)
		cancel()
		if probeErr != nil {
			evidence = nil
		}
		return advance("", evidence)
	case "migrating":
		if !now.Before(h.Deadline) {
			return advance("", nil)
		}
		if err = a.Prepare(ctx, p, workloads); err != nil {
			if errors.Is(err, state.ErrConflict) {
				return advance("conflict", nil)
			}
			return nil
		}
		migrations, ok := store.(state.ProjectPromotionMigrationStore)
		if !ok {
			return fmt.Errorf("promotion migration storage unavailable")
		}
		next, migrationErr := migrations.ReconcileProjectEnvironmentPromotionMigration(ctx, p.AccountID, p.ID)
		if errors.Is(migrationErr, state.ErrConflict) {
			return advance("conflict", nil)
		}
		if migrationErr == nil && next != nil && a.Record != nil {
			a.Record(ctx, p, next)
		}
		return migrationErr
	case "preflight", "verifying":
		if h.CheckedAt != nil && now.Sub(*h.CheckedAt) < api.ProjectReleaseVerificationInterval {
			return nil
		}
		if h.Phase == "preflight" {
			if err = a.Prepare(ctx, p, workloads); err != nil {
				if errors.Is(err, state.ErrConflict) {
					return advance("conflict", nil)
				}
				return advance("", nil)
			}
			_, workloads, err = store.ProjectEnvironmentPromotionByID(ctx, p.AccountID, p.ProjectSlug, p.ToEnvironment, p.ID)
			if err != nil {
				return err
			}
		} else if err = a.Verify(ctx, p); err != nil {
			return advance("fail", nil)
		}
		probeCtx, cancel := context.WithTimeout(ctx, api.ProjectPromotionHealthRoundTimeout)
		evidence, probeErr := a.Probe(probeCtx, p, workloads, h.Policy)
		cancel()
		if probeErr != nil {
			evidence = nil
		}
		return advance("", evidence)
	case "cutover":
		if err = a.Cutover(ctx, p, workloads); errors.Is(err, state.ErrConflict) {
			return advance("conflict", nil)
		}
		return err
	case "finalizing":
		if err = a.Verify(ctx, p); err != nil {
			return advance("fail", nil)
		}
		return advance("complete", nil)
	case "rolling_back":
		if len(api.ProjectPromotionWorkers(&h.Policy)) > 0 && h.Control == nil {
			deadline := h.Deadline.Add(api.ProjectReleaseVerificationTimeout)
			if p.RollbackStartedAt != nil {
				deadline = p.RollbackStartedAt.Add(api.ProjectReleaseVerificationTimeout)
			}
			if !now.Before(deadline) {
				return advance("rollback_failed", nil)
			}
		}
		if p.RollbackStatus != "rolled_back" {
			if err = a.Rollback(ctx, p); err != nil {
				if errors.Is(err, state.ErrConflict) {
					return advance("rollback_failed", nil)
				}
				return err
			}
		}
		return advance("rolled_back", nil)
	}
	return nil
}

func HealthProbeInputs(ctx context.Context, store state.Store, p state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload, policy api.ProjectPromotionHealthPolicy) (api.ProjectSubmissionReceipt, state.ProjectReleaseSet, error) {
	reader, ok := store.(state.ProjectReleaseSetReader)
	if !ok {
		return api.ProjectSubmissionReceipt{}, state.ProjectReleaseSet{}, fmt.Errorf("retained release inventory unavailable")
	}
	baseline, err := reader.ProjectReleaseSetByID(ctx, p.AccountID, p.ProjectID, p.ToEnvironment, p.PreviousTargetReleaseSetID)
	if err != nil {
		return api.ProjectSubmissionReceipt{}, baseline, err
	}
	receipt := api.ProjectSubmissionReceipt{Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: &api.ProjectSafeRelease{Verification: &api.ProjectReleaseVerification{Probes: policy.Probes}}}}}
	bySlug := map[string]string{}
	for _, probe := range policy.Probes {
		bySlug[probe.Slug] = probe.AppID
	}
	for _, w := range workloads {
		if promotionWorkerSlug(policy, w.WorkloadSlug) {
			continue
		}
		appID := bySlug[w.WorkloadSlug]
		if appID == "" || w.TargetDeploymentID == "" {
			return receipt, baseline, fmt.Errorf("promotion candidate checkpoint incomplete")
		}
		receipt.Apply.Builds = append(receipt.Apply.Builds, api.AppliedBuild{AppID: appID, DeploymentID: w.TargetDeploymentID})
	}
	return receipt, baseline, nil
}

func releaseHealthLease(ctx context.Context, health state.ProjectPromotionHealthStore, id, token string) error {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ProjectPromotionHealthPollInterval)
	defer cancel()
	return health.ReleaseProjectEnvironmentPromotionHealth(releaseCtx, id, token)
}
