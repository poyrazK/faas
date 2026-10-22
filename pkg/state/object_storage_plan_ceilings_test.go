// adr: 1320

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func planCeilingPolicy() api.ObjectStoragePolicy {
	return api.ObjectStoragePolicy{MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 20, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 100, MaxMonthlyEgressBytes: 100, MaxMonthlyAuthorizations: 100, MaxReportAgeSeconds: 3600}
}

// planCeilingSnapshot is a fresh provider-accounted snapshot of one ready
// bucket holding storedBytes, with requests and egress reported this month.
func planCeilingSnapshot(now time.Time, storedBytes, requests, egress int64, plan api.ObjectStoragePlanLimits) state.ObjectUsageSnapshot {
	bucket := state.ObjectBucket{ID: "bucket", BackendID: "backend", BackendFingerprint: "fp", State: "ready", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
	return state.ObjectUsageSnapshot{
		Buckets:    []state.ObjectBucketUsage{{Bucket: bucket, ObservedBytes: storedBytes, ObservedKeys: 1, ObservedAt: now.Add(-time.Minute)}},
		Reports:    []api.ObjectStorageUsageReport{{BackendID: "backend", BackendFingerprint: "fp", PeriodStart: state.ObjectStoragePeriod(now), ObservedAt: now.Add(-time.Minute), RequestCount: requests, EgressBytes: egress}},
		PlanLimits: plan,
	}
}

func wantObjectLimit(t *testing.T, err error, kind string, limit int64) {
	t.Helper()
	if kind == "" {
		if err != nil {
			t.Fatalf("err = %v, want admitted", err)
		}
		return
	}
	var le *state.ObjectStorageLimitError
	if !errors.As(err, &le) || le.Kind != kind || le.Limit != limit {
		t.Fatalf("err = %v, want %s limit %d", err, kind, limit)
	}
}

func TestObjectAdmissionEnforcesPlanCeilings(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name                     string
		stored, requests, egress int64
		plan                     api.ObjectStoragePlanLimits
		put                      int64
		kind                     string
		limit                    int64
	}{
		{name: "zero plan keeps operator bytes", stored: 50, put: 40},
		{name: "plan account bytes stop a put", stored: 5, put: 6, plan: api.ObjectStoragePlanLimits{MaxAccountBytes: 10}, kind: "account_bytes", limit: 10},
		{name: "plan account bytes below operator bucket cap", stored: 5, put: 5, plan: api.ObjectStoragePlanLimits{MaxAccountBytes: 10}},
		{name: "plan account bytes above operator keep operator", stored: 50, put: 51, plan: api.ObjectStoragePlanLimits{MaxAccountBytes: 1_000}, kind: "account_bytes", limit: 100},
		{name: "plan requests stop admission", requests: 3, plan: api.ObjectStoragePlanLimits{MaxMonthlyRequests: 3}, kind: "requests", limit: 3},
		{name: "plan egress stops admission", egress: 7, plan: api.ObjectStoragePlanLimits{MaxMonthlyEgressBytes: 7}, kind: "egress_bytes", limit: 7},
		{name: "usage under plan ceilings admitted", stored: 1, requests: 2, egress: 2, put: 1, plan: api.ObjectStoragePlanLimits{MaxAccountBytes: 10, MaxMonthlyRequests: 3, MaxMonthlyEgressBytes: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := planCeilingSnapshot(now, tc.stored, tc.requests, tc.egress, tc.plan)
			_, _, err := state.CheckObjectAdmission(s, "bucket", tc.put, 0, false, true, planCeilingPolicy(), now)
			wantObjectLimit(t, err, tc.kind, tc.limit)
		})
	}
}

func TestGatewayChecksEnforcePlanCeilings(t *testing.T) {
	now := time.Now().UTC()
	p := planCeilingPolicy()
	p.AccountingMode = api.ObjectStorageGatewaySafetyV1
	p.MaxMonthlyCostMillicents = 0
	since := now.Add(-2 * time.Hour)
	p.GatewayMeteringSince = &since
	for _, tc := range []struct {
		name             string
		requests, egress int64
		reserve          int64
		plan             api.ObjectStoragePlanLimits
		kind             string
		limit            int64
	}{
		{name: "zero plan keeps operator egress", egress: 50, reserve: 50},
		{name: "plan egress stops a reservation", egress: 8, reserve: 3, plan: api.ObjectStoragePlanLimits{MaxMonthlyEgressBytes: 10}, kind: "egress_bytes", limit: 10},
		{name: "plan egress reached stops a request", egress: 10, plan: api.ObjectStoragePlanLimits{MaxMonthlyEgressBytes: 10}, kind: "egress_bytes", limit: 10},
		{name: "plan requests stop a request", requests: 2, plan: api.ObjectStoragePlanLimits{MaxMonthlyRequests: 2}, kind: "requests", limit: 2},
		{name: "usage under plan ceilings admitted", requests: 1, egress: 1, reserve: 1, plan: api.ObjectStoragePlanLimits{MaxMonthlyRequests: 2, MaxMonthlyEgressBytes: 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := planCeilingSnapshot(now, 1, 0, 0, tc.plan)
			s.Buckets[0].GatewayMetricsKnown = true
			s.Buckets[0].RequestCount, s.Buckets[0].EgressBytes = tc.requests, tc.egress
			err := state.CheckGatewayRequest(s, "bucket", now, p)
			if err == nil {
				err = state.CheckGatewayEgress(s, "bucket", tc.reserve, now, p)
			}
			wantObjectLimit(t, err, tc.kind, tc.limit)
		})
	}
}
