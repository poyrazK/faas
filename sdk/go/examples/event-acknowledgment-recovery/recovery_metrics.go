package recovery

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/poyrazK/faas/sdk/go/preview"
)

// RecoveryHealthMetricsHost separates explicit polling from a cache-only server.
// Construct once per trusted owner scope; the application supervises both loops.
type RecoveryHealthMetricsHost struct {
	Server           *http.Server
	metrics          *preview.EventAcknowledgmentIntentHealthMetrics
	db               *sql.DB
	accountID, appID string
}

// NewRecoveryHealthMetricsHost starts no goroutine, SQL read or listener. Supply
// a configured PostgreSQL pool; keep it alive until Run and HTTP shutdown join.
func NewRecoveryHealthMetricsHost(db *sql.DB, accountID, appID string) (*RecoveryHealthMetricsHost, error) {
	if db == nil {
		return nil, preview.ErrInvalidEventConsumerRequest
	}
	metrics, err := preview.NewEventAcknowledgmentIntentHealthMetrics(accountID, appID, preview.EventAcknowledgmentIntentHealthMetricsOptions{MaxAge: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	handler, err := preview.NewEventAcknowledgmentIntentHealthMetricsHandler(metrics, preview.EventAcknowledgmentIntentHealthPrometheusOptions{})
	if err != nil {
		return nil, err
	}
	return &RecoveryHealthMetricsHost{
		Server: &http.Server{
			Addr: "127.0.0.1:9468", Handler: handler,
			ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second,
			WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
			MaxHeaderBytes: 8192,
		},
		metrics: metrics, db: db, accountID: accountID, appID: appID,
	}, nil
}

// Run polls sequentially until ctx cancellation, joining the active database
// call and clearing evidence before return. Driver deadlines remain host-owned.
func (h *RecoveryHealthMetricsHost) Run(ctx context.Context) error {
	return preview.WatchEventAcknowledgmentIntentHealthMetrics(ctx, h.db, h.accountID, h.appID, h.metrics,
		preview.EventAcknowledgmentIntentHealthMetricsWatchOptions{
			Health:       preview.EventAcknowledgmentIntentHealthOptions{ScanLimit: 1000, StallAfter: 5 * time.Minute},
			PollInterval: 5 * time.Second,
			QueryTimeout: 5 * time.Second,
		})
}
