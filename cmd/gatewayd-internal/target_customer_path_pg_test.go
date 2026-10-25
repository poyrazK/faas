// adr: 570
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type customerPathResidentScheduler struct {
	gateway.NoopScheduler
	calls atomic.Int32
}

func (s *customerPathResidentScheduler) AdmitInstance(context.Context, string, string, string, string) (string, string, string, string, int32, bool, int, error) {
	s.calls.Add(1)
	return "", "", "", "", 0, false, 0, fmt.Errorf("customer path must hydrate its existing resident")
}

func TestTargetCustomerPathPostgresDiscoversExistingResident(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "customer-placement@test.local", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "customer-placement", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("1", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	router := pgRouter{store: store, appsSuffix: ".apps.test.example", tenantSurfacesEnabled: func() bool { return false }}
	scheduler := &customerPathResidentScheduler{}
	backend := gateway.NewPGBackend(router, scheduler, discardLogger()).WithStore(weightsStoreAdapter{store: store}).
		WithTargetPlacementLoader(newTargetPlacementLoader(store)).WithTargetReadinessLoader(newTargetReadinessLoader(store))
	forwarded := 0
	handler := gateway.NewHandlerWith(backend, gateway.NewMetrics(), discardLogger()).WithPublicRoutingPolicy(newPublicRoutingPinner(store)).WithForwarding(func(target gateway.Target) http.Handler {
		if target.AppID != app.ID || target.DeploymentID != dep.ID || target.InstanceID != instance.ID || target.NodeID != node.ID {
			t.Errorf("wrong resident identity: %+v", target)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwarded++; w.WriteHeader(http.StatusNoContent) })
	})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "http://customer-placement.apps.test.example/", nil))
	if w.Code != http.StatusNoContent || forwarded != 1 || backend.CapacityCount(app.ID) != 1 || !backend.PickForDeployment(app.ID, dep.ID).OK {
		t.Fatalf("resident customer route: status=%d body=%s forwarded=%d capacity=%d pick=%+v", w.Code, w.Body.String(), forwarded, backend.CapacityCount(app.ID), backend.PickForDeployment(app.ID, dep.ID))
	}
	if scheduler.calls.Load() != 0 || backend.HealthyCount(app.ID) != 0 || backend.HealthyCountForDeployments(app.ID, []string{dep.ID}) != 1 {
		t.Fatalf("snapshot route borrowed mutable weights or re-admitted resident: admissions=%d weighted=%d captured=%d", scheduler.calls.Load(), backend.HealthyCount(app.ID), backend.HealthyCountForDeployments(app.ID, []string{dep.ID}))
	}
}
