package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) cronStagingHandlers() deploystaging.CronHandlers {
	store, _ := s.store.(state.DeploymentCronStagingStore)
	return deploystaging.CronHandlers{Store: store, LoadApp: s.loadApp, Changed: func(ctx context.Context, accountID, appID, operation string) {
		_ = s.notif.Notify(ctx, db.NotifyCronChanged, `{"kind":"reconciled","app_id":"`+appID+`"}`)
		s.audit.Emit(ctx, "cron.set_updated", &accountID, map[string]any{"app_id": appID, "source": "deploy_staging", "operation": operation})
	}}
}
func (s *server) getDeploymentCronStagingSnapshot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.cronStagingHandlers().Snapshot(w, r, acct)
}
func (s *server) applyDeploymentCronStaging(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.cronStagingHandlers().Apply(w, r, acct)
}

func (s *server) runCronActivationWorker(ctx context.Context) {
	store, ok := s.store.(state.DeploymentCronActivationStore)
	if !ok {
		return
	}
	deploystaging.RunCronActivationWorker(ctx, store, s.cronStagingHandlers().Changed, func() { s.log.Warn("deployment cron activation reconciliation failed; will retry") })
}
