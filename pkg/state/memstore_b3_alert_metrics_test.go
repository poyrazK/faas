package state_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestMemStoreB3AlertMetricStubs(t *testing.T) {
	m := state.NewMemStore()
	ctx := t.Context()
	since := time.Now().Add(-time.Hour)
	if _, err := m.CountNewErrorFingerprintsSince(ctx, "acct", "app", since); !errors.Is(err, (*state.ErrMemStoreB3AlertMetrics)) {
		t.Fatalf("CountNewErrorFingerprintsSince: %v", err)
	}
	if _, err := m.ColdWakeRatePctSince(ctx, "acct", "app", since); !errors.Is(err, (*state.ErrMemStoreB3AlertMetrics)) {
		t.Fatalf("ColdWakeRatePctSince: %v", err)
	}
	if _, err := m.DailyCostCents(ctx, "acct", "app", time.Now()); !errors.Is(err, (*state.ErrMemStoreB3AlertMetrics)) {
		t.Fatalf("DailyCostCents: %v", err)
	}
}
