package recovery

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

// RecoveryWorkerMetricsHost adds actual pass activity to the existing independent
// health observer. Construction starts no SQL, pass, timer, goroutine or listener.
type RecoveryWorkerMetricsHost struct {
	Server *http.Server
	health *RecoveryHealthMetricsHost
	worker *faas.EventAcknowledgmentIntentRecoveryWorkerMetrics
}

// NewRecoveryWorkerMetricsHost uses the same protected 9468 endpoint as the
// health-only example; deploy one host, not both listeners for the same scope.
func NewRecoveryWorkerMetricsHost(db *sql.DB, accountID, appID string) (*RecoveryWorkerMetricsHost, error) {
	health, err := NewRecoveryHealthMetricsHost(db, accountID, appID)
	if err != nil {
		return nil, err
	}
	worker, err := faas.NewEventAcknowledgmentIntentRecoveryWorkerMetrics(accountID, appID,
		faas.EventAcknowledgmentIntentRecoveryWorkerMetricsOptions{MaxIdle: time.Minute, MaxPassDuration: 5 * time.Minute})
	if err != nil {
		return nil, err
	}
	handler, err := faas.NewEventAcknowledgmentIntentRecoveryMetricsHandler(health.metrics, worker)
	if err != nil {
		return nil, err
	}
	health.Server.Handler = handler
	return &RecoveryWorkerMetricsHost{Server: health.Server, health: health, worker: worker}, nil
}

// RunHealth starts only the independent metadata observer. The application must
// separately schedule RecoverCallbacks, supervise HTTP and join all three.
func (h *RecoveryWorkerMetricsHost) RunHealth(ctx context.Context) error {
	return h.health.Run(ctx)
}

// RecoverCallbacks runs one finite monitored pass. Process progress and err
// before choosing another pass; metrics authorize no retry or scheduling action.
func (h *RecoveryWorkerMetricsHost) RecoverCallbacks(ctx context.Context, client *faas.Client) (faas.EventAcknowledgmentIntentRecoveryPassResult, error) {
	return faas.RecoverEventAcknowledgmentIntentsWithMetrics(ctx, h.health.db, client, h.health.accountID, h.health.appID,
		faas.EventAcknowledgmentIntentRecoveryPassOptions{
			MaxAttempts: 100, Concurrency: 4, ClaimDuration: 30 * time.Second,
			RetryPolicy: &faas.EventAcknowledgmentIntentRetryPolicy{InitialBackoff: time.Second, MaxBackoff: 5 * time.Minute},
		}, h.worker)
}
