package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type obsReadProjectionStore struct {
	Store        state.Store
	legacyReads  atomic.Int32
	tenantError  error
	profileError error
}

func (s *obsReadProjectionStore) ListApps(context.Context, string) ([]state.App, error) {
	s.legacyReads.Add(1)
	return nil, errors.New("legacy app scan")
}

func (s *obsReadProjectionStore) ListDeploymentsForApp(context.Context, string, int, int) ([]state.Deployment, error) {
	s.legacyReads.Add(1)
	return nil, errors.New("legacy deployment history scan")
}

func (s *obsReadProjectionStore) ListAllApps(context.Context) ([]state.App, error) {
	s.legacyReads.Add(1)
	return nil, errors.New("legacy fleet app scan")
}

func (s *obsReadProjectionStore) ListAllInstances(context.Context) ([]state.Instance, error) {
	s.legacyReads.Add(1)
	return nil, errors.New("legacy fleet instance scan")
}

func (s *obsReadProjectionStore) OperatorTenantApps(ctx context.Context, accountID string) ([]api.ObsTenantApp, error) {
	if s.tenantError != nil {
		return nil, s.tenantError
	}
	return s.Store.OperatorTenantApps(ctx, accountID)
}

func (s *obsReadProjectionStore) OperatorCapacityProfiles(ctx context.Context) ([]api.ObsCapacityProfile, error) {
	if s.profileError != nil {
		return nil, s.profileError
	}
	return s.Store.OperatorCapacityProfiles(ctx)
}

func TestObsTenantDetailExactReadProjection(t *testing.T) {
	e := gapEnv(t)
	ctx := context.Background()
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "obs-long-history", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1006; i++ {
		deployment, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployPending, Kind: state.DeploymentKindImage, ImageDigest: "sha256:obs-projection"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := (*e.Store()).UpdateDeploymentStatus(ctx, deployment.ID, state.DeployLive, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	fake := &obsReadProjectionStore{Store: *(*e.S()).Store()}
	*(*e.S()).Store() = statetest.NewStore(fake)
	rec := e.DoOperator(t, "GET", "/v1/admin/obs/tenants/"+(*e.Acct()).ID, nil, nil)
	var got api.ObsTenantDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || len(got.Apps) != 1 || got.Apps[0].Deployments != 1 || got.Account.DeploymentsLiveCount != 1 || fake.legacyReads.Load() != 0 {
		t.Fatalf("legacy_reads=%d status=%d body=%s", fake.legacyReads.Load(), rec.Code, rec.Body.String())
	}
}

func TestObsCapacityAggregateReadProjection(t *testing.T) {
	e := gapEnv(t)
	ctx := context.Background()
	if _, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "obs-small-profile", Type: state.AppTypeApp, RAMMB: 256, CPUMillicores: 500}); err != nil {
		t.Fatal(err)
	}
	fake := &obsReadProjectionStore{Store: *(*e.S()).Store()}
	*(*e.S()).Store() = statetest.NewStore(fake)
	rec := e.Do(t, "GET", "/v1/admin/obs/capacity", nil, nil)
	var got api.ObsCapacityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	profiles := got.Summary.ResourceProfiles
	if rec.Code != http.StatusOK || len(profiles) != 1 || profiles[0].ResourceProfile != "small" || profiles[0].Apps != 1 || profiles[0].ReservedMemoryMB != 256 || fake.legacyReads.Load() != 0 {
		t.Fatalf("legacy_reads=%d status=%d body=%s", fake.legacyReads.Load(), rec.Code, rec.Body.String())
	}
}

func TestObsReadProjectionFailuresAreUnavailable(t *testing.T) {
	for _, target := range []string{"tenant", "tenant360", "capacity"} {
		t.Run(target, func(t *testing.T) {
			e := gapEnv(t)
			fake := &obsReadProjectionStore{Store: *(*e.S()).Store()}
			path := "/v1/admin/obs/tenants/" + (*e.Acct()).ID
			if target == "capacity" {
				path = "/v1/admin/obs/capacity"
				fake.profileError = errors.New("database unavailable")
			} else {
				fake.tenantError = errors.New("database unavailable")
				if target == "tenant360" {
					path += "/360"
				}
			}
			*(*e.S()).Store() = statetest.NewStore(fake)
			rec := e.DoOperator(t, "GET", path, nil, nil)
			if rec.Code != http.StatusServiceUnavailable || fake.legacyReads.Load() != 0 {
				t.Fatalf("legacy_reads=%d status=%d body=%s", fake.legacyReads.Load(), rec.Code, rec.Body.String())
			}
		})
	}
}
