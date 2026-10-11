package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
)

func (s *MemStore) CreateWorkflowReadinessSubscription(ctx context.Context, account, tenant, app string, req api.OperationWorkflowReadinessSubscriptionRequest, operator bool) (api.OperationWorkflowReadinessSubscription, error) {
	return createReadinessSubscription(ctx, s, account, tenant, app, req, operator)
}
func (s *MemStore) ListWorkflowReadinessSubscriptions(ctx context.Context, account, tenant, app, scope string, operator bool) (api.OperationWorkflowReadinessSubscriptionsResponse, error) {
	return listReadinessSubscriptions(ctx, s, account, tenant, app, scope, operator)
}
func (s *MemStore) DeleteWorkflowReadinessSubscription(ctx context.Context, account, tenant, app, scope, id string, operator bool) error {
	return deleteReadinessSubscription(ctx, s, account, tenant, app, scope, id, operator)
}
func (s *MemStore) WorkflowReadinessInbox(ctx context.Context, account, tenant, app, scope, id string, after int64, limit int, operator bool) (api.OperationWorkflowReadinessInboxResponse, error) {
	return readinessSubscriptionInbox(ctx, s, account, tenant, app, scope, id, after, limit, operator)
}
func (s *MemStore) EvaluateDueWorkflowReadinessSubscriptions(ctx context.Context) (int, error) {
	return evaluateDueReadinessSubscriptions(ctx, s)
}

func (s *MemStore) UpdateWorkflowReadinessNotification(ctx context.Context, account, tenant, app, scope, id string, sequence int64, actor string, req api.OperationWorkflowReadinessCoordinationRequest, operator bool) (api.OperationWorkflowReadinessNotification, error) {
	return updateReadinessNotification(ctx, s, account, tenant, app, scope, id, sequence, actor, req, operator)
}
func (s *MemStore) CheckWorkflowReadinessNotification(ctx context.Context, account, tenant, app, scope, id string, sequence int64, operator bool) (api.OperationWorkflowReadinessNotificationCheck, error) {
	return checkReadinessNotification(ctx, s, account, tenant, app, scope, id, sequence, operator)
}
