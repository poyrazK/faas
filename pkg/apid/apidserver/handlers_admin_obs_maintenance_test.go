package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// ADR-20261015154500000: admin health retains collection configuration and
// exact cleanup backlog; unavailable reads never become successful zeros.
func TestObsAlertMaintenanceHealthAndBacklog(t *testing.T) {
	e := gapEnv(t)
	store := *(*e.S()).Store()
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := now.Add(-api.ObsAlertHistoryRetention - time.Hour)
	seedObsAlert(t, store, old, true)
	rec := e.Do(t, "GET", "/v1/admin/obs/health", nil, nil)
	var health api.ObsHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || health.AlertHistory == nil || health.AlertHistory.CollectionState != "disabled" || health.AlertRetention == nil || health.AlertRetention.State != "never_run" || health.AlertRetention.ExpiredEpisodes != 1 || health.AlertRetention.OldestExpiredAt == nil || health.AlertRetention.LastPrunedEpisodes != nil {
		t.Fatalf("initial: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := store.PruneOperatorAlertHistory(context.Background(), now.Add(-api.ObsAlertHistoryRetention), api.ObsAlertHistoryPruneBatch); err != nil {
		t.Fatal(err)
	}
	rec = e.Do(t, "GET", "/v1/admin/obs/health", nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || health.AlertRetention.State != "current" || health.AlertRetention.ExpiredEpisodes != 0 || health.AlertRetention.LastPrunedEpisodes == nil || *health.AlertRetention.LastPrunedEpisodes != 1 || health.AlertRetention.LastSuccessAt == nil {
		t.Fatalf("cleaned: %d %s", rec.Code, rec.Body.String())
	}
}

type obsRetentionFailureStore struct{ Store state.Store }

func (s *obsRetentionFailureStore) OperatorAlertRetentionStatus(context.Context, time.Time) (state.OperatorAlertRetention, error) {
	return state.OperatorAlertRetention{}, errors.New("private retention database detail")
}

func TestObsAlertRetentionFailureIsUnavailable(t *testing.T) {
	e := gapEnv(t)
	*(*e.S()).Store() = statetest.NewStore(&obsRetentionFailureStore{Store: *(*e.S()).Store()})
	rec := e.Do(t, "GET", "/v1/admin/obs/health", nil, nil)
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "private retention") || strings.Contains(rec.Body.String(), "expired_episodes") {
		t.Fatalf("failure: %d %s", rec.Code, rec.Body.String())
	}
}
