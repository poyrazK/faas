package state

// MemStore deliberately keeps the production-only observability tables as
// sentinel stubs (the existing app_errors and request_telemetry surfaces use
// the same policy). B3's evaluator tests inject a narrow wrapper with fixed
// metric values; production uses the PgStore implementations.

import (
	"context"
	"errors"
	"time"
)

var errMemStoreB3AlertMetrics = errors.New("state: MemStore does not implement B3 alert metrics — run the evaluator against pgtest")

func (m *MemStore) CountNewErrorFingerprintsSince(_ context.Context, _ string, _ string, _ time.Time) (int, error) {
	return 0, errMemStoreB3AlertMetrics
}

func (m *MemStore) ColdWakeRatePctSince(_ context.Context, _ string, _ string, _ time.Time) (float64, error) {
	return 0, errMemStoreB3AlertMetrics
}

func (m *MemStore) DailyCostCents(_ context.Context, _ string, _ string, _ time.Time) (int64, error) {
	return 0, errMemStoreB3AlertMetrics
}
