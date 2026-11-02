package apidserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type planPurgeStore struct {
	accounts []state.Account
	cutoffs  map[uuid.UUID]time.Time
}

func (s *planPurgeStore) ListAllAccounts(context.Context) ([]state.Account, error) {
	return s.accounts, nil
}

func (s *planPurgeStore) ListAppErrorFingerprintsForPurge(context.Context, sqlc.ListAppErrorFingerprintsForPurgeParams) ([]uuid.UUID, error) {
	return nil, nil
}

func (s *planPurgeStore) DeleteAppErrorsByIDs(context.Context, []uuid.UUID) error { return nil }

func (s *planPurgeStore) DeleteAppErrorRequestsOlderThan(_ context.Context, accountID uuid.UUID, olderThan time.Time) error {
	s.cutoffs[accountID] = olderThan
	return nil
}

// Each account's request samples (headers included) age out on its own
// plan's retention. The purge used the oldest horizon across every plan, so
// all accounts kept them 90 days.
func TestAppErrorsPurgeUsesEachAccountsPlan(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	free, scale := uuid.New(), uuid.New()
	store := &planPurgeStore{
		accounts: []state.Account{{ID: free.String(), Plan: api.PlanFree}, {ID: scale.String(), Plan: api.PlanScale}},
		cutoffs:  map[uuid.UUID]time.Time{},
	}
	apidserver.PurgeAppErrorsOnce(context.Background(), store, func() time.Time { return now })
	for id, plan := range map[uuid.UUID]api.Plan{free: api.PlanFree, scale: api.PlanScale} {
		want := now.Add(-time.Duration(plan.AppErrorsRetentionDays()) * 24 * time.Hour)
		if got := store.cutoffs[id]; !got.Equal(want) {
			t.Errorf("%s cutoff = %s, want %s (%d days)", plan, got, want, plan.AppErrorsRetentionDays())
		}
	}
	if store.cutoffs[free].Equal(store.cutoffs[scale]) {
		t.Fatal("every plan got the same horizon")
	}
}
