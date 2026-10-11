package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
)

func (s *PgStore) CreateWorkflowReadinessSubscription(ctx context.Context, account, tenant, app string, req api.OperationWorkflowReadinessSubscriptionRequest, operator bool) (api.OperationWorkflowReadinessSubscription, error) {
	return createReadinessSubscription(ctx, s, account, tenant, app, req, operator)
}
func (s *PgStore) ListWorkflowReadinessSubscriptions(ctx context.Context, account, tenant, app, scope string, operator bool) (api.OperationWorkflowReadinessSubscriptionsResponse, error) {
	return listReadinessSubscriptions(ctx, s, account, tenant, app, scope, operator)
}
func (s *PgStore) DeleteWorkflowReadinessSubscription(ctx context.Context, account, tenant, app, scope, id string, operator bool) error {
	return deleteReadinessSubscription(ctx, s, account, tenant, app, scope, id, operator)
}
func (s *PgStore) WorkflowReadinessInbox(ctx context.Context, account, tenant, app, scope, id string, after int64, limit int, operator bool) (api.OperationWorkflowReadinessInboxResponse, error) {
	return readinessSubscriptionInbox(ctx, s, account, tenant, app, scope, id, after, limit, operator)
}
func (s *PgStore) EvaluateDueWorkflowReadinessSubscriptions(ctx context.Context) (int, error) {
	return evaluateDueReadinessSubscriptions(ctx, s)
}
