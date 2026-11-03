package apidserver

// adr: 20261101030512 — a production manifest deploy's event subscriptions
// take effect when the deployment serves all production traffic.

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// runEventSubscriptionActivationWorker applies recorded subscription intents
// once their deployment is live at full production traffic, as the cron
// activation worker does for staged crons. Durable guards (the intent row
// lock and its prepared status) make concurrent apid workers safe.
func (s *server) runEventSubscriptionActivationWorker(ctx context.Context) {
	store, ok := s.store.(state.EventSubscriptionActivationStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		changes, err := store.ReconcileEventSubscriptionActivations(ctx)
		for _, change := range changes {
			s.announceEventSubscriptionActivation(ctx, change)
		}
		if err != nil && ctx.Err() == nil {
			s.log.Warn("event subscription activation reconciliation failed; will retry", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) announceEventSubscriptionActivation(ctx context.Context, change state.EventSubscriptionActivationChange) {
	account := change.AccountID
	for kind, ids := range map[string][]string{"disabled": change.Disabled, "enabled": change.Enabled} {
		for _, id := range ids {
			_ = s.notif.Notify(ctx, db.NotifyEventSubscriptionChanged,
				fmt.Sprintf(`{"kind":"%s","app_id":"%s","subscription_id":"%s"}`, kind, change.AppID, id))
			s.audit.Emit(ctx, "event.subscription."+kind, &account, map[string]any{
				"subscription_id": id, "app_id": change.AppID, "deployment_id": change.DeploymentID,
				"source_ref": true, "reason": "deployment_live",
			})
		}
	}
}
