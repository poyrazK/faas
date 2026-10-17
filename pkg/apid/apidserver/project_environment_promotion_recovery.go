package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	promotionapi "github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) recoverProjectPromotionMigration(w http.ResponseWriter, r *http.Request, account state.Account) {
	promotionapi.HandleMigrationRecovery(w, r, account, s.store, func(receipt *api.ProjectPromotionMigrationRecovery) {
		s.audit.Emit(r.Context(), "project.environment.migration_recovery_requested", &account.ID, map[string]any{"promotion_id": r.PathValue("promotion"), "request_id": receipt.Request.RequestID, "task_id": receipt.Request.ExpectedTaskID, "action": receipt.Request.Action})
	})
}
func (s *server) probePromotionRecovery(ctx context.Context, p state.ProjectEnvironmentPromotion, policy api.ProjectPromotionHealthPolicy, graph state.ProjectReleaseSet) ([]api.ProjectReleaseProbeEvidence, error) {
	sender, ok := s.routeProbes.(deploystaging.ProjectCriticalProbeSender)
	if !ok || !sender.Configured() || s.notif == nil {
		return nil, state.ErrConflict
	}
	receipt := promotionapi.BaselineProbeInputs(policy, graph)
	return deploystaging.ProbeProjectReleaseForScope(ctx, p.ToEnvironment, receipt, graph, sender, func(ctx context.Context, payload string) error {
		return s.notif.Notify(ctx, db.NotifyRouteProbeChallenge, payload)
	}, routeProbeChallengeDelay)
}

func (s *server) controlProjectPromotion(w http.ResponseWriter, r *http.Request, account state.Account) {
	promotionapi.HandleControl(w, r, account, s.store, func(receipt *api.ProjectPromotionControl) {
		s.audit.Emit(r.Context(), "project.environment.promotion_control_requested", &account.ID, map[string]any{"promotion_id": r.PathValue("promotion"), "request_id": receipt.Request.RequestID, "action": receipt.Action, "status": receipt.Status})
	})
}
