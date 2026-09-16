package githubd

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

// WebhookRecoveryStore is the operator-facing subset of the durable delivery
// inbox. It deliberately excludes payload access.
type WebhookRecoveryStore interface {
	ListWebhookDeliveries(context.Context, string, int) ([]WebhookDeliveryRecord, error)
	RetryWebhookDelivery(context.Context, string) (bool, error)
}

// CheckRecoveryStore is the operator-facing subset of the Check Run outbox.
type CheckRecoveryStore interface {
	ListCheckUpdates(context.Context, string, int) ([]CheckUpdateRecord, error)
	RetryCheckUpdate(context.Context, string) (bool, error)
}

// RecoveryService decorates the normal githubd gRPC implementation with
// queue-recovery methods. Embedding preserves every existing RPC, including
// the UnimplementedService behavior when GitHub App credentials are absent.
type RecoveryService struct {
	githubdgrpc.Service
	deliveries WebhookRecoveryStore
	checks     CheckRecoveryStore
}

func NewRecoveryService(base githubdgrpc.Service, deliveries WebhookRecoveryStore, checks CheckRecoveryStore) *RecoveryService {
	if base == nil {
		base = githubdgrpc.UnimplementedService{}
	}
	return &RecoveryService{Service: base, deliveries: deliveries, checks: checks}
}

func (s *RecoveryService) ListRecoveryQueueItems(ctx context.Context, status string, limit int) (githubdgrpc.RecoveryQueueItems, error) {
	deliveries, err := s.deliveries.ListWebhookDeliveries(ctx, status, limit)
	if err != nil {
		return githubdgrpc.RecoveryQueueItems{}, err
	}
	checks, err := s.checks.ListCheckUpdates(ctx, status, limit)
	if err != nil {
		return githubdgrpc.RecoveryQueueItems{}, err
	}
	out := githubdgrpc.RecoveryQueueItems{
		Deliveries:   make([]githubdgrpc.WebhookDeliveryRecord, 0, len(deliveries)),
		CheckUpdates: make([]githubdgrpc.CheckUpdateRecord, 0, len(checks)),
	}
	for _, item := range deliveries {
		out.Deliveries = append(out.Deliveries, githubdgrpc.WebhookDeliveryRecord{
			DeliveryID: item.DeliveryID, EventType: item.EventType, Status: item.Status,
			Attempts: item.Attempts, NextAttempt: item.NextAttempt, LastError: item.LastError,
			ReceivedAt: item.ReceivedAt, ProcessedAt: item.ProcessedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	for _, item := range checks {
		out.CheckUpdates = append(out.CheckUpdates, githubdgrpc.CheckUpdateRecord{
			DeploymentID: item.DeploymentID, Generation: item.Generation, Status: item.Status,
			Attempts: item.Attempts, NextAttempt: item.NextAttempt, LastError: item.LastError,
			ProcessedAt: item.ProcessedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	return out, nil
}

func (s *RecoveryService) RetryWebhookDelivery(ctx context.Context, deliveryID string) (bool, error) {
	return s.deliveries.RetryWebhookDelivery(ctx, deliveryID)
}

func (s *RecoveryService) RetryCheckUpdate(ctx context.Context, deploymentID string) (bool, error) {
	return s.checks.RetryCheckUpdate(ctx, deploymentID)
}

func (s *RecoveryService) GetAppActivity(ctx context.Context, accountID, appID string, limit int) (githubdgrpc.AppActivity, error) {
	deliveries, ok := s.deliveries.(WebhookActivityStore)
	if !ok {
		return githubdgrpc.AppActivity{}, fmt.Errorf("githubd: app webhook activity is not configured")
	}
	checks, ok := s.checks.(CheckActivityStore)
	if !ok {
		return githubdgrpc.AppActivity{}, fmt.Errorf("githubd: app check activity is not configured")
	}
	webhooks, err := deliveries.ListWebhookDeliveriesForApp(ctx, accountID, appID, limit)
	if err != nil {
		return githubdgrpc.AppActivity{}, err
	}
	checkUpdates, err := checks.ListCheckUpdatesForApp(ctx, accountID, appID, limit)
	if err != nil {
		return githubdgrpc.AppActivity{}, err
	}
	out := githubdgrpc.AppActivity{
		Webhooks: make([]githubdgrpc.WebhookActivity, 0, len(webhooks)),
		Checks:   make([]githubdgrpc.CheckActivity, 0, len(checkUpdates)),
	}
	for _, item := range webhooks {
		out.Webhooks = append(out.Webhooks, githubdgrpc.WebhookActivity{
			EventType: item.EventType, Status: item.Status, CommitSHA: item.CommitSHA,
			ReceivedAt: item.ReceivedAt, ProcessedAt: item.ProcessedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	for _, item := range checkUpdates {
		out.Checks = append(out.Checks, githubdgrpc.CheckActivity{
			DeploymentID: item.DeploymentID, Status: item.Status, CommitSHA: item.CommitSHA,
			ProcessedAt: item.ProcessedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	return out, nil
}
