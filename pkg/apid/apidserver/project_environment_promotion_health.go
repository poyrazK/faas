package apidserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	promotionapi "github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) WithPromotionHealthEnabled(enabled bool) *server {
	s.promotionHealthEnabled = enabled
	return s
}

func (s *server) preparePromotionHealthPlan(ctx context.Context, accountID string, plan *projectEnvironmentPromotionPlan) *api.Problem {
	if !s.promotionHealthEnabled {
		return api.ErrCapacity("health-gated environment promotion is disabled pending native qualification")
	}
	if _, ok := s.store.(state.ProjectPromotionHealthStore); !ok {
		return api.ErrCapacity("promotion health storage unavailable")
	}
	if _, ok := s.store.(state.ProjectPromotionHealthScheduler); !ok {
		return api.ErrCapacity("promotion health scheduling unavailable")
	}
	if _, ok := s.store.(state.ProjectPromotionAdmissionStore); !ok {
		return api.ErrCapacity("promotion admission storage unavailable")
	}
	sender, ok := s.routeProbes.(deploystaging.ProjectCriticalProbeSender)
	if !ok || !sender.Configured() || s.notif == nil {
		return api.ErrCapacity("verified promotion requires a configured critical gateway probe sender")
	}
	if plan.FromReleaseSet == nil || plan.ToReleaseSet == nil {
		return api.ErrValidation("verified promotion requires active source and destination release graphs")
	}
	if !plan.SyncConfig || plan.ReleaseConfiguration == nil {
		return api.ErrValidation("verified promotion requires --sync-config and a pinned HTTP release configuration")
	}
	policy, err := promotionapi.PrepareHealth(ctx, s.store, accountID, plan.Sources, plan.Targets, plan.ReleaseTTLSeconds, plan.Migrate, plan.Workers)
	if err != nil {
		return api.ErrValidation(err.Error())
	}
	if plan.Migrate {
		if !s.promotionMigrationsEnabled || plan.ReleaseConfiguration.Migration == nil {
			return api.ErrValidation("migration promotion requires one declared backward-compatible release command and enabled migration admission")
		}
		if _, ok := s.store.(state.ProjectPromotionMigrationStore); !ok {
			return api.ErrCapacity("promotion migration storage unavailable")
		}
		migration := *plan.ReleaseConfiguration.Migration
		policy.Migration = &migration
	}
	if problem := promotionapi.AssignWorkerPolicy(policy, plan.ReleaseConfiguration, plan.Workers); problem != nil {
		return problem
	}

	if problem := promotionapi.RolloutProblem(s.promotionRollout, accountID, plan.ProjectID, policy, plan.ReleaseConfiguration); problem != nil {
		return problem
	}
	plan.HealthPolicy = policy
	return nil
}

func (s *server) dispatchProjectEnvironmentPromotion(ctx context.Context, acct state.Account, p state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload, plan projectEnvironmentPromotionPlan) (api.ProjectEnvironmentPromotionResponse, *api.Problem) {
	if plan.HealthPolicy != nil {
		return projectEnvironmentPromotionResponse(p, workloads), nil
	}
	return s.executeProjectEnvironmentPromotion(ctx, acct, p, workloads, plan)
}

func promotionResponseHTTPStatus(response api.ProjectEnvironmentPromotionResponse) int {
	if response.Status == "running" {
		return http.StatusAccepted
	}
	return http.StatusOK
}

func (s *server) runPromotionHealthWorker(ctx context.Context) {
	promotionapi.RunHealthWorker(ctx, s.store, s.promotionHealthActions(), func(job *state.PromotionHealthJob, err error) {
		if job == nil {
			s.log.Warn("promotion health inventory failed", "err", err)
		} else {
			s.log.Warn("promotion health reconciliation failed", "promotion_id", job.PromotionID, "err", err)
		}
	})
}

func (s *server) promotionHealthActions() promotionapi.HealthActions {
	planFor := func(ctx context.Context, p state.ProjectEnvironmentPromotion, w []state.ProjectEnvironmentPromotionWorkload) (state.Account, projectEnvironmentPromotionPlan, error) {
		// Resolve the persisted project's account: in an adopted organization
		// p.AccountID is the organization, which has no account row, so a
		// creator lookup failed every round and no company promotion could
		// complete.
		acct, err := state.ProjectResourceAccountByID(ctx, s.store, p.ProjectID)
		if err != nil {
			return acct, projectEnvironmentPromotionPlan{}, err
		}
		plan, problem := s.buildProjectEnvironmentPromotionResumePlan(ctx, acct, p, w)
		if problem != nil {
			return acct, plan, fmt.Errorf("%w: promotion inputs changed", state.ErrConflict)
		}
		return acct, plan, nil
	}
	return promotionapi.HealthActions{
		RecoveryProbe: s.probePromotionRecovery,
		RecoveryRecord: func(ctx context.Context, job state.PromotionHealthJob, r *api.ProjectPromotionMigrationRecovery) {
			s.audit.Emit(ctx, "project.environment.migration_recovery_completed", &job.AccountID, map[string]any{"promotion_id": job.PromotionID, "request_id": r.Request.RequestID, "status": r.Status, "task_id": r.Request.ExpectedTaskID})
		},
		Record: s.recordPromotionHealth,
		Prepare: func(ctx context.Context, p state.ProjectEnvironmentPromotion, w []state.ProjectEnvironmentPromotionWorkload) error {
			acct, plan, err := planFor(ctx, p, w)
			if err != nil {
				return err
			}
			_, problem := s.executeProjectEnvironmentPromotion(ctx, acct, p, w, plan)
			if problem != nil {
				return fmt.Errorf("promotion preparation: %s", problem.Title)
			}
			return nil
		},
		Cutover: func(ctx context.Context, p state.ProjectEnvironmentPromotion, w []state.ProjectEnvironmentPromotionWorkload) error {
			acct, plan, err := planFor(ctx, p, w)
			if err != nil {
				return err
			}
			_, problem := s.activateProjectEnvironmentPromotionGraph(ctx, acct, p, plan)
			if problem != nil {
				if problem.Status == http.StatusConflict {
					return state.ErrConflict
				}
				return fmt.Errorf("promotion cutover: %s", problem.Title)
			}
			return nil
		},
		Verify: func(ctx context.Context, p state.ProjectEnvironmentPromotion) error {
			return promotionapi.VerifyRecords(ctx, s.store, state.Account{ID: p.AccountID}, p)
		},
		Rollback: func(ctx context.Context, p state.ProjectEnvironmentPromotion) error {
			return s.autoRollbackProjectEnvironmentPromotion(ctx, state.Account{ID: p.AccountID}, p)
		},
		Probe: s.probePromotionHealth,
	}
}

func (s *server) recordPromotionHealth(ctx context.Context, p state.ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth) {
	if event, fields := promotionapi.HealthAudit(p, h); event != "" {
		s.audit.Emit(ctx, event, &p.AccountID, fields)
	}
}

func (s *server) probePromotionHealth(ctx context.Context, p state.ProjectEnvironmentPromotion, w []state.ProjectEnvironmentPromotionWorkload, policy api.ProjectPromotionHealthPolicy) ([]api.ProjectReleaseProbeEvidence, error) {
	sender, ok := s.routeProbes.(deploystaging.ProjectCriticalProbeSender)
	if !ok || s.notif == nil {
		return nil, fmt.Errorf("critical gateway probes unavailable")
	}
	receipt, baseline, err := promotionapi.HealthProbeInputs(ctx, s.store, p, w, policy)
	if err != nil {
		return nil, err
	}
	return deploystaging.ProbeProjectReleaseForScope(ctx, p.ToEnvironment, receipt, baseline, sender, func(ctx context.Context, payload string) error {
		return s.notif.Notify(ctx, db.NotifyRouteProbeChallenge, payload)
	}, routeProbeChallengeDelay)
}

func (s *server) WithPromotionMigrationsEnabled(enabled bool) *server {
	s.promotionMigrationsEnabled = enabled
	return s
}
