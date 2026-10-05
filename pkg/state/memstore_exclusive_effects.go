// adr: 488
package state

import "context"

func (s *MemStore) operationEffectSurfaceLinkedLocked(op ExclusiveOperation) bool {
	for id, tenant := range s.platformTenantBySurface {
		surface := s.tenantSurfaces[id]
		if tenant == op.PlatformTenantID && surface.AccountID == op.AccountID && surface.AppID == op.AppID && surface.Status == SurfaceStatusActive {
			return true
		}
	}
	return false
}

func (s *MemStore) OperationEffectDeliveryAllowed(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delivery, ok := s.appWebhookDeliveries[id]
	for opID, effects := range s.exclusiveEffects {
		for _, effect := range effects {
			if effect.Record.DeliveryID != id {
				continue
			}
			op := s.exclusiveOperations[opID]
			app := s.apps[op.AppID]
			if !ok || delivery.Event != OperationEffectEvent || op.State != "completed" || op.Generation != effect.Record.Generation || op.AppID != delivery.AppID || op.AccountID != delivery.AccountID || effect.Record.WebhookID != delivery.WebhookID ||
				app.ID == "" || app.AccountID != op.AccountID || app.Status == AppDeleted || s.accounts[op.AccountID].Status != AccountActive {
				return false, nil
			}
			if !operationEffectDestinationMatches(op, s.appWebhooks[delivery.WebhookID]) {
				return false, nil
			}
			if op.PlatformTenantID != "" {
				tenant := s.platformTenants[op.PlatformTenantID]
				if tenant.AccountID != op.AccountID || tenant.Status != PlatformTenantActive || !s.operationEffectSurfaceLinkedLocked(op) {
					return false, nil
				}
			}
			return true, nil
		}
	}
	for attemptKey, effects := range s.workflowOperationEffects {
		for _, effect := range effects {
			if canonicalMemUUID(effect.Record.DeliveryID) != canonicalMemUUID(id) {
				continue
			}
			if !ok || delivery.Event != OperationEffectEvent || effect.Record.WebhookID != delivery.WebhookID ||
				canonicalMemUUID(delivery.AccountID) == "" || canonicalMemUUID(delivery.AppID) == "" {
				return false, nil
			}
			app, appExists := s.apps[delivery.AppID]
			account, accountExists := s.accounts[delivery.AccountID]
			if !appExists || app.ID == "" || app.Status == AppDeleted || !accountExists || account.Status != AccountActive ||
				canonicalMemUUID(app.AccountID) != canonicalMemUUID(delivery.AccountID) {
				return false, nil
			}
			hook := s.appWebhooks[delivery.WebhookID]
			if hook.ID == "" {
				for hookID, candidate := range s.appWebhooks {
					if canonicalMemUUID(hookID) == canonicalMemUUID(delivery.WebhookID) {
						hook = candidate
						break
					}
				}
			}
			run, runExists := s.workflowRuns[attemptKey.runID]
			if !runExists || canonicalMemUUID(run.AppID) != canonicalMemUUID(delivery.AppID) {
				return false, nil
			}
			if run.PlatformTenantID != "" {
				tenant, tenantExists := s.platformTenants[run.PlatformTenantID]
				op := ExclusiveOperation{AppID: run.AppID, AccountID: delivery.AccountID, PlatformTenantID: run.PlatformTenantID}
				if !tenantExists || canonicalMemUUID(tenant.AccountID) != canonicalMemUUID(delivery.AccountID) || tenant.Status != PlatformTenantActive || !s.operationEffectSurfaceLinkedLocked(op) {
					return false, nil
				}
			}
			return managedWorkflowEffectDestinationMatches(delivery.AccountID, delivery.AppID, run.PlatformTenantID, hook), nil
		}
	}
	return false, ErrNotOperationEffect
}
