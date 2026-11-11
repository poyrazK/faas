package api_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func lockDays(n int32) *int32 { return &n }

// TestObjectLockPlanCaps pins ADR-20261111123000 decision 8: Object Lock is
// paid-only, and retention stops at the plan's cap (object storage audit OS-6).
func TestObjectLockPlanCaps(t *testing.T) {
	want := map[api.Plan]int32{api.PlanFree: 0, api.PlanHobby: 90, api.PlanPro: 366, api.PlanScale: 2557}
	for plan, days := range want {
		if got := api.MustLimitsFor(plan).ObjectStorage.MaxLockRetentionDays; got != days {
			t.Errorf("%s MaxLockRetentionDays = %d, want %d", plan, got, days)
		}
	}
	// Whole years always fit the cap that names them, leap days included.
	if v := api.MustLimitsFor(api.PlanPro).ObjectStorage.ObjectLockConfigurationViolation(api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "GOVERNANCE", Years: lockDays(1)}}, false); v != nil {
		t.Errorf("Pro 1 year default = %+v, want allowed", v)
	}
	if v := api.MustLimitsFor(api.PlanScale).ObjectStorage.ObjectLockConfigurationViolation(api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Years: lockDays(7)}}, false); v != nil {
		t.Errorf("Scale 7 years default = %+v, want allowed", v)
	}
}

func TestObjectLockConfigurationViolation(t *testing.T) {
	free := api.MustLimitsFor(api.PlanFree).ObjectStorage
	hobby := api.MustLimitsFor(api.PlanHobby).ObjectStorage
	def := func(days, years *int32, event *api.ObjectRetentionPeriod) api.ObjectBucketObjectLockConfiguration {
		return api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: days, Years: years, DefaultEventHold: event}}
	}
	for _, tc := range []struct {
		name    string
		limits  api.ObjectStoragePlanLimits
		c       api.ObjectBucketObjectLockConfiguration
		enabled bool
		want    *api.ObjectLockPlanViolation
	}{
		{"free enable", free, api.ObjectBucketObjectLockConfiguration{Enabled: true}, false, &api.ObjectLockPlanViolation{Kind: "lock_retention_days", Limit: 0, Observed: 1}},
		{"free clears default on a legacy bucket", free, api.ObjectBucketObjectLockConfiguration{Enabled: true}, true, nil},
		{"free default", free, def(lockDays(1), nil, nil), true, &api.ObjectLockPlanViolation{Kind: "lock_retention_days", Limit: 0, Observed: 1}},
		{"hobby at cap", hobby, def(lockDays(90), nil, nil), false, nil},
		{"hobby past cap", hobby, def(lockDays(91), nil, nil), false, &api.ObjectLockPlanViolation{Kind: "lock_retention_days", Limit: 90, Observed: 91}},
		{"hobby one year", hobby, def(nil, lockDays(1), nil), false, &api.ObjectLockPlanViolation{Kind: "lock_retention_days", Limit: 90, Observed: 365}},
		{"hobby event hold past cap", hobby, def(nil, nil, &api.ObjectRetentionPeriod{Days: lockDays(100)}), false, &api.ObjectLockPlanViolation{Kind: "lock_retention_days", Limit: 90, Observed: 100}},
		{"hobby event hold at cap", hobby, def(nil, nil, &api.ObjectRetentionPeriod{Days: lockDays(90)}), false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.limits.ObjectLockConfigurationViolation(tc.c, tc.enabled)
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("violation = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestObjectWriteProtectionViolation(t *testing.T) {
	now := time.Date(2027, 2, 1, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	day := 24 * time.Hour
	free := api.MustLimitsFor(api.PlanFree).ObjectStorage
	hobby := api.MustLimitsFor(api.PlanHobby).ObjectStorage
	pro := api.MustLimitsFor(api.PlanPro).ObjectStorage
	for _, tc := range []struct {
		name   string
		limits api.ObjectStoragePlanLimits
		p      api.ObjectWriteProtection
		want   *api.ObjectLockPlanViolation
	}{
		{"free retention", free, api.ObjectWriteProtection{Retention: &api.ObjectVersionRetention{Mode: "GOVERNANCE", RetainUntilDate: at(time.Hour)}}, &api.ObjectLockPlanViolation{Kind: "lock_retention_days", Limit: 0, Observed: 1}},
		{"free legal hold", free, api.ObjectWriteProtection{LegalHold: &api.ObjectVersionLegalHold{Status: "ON"}}, &api.ObjectLockPlanViolation{Kind: "legal_hold", Limit: 0, Observed: 1}},
		{"free releases legal hold", free, api.ObjectWriteProtection{LegalHold: &api.ObjectVersionLegalHold{Status: "OFF"}}, nil},
		{"free past date", free, api.ObjectWriteProtection{Retention: &api.ObjectVersionRetention{Mode: "GOVERNANCE", RetainUntilDate: at(-day)}}, nil},
		{"hobby 90 days", hobby, api.ObjectWriteProtection{Retention: &api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: at(90*day + time.Minute)}}, nil},
		{"hobby 100 years", hobby, api.ObjectWriteProtection{Retention: &api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: at(36500 * day)}}, &api.ObjectLockPlanViolation{Kind: "lock_retention_days", Limit: 90, Observed: 36500}},
		{"hobby legal hold", hobby, api.ObjectWriteProtection{LegalHold: &api.ObjectVersionLegalHold{Status: "ON"}}, nil},
		{"hobby event hold past cap", hobby, api.ObjectWriteProtection{Retention: &api.ObjectVersionRetention{Mode: "GOVERNANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Years: lockDays(1)}}}, &api.ObjectLockPlanViolation{Kind: "lock_retention_days", Limit: 90, Observed: 365}},
		{"hobby event hold release", hobby, api.ObjectWriteProtection{Retention: &api.ObjectVersionRetention{Mode: "GOVERNANCE", EventHold: "OFF"}}, nil},
		// A year from a date before a leap day is 366 days.
		{"pro one leap year", pro, api.ObjectWriteProtection{Retention: &api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: at(366 * day)}}, nil},
		{"pro past cap", pro, api.ObjectWriteProtection{Retention: &api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: at(367 * day)}}, &api.ObjectLockPlanViolation{Kind: "lock_retention_days", Limit: 366, Observed: 367}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.limits.ObjectWriteProtectionViolation(tc.p, now)
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("violation = %+v, want %+v", got, tc.want)
			}
		})
	}
}
