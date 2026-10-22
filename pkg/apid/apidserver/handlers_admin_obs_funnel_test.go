package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type obsOverviewProjectionStore struct {
	Store   state.Store
	calls   int
	failure error
}

func (s *obsOverviewProjectionStore) OperatorOverview(ctx context.Context, now time.Time) (api.ObsOverviewResponse, error) {
	s.calls++
	if s.failure != nil {
		return api.ObsOverviewResponse{}, s.failure
	}
	return s.Store.OperatorOverview(ctx, now)
}

func (s *obsOverviewProjectionStore) ListAllApps(context.Context) ([]state.App, error) {
	return nil, errors.New("overview must not scan fleet apps")
}

func (s *obsOverviewProjectionStore) ListAllInstances(context.Context) ([]state.Instance, error) {
	return nil, errors.New("overview must not scan fleet instances")
}

func TestObsOverviewUsesSnapshotProjection(t *testing.T) {
	e := gapEnv(t)
	fake := &obsOverviewProjectionStore{Store: *(*e.S()).Store()}
	*(*e.S()).Store() = statetest.NewStore(fake)
	rec := e.Do(t, "GET", "/v1/admin/obs/overview", nil, nil)
	var got api.ObsOverviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || fake.calls != 1 || got.Totals.AccountsActive != 1 || got.BetaFunnel14d.AccountsCreated != 1 || got.RecentFailures1h == nil || got.TopRateLimitedAccounts24h == nil || got.NodeHealth == nil {
		t.Fatalf("calls=%d status=%d body=%s", fake.calls, rec.Code, rec.Body.String())
	}
}

func TestObsOverviewSnapshotFailureIsUnavailable(t *testing.T) {
	e := gapEnv(t)
	fake := &obsOverviewProjectionStore{Store: *(*e.S()).Store(), failure: errors.New("private database detail")}
	*(*e.S()).Store() = statetest.NewStore(fake)
	rec := e.Do(t, "GET", "/v1/admin/obs/overview", nil, nil)
	if rec.Code != http.StatusServiceUnavailable || fake.calls != 1 || strings.Contains(rec.Body.String(), "private database detail") {
		t.Fatalf("calls=%d status=%d body=%s", fake.calls, rec.Code, rec.Body.String())
	}
}
