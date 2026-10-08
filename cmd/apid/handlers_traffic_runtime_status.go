// adr: 570
package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type trafficRuntimeStatusStore interface {
	ListServingGatewayTrafficRuntime(context.Context) ([]state.ServingGatewayTrafficRuntime, error)
}

func summarizeTrafficRuntime(rows []state.ServingGatewayTrafficRuntime) api.TrafficRuntimeStatus {
	s := api.TrafficRuntimeStatus{Scope: "compute_gateway_wiring", State: "unverified", EnforcementStatus: "unverified", ServingGateways: len(rows)}
	fresh := make([]state.ServingGatewayTrafficRuntime, 0, len(rows))
	for _, row := range rows {
		if row.Generation <= 0 || row.ReportedAt.IsZero() {
			s.MissingGateways++
		} else if row.DatabaseNow.IsZero() || row.ReportedAt.After(row.DatabaseNow) || row.ReportedAt.Before(row.DatabaseNow.Add(-api.TrafficRuntimeObservationFreshness)) {
			s.StaleGateways++
		} else {
			fresh = append(fresh, row)
		}
	}
	s.FreshGateways = len(fresh)
	complete := len(rows) > 0 && len(fresh) == len(rows)
	if complete {
		s.State = "observed"
	} else if len(fresh) > 0 {
		s.State = "partial"
	}
	feature := func(value func(state.ServingGatewayTrafficRuntime) string) api.TrafficRuntimeFeatureStatus {
		f := api.TrafficRuntimeFeatureStatus{State: "unverified", Mode: "unwired"}
		if !complete {
			return f
		}
		f.Mode, f.State = value(fresh[0]), "observed"
		for _, row := range fresh[1:] {
			if value(row) != f.Mode {
				return api.TrafficRuntimeFeatureStatus{State: "mixed", Mode: "mixed"}
			}
		}
		return f
	}
	boolean := func(v bool) string {
		if v {
			return "enabled"
		}
		return "disabled"
	}
	s.PublicRetry = feature(func(r state.ServingGatewayTrafficRuntime) string { return boolean(r.RetryEnabled) })
	s.RateCounter = feature(func(r state.ServingGatewayTrafficRuntime) string { return r.RateCounterMode })
	s.RetryCounter = feature(func(r state.ServingGatewayTrafficRuntime) string { return r.RetryCounterMode })
	if complete && (s.RetryCounter.Mode == "shared" || s.RetryCounter.Mode == "redis") {
		backendID := fresh[0].RetryBackendID
		for _, row := range fresh {
			if backendID == "" || row.RetryBackendID != backendID {
				s.RetryCounter = api.TrafficRuntimeFeatureStatus{State: "mixed", Mode: "mixed"}
			}
		}
	}
	s.DeadlineSigning = feature(func(r state.ServingGatewayTrafficRuntime) string { return boolean(r.DeadlineSigning) })
	s.PolicySnapshot = feature(func(r state.ServingGatewayTrafficRuntime) string { return boolean(r.PolicySnapshot) })
	s.SecurityRevocation = feature(func(r state.ServingGatewayTrafficRuntime) string { return boolean(r.SecurityRevocation) })
	s.ManagedHTTP = feature(func(r state.ServingGatewayTrafficRuntime) string { return boolean(r.ManagedHTTP) })
	s.ManagedCircuit = feature(func(r state.ServingGatewayTrafficRuntime) string { return boolean(r.ManagedCircuit) })
	return s
}

func (s *server) loadTrafficRuntimeStatus(ctx context.Context, gateways []state.ServingGatewayControlPlaneState) (api.TrafficRuntimeStatus, error) {
	store, ok := s.store.(trafficRuntimeStatusStore)
	if !ok {
		status := summarizeTrafficRuntime(nil)
		status.ServingGateways, status.MissingGateways = len(gateways), len(gateways)
		return status, nil
	}
	// Bound this additive read separately from the existing optional policy wait.
	readCtx, cancel := context.WithTimeout(ctx, api.TrafficRuntimeObservationTimeout)
	defer cancel()
	rows, err := store.ListServingGatewayTrafficRuntime(readCtx)
	if err != nil {
		return api.TrafficRuntimeStatus{}, err
	}
	return summarizeTrafficRuntime(rows), nil
}
