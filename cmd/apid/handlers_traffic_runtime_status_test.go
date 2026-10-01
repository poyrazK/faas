// adr: 375
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTrafficRuntimeStatusDoesNotInferEnforcement(t *testing.T) {
	now := time.Now().UTC()
	row := state.ServingGatewayTrafficRuntime{NodeName: "a", Generation: 1, ReportedAt: now, DatabaseNow: now,
		GatewayTrafficFeatures: state.GatewayTrafficFeatures{RetryEnabled: true, RateCounterMode: "central", RetryCounterMode: "shared", RetryBackendID: "0123456789abcdef", DeadlineSigning: true, PolicySnapshot: true, SecurityRevocation: true, ManagedHTTP: true, ManagedCircuit: true}}
	for _, tc := range []struct {
		name                                                string
		change                                              func(*state.ServingGatewayTrafficRuntime)
		state, retryState, retryMode, rateMode, counterMode string
		fresh, missing, stale                               int
	}{
		{name: "fresh wiring", state: "observed", retryState: "observed", retryMode: "enabled", rateMode: "central", counterMode: "shared", fresh: 2},
		{name: "missing member", change: func(r *state.ServingGatewayTrafficRuntime) { r.Generation = 0; r.ReportedAt = time.Time{} }, state: "partial", retryState: "unverified", retryMode: "unwired", rateMode: "unwired", counterMode: "unwired", fresh: 1, missing: 1},
		{name: "stale member", change: func(r *state.ServingGatewayTrafficRuntime) {
			r.ReportedAt = now.Add(-api.TrafficRuntimeObservationFreshness - time.Nanosecond)
		}, state: "partial", retryState: "unverified", retryMode: "unwired", rateMode: "unwired", counterMode: "unwired", fresh: 1, stale: 1},
		{name: "future clock", change: func(r *state.ServingGatewayTrafficRuntime) { r.ReportedAt = now.Add(time.Nanosecond) }, state: "partial", retryState: "unverified", retryMode: "unwired", rateMode: "unwired", counterMode: "unwired", fresh: 1, stale: 1},
		{name: "disabled retry", change: func(r *state.ServingGatewayTrafficRuntime) { r.RetryEnabled = false }, state: "observed", retryState: "mixed", retryMode: "mixed", rateMode: "central", counterMode: "shared", fresh: 2},
		{name: "local counter", change: func(r *state.ServingGatewayTrafficRuntime) {
			r.RateCounterMode = "local"
			r.RetryCounterMode = "local"
			r.RetryBackendID = ""
		}, state: "observed", retryState: "observed", retryMode: "enabled", rateMode: "mixed", counterMode: "mixed", fresh: 2},
		{name: "different shared endpoints", change: func(r *state.ServingGatewayTrafficRuntime) { r.RetryBackendID = "fedcba9876543210" }, state: "observed", retryState: "observed", retryMode: "enabled", rateMode: "central", counterMode: "mixed", fresh: 2},
		{name: "exact freshness boundary", change: func(r *state.ServingGatewayTrafficRuntime) {
			r.ReportedAt = now.Add(-api.TrafficRuntimeObservationFreshness)
		}, state: "observed", retryState: "observed", retryMode: "enabled", rateMode: "central", counterMode: "shared", fresh: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			second := row
			second.NodeName = "b"
			if tc.change != nil {
				tc.change(&second)
			}
			got := summarizeTrafficRuntime([]state.ServingGatewayTrafficRuntime{row, second})
			if got.State != tc.state || got.PublicRetry.State != tc.retryState || got.PublicRetry.Mode != tc.retryMode || got.RateCounter.Mode != tc.rateMode || got.RetryCounter.Mode != tc.counterMode || got.FreshGateways != tc.fresh || got.MissingGateways != tc.missing || got.StaleGateways != tc.stale {
				t.Fatalf("traffic status = %+v", got)
			}
			if got.EnforcementStatus != "unverified" || got.Scope != "compute_gateway_wiring" {
				t.Fatalf("wiring promoted to enforcement: %+v", got)
			}
		})
	}
	got := summarizeTrafficRuntime(nil)
	if got.State != "unverified" || got.PublicRetry.State != "unverified" || got.EnforcementStatus != "unverified" {
		t.Fatalf("empty fleet = %+v", got)
	}
}

type runtimeTrafficStatusTestStore struct {
	*runtimePolicyStatusTestStore
	rows         []state.ServingGatewayTrafficRuntime
	err          error
	trafficReads int
}

func (s *runtimeTrafficStatusTestStore) ListServingGatewayTrafficRuntime(context.Context) ([]state.ServingGatewayTrafficRuntime, error) {
	s.trafficReads++
	return s.rows, s.err
}

func TestGetRuntimePolicyStatusTrafficObservationsRespectAppOwnership(t *testing.T) {
	base := state.NewMemStore()
	owner, err := base.CreateAccount(t.Context(), "traffic-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	other, err := base.CreateAccount(t.Context(), "traffic-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := base.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "traffic-status", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	store := &runtimeTrafficStatusTestStore{runtimePolicyStatusTestStore: &runtimePolicyStatusTestStore{Store: base}}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", &captureNotifier{}, "", noopMailer{}, stubGithubdClient{}, nil, nil, 0, "")
	call := func(acct state.Account) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/apps/traffic-status/policy/status", nil)
		r.SetPathValue("slug", app.Slug)
		w := httptest.NewRecorder()
		srv.getRuntimePolicyStatus(w, r, acct)
		return w
	}
	if w := call(other); w.Code != http.StatusNotFound || store.trafficReads != 0 {
		t.Fatalf("foreign owner: code=%d reads=%d", w.Code, store.trafficReads)
	}
	now := time.Now().UTC()
	store.rows = []state.ServingGatewayTrafficRuntime{{Generation: 1, ReportedAt: now, DatabaseNow: now, GatewayTrafficFeatures: state.GatewayTrafficFeatures{RateCounterMode: "local", RetryCounterMode: "local"}}}
	w := call(owner)
	var response api.RuntimePolicyStatusResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatalf("status response = %d %s", w.Code, w.Body.String())
	}
	if response.TrafficRuntime.State != "observed" || response.TrafficRuntime.RateCounter.Mode != "local" || response.TrafficRuntime.EnforcementStatus != "unverified" {
		t.Fatalf("observations = %+v", response.TrafficRuntime)
	}
	store.err = errors.New("database unavailable")
	if w := call(owner); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed observation read = %d %s", w.Code, w.Body.String())
	}
}
