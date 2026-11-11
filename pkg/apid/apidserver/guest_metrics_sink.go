package apidserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"

	"github.com/onebox-faas/faas/pkg/apid/receivers"
	"github.com/onebox-faas/faas/pkg/otlpmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// guestMetricsSink stores in-guest metric exports (ADR-20261108160000 slice
// B) through the same translation, name admission and caps as the public
// OTLP metrics endpoint (postOTLPMetrics), for the app vmmd resolved from
// the instance.
type guestMetricsSink struct{ s *server }

var _ receivers.GuestMetricsSink = guestMetricsSink{}

func (k guestMetricsSink) StoreGuestMetrics(ctx context.Context, accountID, appID string, export *colmetricspb.ExportMetricsServiceRequest) (int64, int64, error) {
	if !k.s.customMetricHistoryEnabled {
		return 0, 0, receivers.ErrGuestMetricsDisabled
	}
	store, ok := k.s.store.(state.CustomMetricKindStore)
	if !ok {
		return 0, 0, errors.New("custom metric storage unavailable")
	}
	app, err := k.s.store.AppByID(ctx, appID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return 0, 0, receivers.ErrGuestMetricsDisabled
		}
		return 0, 0, fmt.Errorf("load app: %w", err)
	}
	if app.AccountID != accountID {
		// vmmd's identity and the app row disagree; refuse rather than
		// write another account's metrics.
		return 0, 0, receivers.ErrGuestMetricsDisabled
	}
	owner, err := state.AppResourceAccount(ctx, k.s.store, app)
	if err != nil {
		return 0, 0, fmt.Errorf("load app owner: %w", err)
	}
	if !owner.Plan.CustomMetricsAllowed() {
		return 0, 0, receivers.ErrGuestMetricsDisabled
	}
	res := otlpmetrics.Translate(export, time.Now().UTC())
	if err := k.s.storeOTLPPoints(ctx, store, app.ID, k.s.customMetricCapsFor(ctx, owner.Plan, app), &res); err != nil {
		return 0, 0, err
	}
	return int64(len(res.Points)), res.Rejected, nil
}
